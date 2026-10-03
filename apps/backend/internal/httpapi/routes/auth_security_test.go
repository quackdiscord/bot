package routes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/config"
	"github.com/quackdiscord/bot/internal/httpapi/apierror"
	"github.com/quackdiscord/bot/internal/httpapi/middleware"
	"github.com/quackdiscord/bot/internal/quack"
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
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	cfg := config.Default()
	cfg.Auth.CookieSecure = true
	setAuthCookies(c, cfg, "session-secret", "csrf-secret", 3600)
	cookies := recorder.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatalf("expected two cookies, got %d headers=%v", len(cookies), recorder.Header())
	}
	if cookies[0].Name != cfg.Auth.SessionCookieName || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode || cookies[0].Path != "/" {
		t.Fatalf("unexpected session cookie: %+v", cookies[0])
	}
	if cookies[1].Name != cfg.Auth.CSRFCookieName || !cookies[1].Secure || cookies[1].HttpOnly || cookies[1].SameSite != http.SameSiteLaxMode || cookies[1].Path != "/" {
		t.Fatalf("unexpected CSRF cookie: %+v", cookies[1])
	}
}

func TestExpiredOAuthTokenForcesStableReauthenticationAndRevokesSession(t *testing.T) {
	store := testutil.NewSQLiteRedisStore(t)
	now := time.Now().UTC()
	session := &quack.AuthSession{
		ID: "expired-session", DiscordUserID: "user-1", AccessToken: "token-secret",
		TokenExpiresAt: now.Add(-time.Minute), SessionExpiresAt: now.Add(time.Hour), CreatedAt: now, LastSeenAt: now,
	}
	if err := store.SaveSession(context.Background(), session, time.Hour); err != nil {
		t.Fatalf("save session: %v", err)
	}
	services := testDeps(store, nil, nil)
	router := authTestRouter(services)
	request := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	request.Header.Set("Authorization", "Bearer "+session.ID)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", response.Code, response.Body.String())
	}
	var body apierror.Response
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Error.Code != apierror.CodeReauthenticate {
		t.Fatalf("unexpected reauthentication response: %+v err=%v", body, err)
	}
	if strings.Contains(response.Body.String(), session.ID) || strings.Contains(response.Body.String(), session.AccessToken) {
		t.Fatalf("expired response exposed credentials: %s", response.Body.String())
	}
	loaded, err := store.GetSession(context.Background(), session.ID)
	if err != nil || loaded != nil {
		t.Fatalf("expected expired session revocation, got %+v err=%v", loaded, err)
	}
}

func TestAuthMeAndLogoutAllDoNotExposeOrRetainSessions(t *testing.T) {
	store := testutil.NewSQLiteRedisStore(t)
	now := time.Now().UTC()
	first := &quack.AuthSession{ID: "session-one", DiscordUserID: "user-1", AccessToken: "access-one", RefreshToken: "refresh-one", TokenExpiresAt: now.Add(time.Hour), SessionExpiresAt: now.Add(time.Hour), CreatedAt: now, LastSeenAt: now}
	second := &quack.AuthSession{ID: "session-two", DiscordUserID: "user-1", AccessToken: "access-two", RefreshToken: "refresh-two", TokenExpiresAt: now.Add(time.Hour), SessionExpiresAt: now.Add(time.Hour), CreatedAt: now, LastSeenAt: now}
	for _, session := range []*quack.AuthSession{first, second} {
		if err := store.SaveSession(context.Background(), session, time.Hour); err != nil {
			t.Fatalf("save session: %v", err)
		}
	}
	services := testDeps(store, nil, nil)
	router := authTestRouter(services)
	request := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	request.AddCookie(&http.Cookie{Name: services.Config.Auth.SessionCookieName, Value: first.ID})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("auth me: status=%d body=%s", response.Code, response.Body.String())
	}
	for _, secret := range []string{first.ID, first.AccessToken, first.RefreshToken} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatalf("auth me exposed %q: %s", secret, response.Body.String())
		}
	}
	var meBody struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &meBody); err != nil || meBody.CSRFToken == "" {
		t.Fatalf("expected readable CSRF challenge in auth me response: %+v err=%v", meBody, err)
	}
	foundCSRFCookie := false
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == services.Config.Auth.CSRFCookieName && cookie.Value == meBody.CSRFToken {
			foundCSRFCookie = true
		}
	}
	if !foundCSRFCookie {
		t.Fatalf("expected matching host-only CSRF cookie, headers=%v", response.Header())
	}

	request = httptest.NewRequest(http.MethodPost, "/auth/logout-all", nil)
	request.Header.Set("Authorization", "Bearer "+first.ID)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("logout all: status=%d body=%s", response.Code, response.Body.String())
	}
	for _, sessionID := range []string{first.ID, second.ID} {
		loaded, err := store.GetSession(context.Background(), sessionID)
		if err != nil || loaded != nil {
			t.Fatalf("expected %s revoked, got %+v err=%v", sessionID, loaded, err)
		}
	}
}

