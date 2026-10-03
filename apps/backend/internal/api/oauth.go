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

type discordToken struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
}

type discordUser struct {
	ID         string `json:"id"`
	Username   string `json:"username"`
	GlobalName string `json:"global_name"`
	Avatar     string `json:"avatar"`
}

// discordLogin starts sign-in. It stores a single-use state, binds it to this
// browser with a cookie, and either redirects to Discord or, with mode=json,
// returns the authorization URL.
func (s *Server) discordLogin(w http.ResponseWriter, r *http.Request) {
	if !s.oauthConfigured() {
		writeError(w, r, http.StatusServiceUnavailable, codeDependency, "Discord sign-in is unavailable")
		return
	}
	query := r.URL.Query()
	mode := "redirect"
	if strings.EqualFold(strings.TrimSpace(query.Get("mode")), "json") {
		mode = "json"
	}
	stateID := quack.NewID()
	state := &quack.OAuthState{
		RedirectTo:   sanitizeRedirectTarget(query.Get("redirect_to"), s.cfg.Auth.PostLoginRedirect),
		ResponseMode: mode,
		CreatedAt:    time.Now().UTC(),
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	stateTTL := s.cfg.Auth.StateTTL
	if err := s.store.SaveOAuthState(ctx, stateID, state, stateTTL); err != nil {
		slog.Error("oauth state dependency unavailable", "request_id", quack.RequestIDFromContext(r.Context()))
		writeError(w, r, http.StatusServiceUnavailable, codeDependency, "Discord sign-in is temporarily unavailable")
		return
	}

	s.setCookie(w, s.oauthStateCookie(), stateID, int(stateTTL.Seconds()), true)
	authURL := s.discordAuthURL(stateID)
	if mode == "json" {
		writeJSON(w, http.StatusOK, map[string]any{"auth_url": authURL, "state": stateID})
		return
	}
	http.Redirect(w, r, authURL, http.StatusFound)
}

// discordCallback finishes sign-in: it checks the state against the browser
// cookie before consuming it, exchanges the code, and creates the session.
// Discord's error text is never echoed back.
func (s *Server) discordCallback(w http.ResponseWriter, r *http.Request) {
	if !s.oauthConfigured() {
		writeError(w, r, http.StatusServiceUnavailable, codeDependency, "Discord sign-in is unavailable")
		return
	}
	query := r.URL.Query()
	if query.Get("error") != "" {
		writeError(w, r, http.StatusUnauthorized, codeReauthenticate, "Discord authorization was not granted; sign in again")
		return
	}
	code := strings.TrimSpace(query.Get("code"))
	stateID := strings.TrimSpace(query.Get("state"))
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
	requestID := quack.RequestIDFromContext(r.Context())
	state, err := s.store.ConsumeOAuthState(ctx, stateID)
	if err != nil {
		slog.Error("oauth state dependency unavailable", "request_id", requestID)
		writeError(w, r, http.StatusServiceUnavailable, codeDependency, "Discord sign-in is temporarily unavailable")
		return
	}
	if state == nil {
		writeError(w, r, http.StatusUnauthorized, codeReauthenticate, "Discord sign-in expired; sign in again")
		return
	}
	token, err := s.oauth.exchange(ctx, s.cfg.Discord.AppID, s.cfg.Discord.ClientSecret, s.cfg.Discord.OAuthRedirectURI, code)
	if err != nil {
		slog.Warn("Discord OAuth grant rejected", "request_id", requestID)
		writeError(w, r, http.StatusUnauthorized, codeReauthenticate, "Discord authorization is invalid or revoked; sign in again")
		return
	}
	user, err := s.oauth.user(ctx, token.AccessToken)
	if err != nil {
		slog.Warn("Discord OAuth identity request rejected", "request_id", requestID)
		writeError(w, r, http.StatusUnauthorized, codeReauthenticate, "Discord authorization is invalid or revoked; sign in again")
		return
	}
	csrfToken, err := newCSRFToken()
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, codeInternal, "could not create authentication session")
		return
	}

	now := time.Now().UTC()
	sessionTTL := s.cfg.Auth.SessionTTL
	session := &quack.AuthSession{
		ID:               quack.NewID(),
		DiscordUserID:    user.ID,
		Username:         user.Username,
		GlobalName:       user.GlobalName,
		Avatar:           user.Avatar,
		AccessToken:      token.AccessToken,
		RefreshToken:     token.RefreshToken,
		CSRFToken:        csrfToken,
		TokenType:        token.TokenType,
		Scope:            token.Scope,
		TokenExpiresAt:   now.Add(time.Duration(token.ExpiresIn) * time.Second),
		SessionExpiresAt: now.Add(sessionTTL),
		CreatedAt:        now,
		LastSeenAt:       now,
	}
	if err := s.store.SaveSession(ctx, session, sessionTTL); err != nil {
		slog.Error("auth session dependency unavailable", "request_id", requestID)
		writeError(w, r, http.StatusServiceUnavailable, codeDependency, "authentication service unavailable")
		return
	}
	s.setAuthCookies(w, session.ID, csrfToken, int(sessionTTL.Seconds()))

	if state.ResponseMode == "json" {
		writeJSON(w, http.StatusOK, map[string]any{
			"csrf_token": csrfToken,
			"user":       sessionUser(session),
			"expires_at": session.SessionExpiresAt,
		})
		return
	}
	http.Redirect(w, r, sanitizeRedirectTarget(state.RedirectTo, s.cfg.Auth.PostLoginRedirect), http.StatusFound)
}

