package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/quackdiscord/bot/internal/config"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/quack"
)

const (
	discordAuthorizeURL = "https://discord.com/oauth2/authorize"
	maxDiscordBodyBytes = 1 << 20
)

// oauthClient talks to Discord's OAuth endpoints. Tests point it at a fake.
type oauthClient struct {
	tokenURL string
	meURL    string
	http     *http.Client
}

func defaultOAuthClient() oauthClient {
	return oauthClient{
		tokenURL: "https://discord.com/api/v10/oauth2/token",
		meURL:    "https://discord.com/api/v10/users/@me",
		http:     &http.Client{Timeout: 10 * time.Second},
	}
}

// discordToken is Discord's answer to an authorization code exchange.
type discordToken struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

// discordUser is the part of Discord's /users/@me that sign-in uses.
// MFAEnabled is recorded per user rather than kept in the session.
type discordUser struct {
	ID         string `json:"id"`
	Username   string `json:"username"`
	GlobalName string `json:"global_name"`
	Avatar     string `json:"avatar"`
	MFAEnabled bool   `json:"mfa_enabled"`
}

// loginQuery is the query /auth/discord/login reads.
type loginQuery struct {
	// Mode "json" returns the authorization URL instead of redirecting.
	Mode string `query:"mode" enum:"redirect,json"`
	// RedirectTo is where the dashboard lands after sign-in. Unsafe targets
	// fall back to the configured post-login page.
	RedirectTo string `query:"redirect_to"`
}

// loginResponse is the mode=json answer to /auth/discord/login.
type loginResponse struct {
	AuthURL string `json:"auth_url"`
	State   string `json:"state"`
}

// callbackQuery is what Discord sends back to /auth/discord/callback.
type callbackQuery struct {
	Code  string `query:"code"`
	State string `query:"state"`
	// Error is set when the user declined.
	Error string `query:"error"`
}

// signInResponse is the mode=json answer to /auth/discord/callback.
type signInResponse struct {
	CSRFToken string              `json:"csrf_token"`
	ExpiresAt time.Time           `json:"expires_at"`
	User      sessionUserResponse `json:"user"`
}