func TestRevokedDiscordGrantReturnsSafeReauthentication(t *testing.T) {
	store := testutil.NewSQLiteRedisStore(t)
	services := testDeps(store, nil, nil)
	services.Config.Discord.AppID = "app"
	services.Config.Discord.ClientSecret = "client-secret"
	services.Config.Discord.OAuthRedirectURI = "https://dashboard.example.com/callback"
	if err := store.SaveOAuthState(context.Background(), "state-id", &quack.OAuthState{ResponseMode: "json", CreatedAt: time.Now().UTC()}, time.Minute); err != nil {
		t.Fatalf("save state: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"revoked-grant-secret"}`))
	}))
	defer server.Close()
	previousEndpoint, previousClient := discordTokenEndpoint, discordHTTPClient
	discordTokenEndpoint, discordHTTPClient = server.URL, server.Client()
	t.Cleanup(func() { discordTokenEndpoint, discordHTTPClient = previousEndpoint, previousClient })

	router := authTestRouter(services)
	request := httptest.NewRequest(http.MethodGet, "/auth/discord/callback?code=code-secret&state=state-id", nil)
	request.AddCookie(&http.Cookie{Name: oauthStateCookieName(services.Config), Value: "state-id"})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", response.Code, response.Body.String())
	}
	var body apierror.Response
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Error.Code != apierror.CodeReauthenticate {
		t.Fatalf("unexpected body: %+v err=%v", body, err)
	}
	for _, secret := range []string{"revoked-grant-secret", "code-secret", "client-secret", "state-id"} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatalf("OAuth failure exposed %q: %s", secret, response.Body.String())
		}
	}
}

func TestOAuthJSONCallbackReturnsOnlySafeUserContract(t *testing.T) {
	store := testutil.NewSQLiteRedisStore(t)
	services := testDeps(store, nil, nil)
	services.Config.Discord.AppID = "app"
	services.Config.Discord.ClientSecret = "client-secret"
	services.Config.Discord.OAuthRedirectURI = "https://dashboard.example.com/callback"
	if err := store.SaveOAuthState(context.Background(), "state-id", &quack.OAuthState{ResponseMode: "json", CreatedAt: time.Now().UTC()}, time.Minute); err != nil {
		t.Fatalf("save state: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"access-secret","refresh_token":"refresh-secret","token_type":"Bearer","scope":"identify","expires_in":3600}`))
		case "/me":
			if request.Header.Get("Authorization") != "Bearer access-secret" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"user-1","username":"user","global_name":"User","avatar":"avatar"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	previousTokenEndpoint, previousMeEndpoint, previousClient := discordTokenEndpoint, discordMeEndpoint, discordHTTPClient
	discordTokenEndpoint, discordMeEndpoint, discordHTTPClient = server.URL+"/token", server.URL+"/me", server.Client()
	t.Cleanup(func() {
		discordTokenEndpoint, discordMeEndpoint, discordHTTPClient = previousTokenEndpoint, previousMeEndpoint, previousClient
	})

	router := authTestRouter(services)
	request := httptest.NewRequest(http.MethodGet, "/auth/discord/callback?code=code-secret&state=state-id", nil)
	request.AddCookie(&http.Cookie{Name: oauthStateCookieName(services.Config), Value: "state-id"})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", response.Code, response.Body.String())
	}
	for _, secret := range []string{"access-secret", "refresh-secret", "code-secret", "state-id", "session_id"} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatalf("OAuth callback exposed %q: %s", secret, response.Body.String())
		}
	}
	var body struct {
		CSRFToken string `json:"csrf_token"`
		User      struct {
			ID string `json:"id"`
		} `json:"user"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.CSRFToken == "" || body.User.ID != "user-1" || body.ExpiresAt.IsZero() {
		t.Fatalf("unexpected safe callback contract: %+v err=%v", body, err)
	}
	if len(response.Result().Cookies()) != 3 {
		t.Fatalf("expected session and CSRF cookies, headers=%v", response.Header())
	}
}

// authTestRouter builds the route contract with trace and error normalization but without browser-only CSRF concerns.
func authTestRouter(services *Deps) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.RequestContext, middleware.ErrorEnvelope)
	SetupRoutes(router, services)
	return router
}

func TestOAuthCallbackRequiresInitiatingBrowser(t *testing.T) {
	for _, binding := range []string{"", "different-browser"} {
		t.Run(binding, func(t *testing.T) {
			store := testutil.NewSQLiteRedisStore(t)
			services := testDeps(store, nil, nil)
			services.Config.Discord.AppID = "app"
			services.Config.Discord.ClientSecret = "secret"
			services.Config.Discord.OAuthRedirectURI = "https://api.example/auth/discord/callback"
			if err := store.SaveOAuthState(context.Background(), "state-id", &quack.OAuthState{}, time.Minute); err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodGet, "/auth/discord/callback?code=code&state=state-id", nil)
			if binding != "" {
				request.AddCookie(&http.Cookie{Name: oauthStateCookieName(services.Config), Value: binding})
			}
			response := httptest.NewRecorder()
			authTestRouter(services).ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			state, err := store.ConsumeOAuthState(context.Background(), "state-id")
			if err != nil || state == nil {
				t.Fatalf("wrong browser consumed state: %v", err)
			}
		})
	}
}

func TestRedirectTargetRejectsExternalAndBrowserNormalizedURLs(t *testing.T) {
	fallback := "https://dashboard.example/cases"
	for _, target := range []string{"//evil.example", `/\evil.example`, `/%5cevil.example`, "/%2fevil.example", "https://evil.example", "http://dashboard.example", "ftp://dashboard.example", "https://user@dashboard.example"} {
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
		t.Fatalf("unsafe fallback: %s", got)
	}
}

func TestOAuthLoginSetsHostBoundStateCookie(t *testing.T) {
	services := testDeps(testutil.NewSQLiteRedisStore(t), nil, nil)
	services.Config.Discord.AppID = "app"
	services.Config.Discord.ClientSecret = "secret"
	services.Config.Discord.OAuthRedirectURI = "https://api.example/auth/discord/callback"
	services.Config.Auth.CookieSecure = true
	response := httptest.NewRecorder()
	authTestRouter(services).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/auth/discord/login?mode=json", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "__Host-quack_oauth_state" || cookies[0].Value != body.State || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].Domain != "" || cookies[0].Path != "/" || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatal("OAuth browser binding cookie is missing or insecure")
	}
}
