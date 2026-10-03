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

func TestOAuthCallback(t *testing.T) {
	discord := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			_, _ = w.Write([]byte(`{"access_token":"access-secret","refresh_token":"refresh-secret","token_type":"Bearer","scope":"identify","expires_in":3600}`))
		case "/me":
			if r.Header.Get("Authorization") != "Bearer access-secret" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"id":"user-1","username":"user","global_name":"User","avatar":"avatar"}`))
		}
	})
	revoked := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"revoked-grant-secret"}`))
	})
	callback := func(t *testing.T, discord http.Handler, mode string, browserState string) (*httptest.ResponseRecorder, *storage.Store) {
		t.Helper()
		store := testutil.NewSQLiteRedisStore(t)
		if err := store.SaveOAuthState(context.Background(), "state-id", &quack.OAuthState{
			ResponseMode: mode, RedirectTo: "/cases", CreatedAt: time.Now().UTC(),
		}, time.Minute); err != nil {
			t.Fatalf("save state: %v", err)
		}
		server := authServer(t, store, discord)
		request := httptest.NewRequest(http.MethodGet, "/auth/discord/callback?code=code-secret&state=state-id", nil)
		if browserState != "" {
			request.AddCookie(&http.Cookie{Name: server.oauthStateCookie(), Value: browserState})
		}
		response := httptest.NewRecorder()
		server.ServeHTTP(response, request)
		return response, store
	}

	t.Run("json mode returns only the safe user contract", func(t *testing.T) {
		response, _ := callback(t, discord, "json", "state-id")
		expectStatus(t, response, http.StatusOK)
		for _, secret := range []string{"access-secret", "refresh-secret", "code-secret", "state-id", "session_id"} {
			if strings.Contains(response.Body.String(), secret) {
				t.Fatalf("callback exposed %q: %s", secret, response.Body.String())
			}
		}
		var body struct {
			CSRFToken string `json:"csrf_token"`
			User      struct {
				ID        string `json:"id"`
				AvatarURL string `json:"avatar_url"`
			} `json:"user"`
			ExpiresAt time.Time `json:"expires_at"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.CSRFToken == "" ||
			body.User.ID != "user-1" || body.User.AvatarURL == "" || body.ExpiresAt.IsZero() {
			t.Fatalf("callback body = %s (err %v)", response.Body.String(), err)
		}
		// The cleared state cookie, the session cookie, and the CSRF cookie.
		if got := len(response.Result().Cookies()); got != 3 {
			t.Fatalf("got %d cookies, want 3: %v", got, response.Header())
		}
	})
	t.Run("redirect mode", func(t *testing.T) {
		response, _ := callback(t, discord, "redirect", "state-id")
		expectStatus(t, response, http.StatusFound)
		if got := response.Header().Get("Location"); got != "/cases" {
			t.Fatalf("Location = %q, want /cases", got)
		}
	})
	t.Run("revoked grant", func(t *testing.T) {
		response, _ := callback(t, revoked, "json", "state-id")
		assertEnvelope(t, response, http.StatusUnauthorized, codeReauthenticate)
		for _, secret := range []string{"revoked-grant-secret", "code-secret", "client-secret", "state-id"} {
			if strings.Contains(response.Body.String(), secret) {
				t.Fatalf("failure exposed %q: %s", secret, response.Body.String())
			}
		}
	})
	for _, browserState := range []string{"", "different-browser"} {
		t.Run("other browser "+browserState, func(t *testing.T) {
			response, store := callback(t, discord, "json", browserState)
			assertEnvelope(t, response, http.StatusUnauthorized, codeReauthenticate)
			if state, err := store.ConsumeOAuthState(context.Background(), "state-id"); err != nil || state == nil {
				t.Fatalf("the wrong browser consumed the state: %v", err)
			}
		})
	}
}

func TestOAuthLogin(t *testing.T) {
	store := testutil.NewSQLiteRedisStore(t)
	server := authServer(t, store, nil)
	server.cfg.Auth.CookieSecure = true

	response := send(t, server, http.MethodGet, "/auth/discord/login?mode=json", "", "")
	expectStatus(t, response, http.StatusOK)
	var body struct {
		AuthURL string `json:"auth_url"`
		State   string `json:"state"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || !strings.HasPrefix(body.AuthURL, discordAuthorizeURL+"?") {
		t.Fatalf("login body = %s (err %v)", response.Body.String(), err)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "__Host-quack_oauth_state" || cookies[0].Value != body.State || !cookies[0].HttpOnly ||
		!cookies[0].Secure || cookies[0].Domain != "" || cookies[0].Path != "/" || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("state cookie = %+v", cookies)
	}

	redirect := send(t, server, http.MethodGet, "/auth/discord/login", "", "")
	expectStatus(t, redirect, http.StatusFound)
	if !strings.HasPrefix(redirect.Header().Get("Location"), discordAuthorizeURL+"?") {
		t.Fatalf("Location = %q", redirect.Header().Get("Location"))
	}

	server.cfg.Discord.ClientSecret = ""
	assertEnvelope(t, send(t, server, http.MethodGet, "/auth/discord/login", "", ""), http.StatusServiceUnavailable, codeDependency)
}

func TestRedirectTargetRejectsExternalAndBrowserNormalizedURLs(t *testing.T) {
	fallback := "https://dashboard.example/cases"
	for _, target := range []string{"//evil.example", `/\evil.example`, `/%5cevil.example`, "/%2fevil.example",
		"https://evil.example", "http://dashboard.example", "ftp://dashboard.example", "https://user@dashboard.example"} {
		if got := sanitizeRedirectTarget(target, fallback); got != fallback {
			t.Errorf("accepted %q as %q", target, got)
		}
	}
	for _, target := range []string{"/cases?sort=new", "https://dashboard.example/settings"} {
		if got := sanitizeRedirectTarget(target, fallback); got != target {
			t.Errorf("rejected safe target %q", target)
		}
	}
	if got := sanitizeRedirectTarget("", "//evil.example"); got != "/" {
		t.Fatalf("unsafe fallback became %q, want /", got)
	}
}