// requireOAuth answers 503 when the Discord application is not configured
// for sign-in.
func (s *Server) requireOAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d := s.cfg.Discord
		if strings.TrimSpace(d.AppID) == "" || strings.TrimSpace(d.ClientSecret) == "" ||
			strings.TrimSpace(d.OAuthRedirectURI) == "" {
			writeError(w, r, http.StatusServiceUnavailable, codeDependency, "Discord sign-in is unavailable")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// discordLogin starts sign-in. It stores a single-use state, binds it to this
// browser with a cookie, and either redirects to Discord or, with mode=json,
// returns the authorization URL.
func (s *Server) discordLogin(w http.ResponseWriter, r *http.Request) {
	var query loginQuery
	modules.DecodeQuery(r, &query)
	mode := "redirect"
	if strings.EqualFold(strings.TrimSpace(query.Mode), "json") {
		mode = "json"
	}
	stateID := quack.NewID()
	state := &quack.OAuthState{
		RedirectTo:   sanitizeRedirectTarget(query.RedirectTo, s.cfg.Auth.PostLoginRedirect),
		ResponseMode: mode,
		CreatedAt:    time.Now().UTC(),
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	stateTTL := s.cfg.Auth.StateTTL
	if err := s.store.SaveOAuthState(ctx, stateID, state, stateTTL); err != nil {
		slog.ErrorContext(ctx, "oauth state dependency unavailable")
		writeError(w, r, http.StatusServiceUnavailable, codeDependency, "Discord sign-in is temporarily unavailable")
		return
	}

	s.setCookie(w, s.oauthStateCookie(), stateID, int(stateTTL.Seconds()), true)
	authURL := s.discordAuthURL(stateID)
	if mode == "json" {
		writeJSON(w, http.StatusOK, loginResponse{AuthURL: authURL, State: stateID})
		return
	}
	http.Redirect(w, r, authURL, http.StatusFound)
}

// discordCallback finishes sign-in: it checks the state against the browser
// cookie before consuming it, exchanges the code, records the user's 2FA
// status, and creates the session. Discord's error text is never echoed
// back.
func (s *Server) discordCallback(w http.ResponseWriter, r *http.Request) {
	var query callbackQuery
	modules.DecodeQuery(r, &query)
	if query.Error != "" {
		writeError(w, r, http.StatusUnauthorized, codeReauthenticate, "Discord authorization was not granted; sign in again")
		return
	}
	code := strings.TrimSpace(query.Code)
	stateID := strings.TrimSpace(query.State)
	if code == "" || stateID == "" {
		writeError(w, r, http.StatusBadRequest, codeValidation, "OAuth code and state are required")
		return
	}
	// Check the browser binding first, so a stolen state link cannot burn
	// the real user's sign-in.
	browserState, ok := cookieValue(r, s.oauthStateCookie())
	if !ok || !secretsEqual(browserState, stateID) {
		writeError(w, r, http.StatusUnauthorized, codeReauthenticate, "Discord sign-in must finish in the browser that started it")
		return
	}
	s.setCookie(w, s.oauthStateCookie(), "", -1, true)

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	state, err := s.store.ConsumeOAuthState(ctx, stateID)
	if err != nil {
		slog.ErrorContext(ctx, "oauth state dependency unavailable")
		writeError(w, r, http.StatusServiceUnavailable, codeDependency, "Discord sign-in is temporarily unavailable")
		return
	}
	if state == nil {
		writeError(w, r, http.StatusUnauthorized, codeReauthenticate, "Discord sign-in expired; sign in again")
		return
	}
	token, user, err := s.oauth.signIn(ctx, s.cfg.Discord, code)
	if err != nil {
		// The cause stays out of the logs too: it can carry Discord's reply.
		slog.WarnContext(ctx, "Discord OAuth rejected")
		writeError(w, r, http.StatusUnauthorized, codeReauthenticate, "Discord authorization is invalid or revoked; sign in again")
		return
	}

	// Guilds that require 2FA read this before letting staff act, from the
	// dashboard and from Discord alike.
	if err := s.store.RecordDiscordUserMFA(ctx, user.ID, user.MFAEnabled, time.Now()); err != nil {
		slog.ErrorContext(ctx, "discord user mfa dependency unavailable")
		writeError(w, r, http.StatusServiceUnavailable, codeDependency, "authentication service unavailable")
		return
	}
	session := s.newSession(token, user)
	if err := s.store.SaveSession(ctx, session, s.cfg.Auth.SessionTTL); err != nil {
		slog.ErrorContext(ctx, "auth session dependency unavailable")
		writeError(w, r, http.StatusServiceUnavailable, codeDependency, "authentication service unavailable")
		return
	}
	s.setAuthCookies(w, session.ID, session.CSRFToken, int(s.cfg.Auth.SessionTTL.Seconds()))

	if state.ResponseMode == "json" {
		writeJSON(w, http.StatusOK, signInResponse{
			CSRFToken: session.CSRFToken,
			ExpiresAt: session.SessionExpiresAt,
			User:      sessionUser(session),
		})
		return
	}
	http.Redirect(w, r, sanitizeRedirectTarget(state.RedirectTo, s.cfg.Auth.PostLoginRedirect), http.StatusFound)
}

// newSession starts a session for a user who just signed in with token.
func (s *Server) newSession(token *discordToken, user *discordUser) *quack.AuthSession {
	now := time.Now().UTC()
	return &quack.AuthSession{
		ID:               quack.NewID(),
		DiscordUserID:    user.ID,
		Username:         user.Username,
		GlobalName:       user.GlobalName,
		Avatar:           user.Avatar,
		AccessToken:      token.AccessToken,
		RefreshToken:     token.RefreshToken,
		CSRFToken:        randomToken(),
		TokenType:        token.TokenType,
		Scope:            token.Scope,
		TokenExpiresAt:   now.Add(time.Duration(token.ExpiresIn) * time.Second),
		SessionExpiresAt: now.Add(s.cfg.Auth.SessionTTL),
		CreatedAt:        now,
		LastSeenAt:       now,
	}
}

func (s *Server) discordAuthURL(state string) string {
	v := url.Values{}
	v.Set("client_id", s.cfg.Discord.AppID)
	v.Set("redirect_uri", s.cfg.Discord.OAuthRedirectURI)
	v.Set("response_type", "code")
	v.Set("scope", s.cfg.Discord.OAuthScopes)
	v.Set("state", state)
	return discordAuthorizeURL + "?" + v.Encode()
}

// oauthStateCookie is the cookie binding an OAuth state to the browser that
// started sign-in. With secure cookies it takes the __Host- prefix, which
// stops a sibling subdomain from planting one.
func (s *Server) oauthStateCookie() string {
	if s.cfg.Auth.CookieSecure {
		return "__Host-quack_oauth_state"
	}
	return "quack_oauth_state"
}

// signIn trades an authorization code for an access token on behalf of app
// and fetches the identity behind it.
func (c oauthClient) signIn(ctx context.Context, app config.Discord, code string) (*discordToken, *discordUser, error) {
	token, err := c.exchange(ctx, app, code)
	if err != nil {
		return nil, nil, err
	}
	user, err := c.user(ctx, token.AccessToken)
	if err != nil {
		return nil, nil, err
	}
	return token, user, nil
}

// exchange trades an authorization code for an access token.
func (c oauthClient) exchange(ctx context.Context, app config.Discord, code string) (*discordToken, error) {
	form := url.Values{}
	form.Set("client_id", app.AppID)
	form.Set("client_secret", app.ClientSecret)
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", app.OAuthRedirectURI)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var token discordToken
	status, err := c.do(req, &token)
	if err != nil {
		return nil, fmt.Errorf("exchange code: %w", err)
	}
	if status >= 400 || token.AccessToken == "" || token.ExpiresIn <= 0 {
		return nil, errors.New("discord token exchange rejected")
	}
	return &token, nil
}

// user fetches the identity behind an access token.
func (c oauthClient) user(ctx context.Context, accessToken string) (*discordUser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.meURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create user request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	var user discordUser
	status, err := c.do(req, &user)
	if err != nil {
		return nil, fmt.Errorf("fetch user: %w", err)
	}
	if status >= 400 || user.ID == "" {
		return nil, errors.New("discord user fetch rejected")
	}
	return &user, nil
}

// do sends req and decodes a bounded JSON body into v, returning the status.
func (c oauthClient) do(req *http.Request, v any) (int, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxDiscordBodyBytes)).Decode(v); err != nil {
		return 0, fmt.Errorf("decode response: %w", err)
	}
	return resp.StatusCode, nil
}

