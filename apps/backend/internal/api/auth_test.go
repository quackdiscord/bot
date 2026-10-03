package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/config"
	"github.com/quackdiscord/bot/internal/quack"
	storage "github.com/quackdiscord/bot/internal/store"
	"github.com/quackdiscord/bot/internal/testutil"
)

func TestAuthSessionJSONNeverExposesCredentials(t *testing.T) {
	session := &quack.AuthSession{
		ID: "session-secret", DiscordUserID: "user-1", AccessToken: "access-secret",
		RefreshToken: "refresh-secret", CSRFToken: "csrf-secret",
	}
	body, err := json.Marshal(session)
	if err != nil {
		t.Fatalf("marshal session: %v", err)
	}
	for _, secret := range []string{"session-secret", "access-secret", "refresh-secret", "csrf-secret"} {
		if strings.Contains(string(body), secret) {
			t.Fatalf("session JSON exposed %q: %s", secret, body)
		}
	}
}

func TestAuthCookieAttributes(t *testing.T) {
	cfg := config.Default()
	cfg.Auth.CookieSecure = true
	server := newTestServer(t, cfg, Deps{})
	recorder := httptest.NewRecorder()
	server.setAuthCookies(recorder, "session-secret", "csrf-secret", 3600)
	cookies := recorder.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatalf("got %d cookies, want 2: %v", len(cookies), recorder.Header())
	}
	session, csrf := cookies[0], cookies[1]
	if session.Name != cfg.Auth.SessionCookieName || session.Value != "session-secret" || !session.Secure ||
		!session.HttpOnly || session.SameSite != http.SameSiteLaxMode || session.Path != "/" || session.MaxAge != 3600 {
		t.Errorf("session cookie = %+v", session)
	}
	if csrf.Name != cfg.Auth.CSRFCookieName || !csrf.Secure || csrf.HttpOnly || csrf.SameSite != http.SameSiteLaxMode || csrf.Path != "/" {
		t.Errorf("CSRF cookie = %+v", csrf)
	}
}

// authServer serves the API over a store with OAuth configured and Discord
// replaced by discord.
func authServer(t *testing.T, store *storage.Store, discord http.Handler) *Server {
	t.Helper()
	cfg := config.Default()
	cfg.Discord.AppID = "app"
	cfg.Discord.ClientSecret = "client-secret"
	cfg.Discord.OAuthRedirectURI = "https://dashboard.example.com/callback"
	server := storeServer(t, store, nil, nil, cfg)
	if discord != nil {
		fake := httptest.NewServer(discord)
		t.Cleanup(fake.Close)
		server.oauth = oauthClient{tokenURL: fake.URL + "/token", meURL: fake.URL + "/me", http: fake.Client()}
	}
	return server
}

func TestExpiredDiscordGrantRevokesSession(t *testing.T) {
	store := testutil.NewSQLiteRedisStore(t)
	now := time.Now().UTC()
	session := &quack.AuthSession{
		ID: "expired-session", DiscordUserID: "user-1", AccessToken: "token-secret",
		TokenExpiresAt: now.Add(-time.Minute), SessionExpiresAt: now.Add(time.Hour), CreatedAt: now, LastSeenAt: now,
	}
	saveSession(t, store, session)
	server := authServer(t, store, nil)

	response := send(t, server, http.MethodGet, "/auth/me", "", session.ID)
	assertEnvelope(t, response, http.StatusUnauthorized, codeReauthenticate)
	if strings.Contains(response.Body.String(), session.ID) || strings.Contains(response.Body.String(), session.AccessToken) {
		t.Fatalf("response exposed credentials: %s", response.Body.String())
	}
	if loaded, err := store.GetSession(context.Background(), session.ID); err != nil || loaded != nil {
		t.Fatalf("session after expiry = %+v, %v; want deleted", loaded, err)
	}
}

func TestAuthMeAndLogoutAll(t *testing.T) {
	store := testutil.NewSQLiteRedisStore(t)
	now := time.Now().UTC()
	first := &quack.AuthSession{ID: "session-one", DiscordUserID: "user-1", AccessToken: "access-one", RefreshToken: "refresh-one",
		TokenExpiresAt: now.Add(time.Hour), SessionExpiresAt: now.Add(time.Hour), CreatedAt: now, LastSeenAt: now}
	second := &quack.AuthSession{ID: "session-two", DiscordUserID: "user-1", AccessToken: "access-two", RefreshToken: "refresh-two",
		TokenExpiresAt: now.Add(time.Hour), SessionExpiresAt: now.Add(time.Hour), CreatedAt: now, LastSeenAt: now}
	saveSession(t, store, first)
	saveSession(t, store, second)
	server := authServer(t, store, nil)
	cfg := server.cfg

	request := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	request.AddCookie(&http.Cookie{Name: cfg.Auth.SessionCookieName, Value: first.ID})
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	expectStatus(t, response, http.StatusOK)
	for _, secret := range []string{first.ID, first.AccessToken, first.RefreshToken} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatalf("auth/me exposed %q: %s", secret, response.Body.String())
		}
	}
	var me struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &me); err != nil || me.CSRFToken == "" {
		t.Fatalf("auth/me CSRF token = %+v, %v", me, err)
	}
	refreshed := false
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == cfg.Auth.CSRFCookieName && cookie.Value == me.CSRFToken && !cookie.HttpOnly {
			refreshed = true
		}
	}
	if !refreshed {
		t.Fatalf("cookie session did not get a matching CSRF cookie: %v", response.Header())
	}

	expectStatus(t, send(t, server, http.MethodPost, "/auth/logout-all", "", first.ID), http.StatusNoContent)
	for _, id := range []string{first.ID, second.ID} {
		if loaded, err := store.GetSession(context.Background(), id); err != nil || loaded != nil {
			t.Fatalf("session %s = %+v, %v; want revoked", id, loaded, err)
		}
	}
}

func TestLogoutClearsCookies(t *testing.T) {
	store := testutil.NewSQLiteRedisStore(t)
	sessionID := saveSession(t, store, testSession("user-1"))
	server := authServer(t, store, nil)
	response := send(t, server, http.MethodPost, "/auth/logout", "", sessionID)
	expectStatus(t, response, http.StatusNoContent)
	cookies := response.Result().Cookies()
	if len(cookies) != 2 || cookies[0].MaxAge >= 0 || cookies[1].MaxAge >= 0 {
		t.Fatalf("logout cookies = %+v", cookies)
	}
	if loaded, _ := store.GetSession(context.Background(), sessionID); loaded != nil {
		t.Fatal("logout kept the session")
	}
}