// authMe returns the signed-in user and the CSRF token the dashboard must
// echo on writes.
func (s *Server) authMe(w http.ResponseWriter, r *http.Request) {
	session := sessionFrom(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"csrf_token": session.CSRFToken,
		"user":       sessionUser(session),
		"session": map[string]any{
			"expires_at": session.SessionExpiresAt,
			"last_seen":  session.LastSeenAt,
		},
	})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), sessionStoreTimeout)
	defer cancel()
	if err := s.store.DeleteSession(ctx, sessionFrom(r.Context()).ID); err != nil {
		slog.Error("auth logout dependency unavailable", "request_id", quack.RequestIDFromContext(r.Context()))
		writeError(w, r, http.StatusServiceUnavailable, codeDependency, "authentication service unavailable")
		return
	}
	s.clearAuthCookies(w)
	w.WriteHeader(http.StatusNoContent)
}

// logoutAll revokes every session of the user, for a compromised account.
func (s *Server) logoutAll(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), sessionStoreTimeout)
	defer cancel()
	if err := s.store.RevokeUserSessions(ctx, sessionFrom(r.Context()).DiscordUserID); err != nil {
		slog.Error("auth compromise revocation dependency unavailable", "request_id", quack.RequestIDFromContext(r.Context()))
		writeError(w, r, http.StatusServiceUnavailable, codeDependency, "session revocation unavailable")
		return
	}
	s.clearAuthCookies(w)
	w.WriteHeader(http.StatusNoContent)
}

func sessionUser(session *quack.AuthSession) map[string]any {
	return map[string]any{
		"id":          session.DiscordUserID,
		"username":    session.Username,
		"global_name": session.GlobalName,
		"avatar":      session.Avatar,
		"avatar_url":  discordAvatarURL(session.DiscordUserID, session.Avatar),
	}
}

func discordAvatarURL(userID, avatarHash string) string {
	if userID == "" || avatarHash == "" {
		return ""
	}
	ext := "png"
	if strings.HasPrefix(avatarHash, "a_") {
		ext = "gif"
	}
	return fmt.Sprintf("https://cdn.discordapp.com/avatars/%s/%s.%s", userID, avatarHash, ext)
}

func (s *Server) oauthConfigured() bool {
	d := s.cfg.Discord
	return strings.TrimSpace(d.AppID) != "" && strings.TrimSpace(d.ClientSecret) != "" &&
		strings.TrimSpace(d.OAuthRedirectURI) != ""
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

// exchange trades an authorization code for an access token.
func (c oauthClient) exchange(ctx context.Context, clientID, clientSecret, redirectURI, code string) (*discordToken, error) {
	form := url.Values{}
	form.Set("client_id", clientID)
	form.Set("client_secret", clientSecret)
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
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