// sanitizeRedirectTarget returns target if it is a local path or a URL on
// the fallback's origin, and fallback otherwise. A fallback that is itself
// unsafe becomes "/".
func sanitizeRedirectTarget(target, fallback string) string {
	target, fallback = strings.TrimSpace(target), strings.TrimSpace(fallback)
	fallbackURL, ok := safeRedirectURL(fallback)
	if !ok {
		fallback, fallbackURL = "/", &url.URL{Path: "/"}
	}
	targetURL, ok := safeRedirectURL(target)
	if !ok {
		return fallback
	}
	if targetURL.Host == "" {
		return target
	}
	if strings.EqualFold(targetURL.Scheme, fallbackURL.Scheme) && strings.EqualFold(targetURL.Host, fallbackURL.Host) {
		return target
	}
	return fallback
}

// safeRedirectURL rejects what browsers would turn into another host:
// network paths ("//evil"), backslashes, control characters, and userinfo.
func safeRedirectURL(raw string) (*url.URL, bool) {
	parsed, err := url.Parse(raw)
	if err != nil || raw == "" || parsed.User != nil || strings.ContainsAny(raw, "\\\r\n\t") ||
		strings.Contains(parsed.Path, "\\") || strings.HasPrefix(parsed.Path, "//") {
		return nil, false
	}
	if parsed.Scheme == "" {
		return parsed, strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "//") && parsed.Host == ""
	}
	return parsed, (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" && parsed.Opaque == ""
}
