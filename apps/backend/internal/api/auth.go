package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
)

const (
	csrfHeader = "X-CSRF-Token"
	// sessionStoreTimeout bounds each session lookup, refresh, or delete.
	sessionStoreTimeout = 5 * time.Second
)

// requireAuth loads the caller's session from a bearer token or the session
// cookie. An expired session or Discord grant is deleted and answered with
// reauthentication_required. A live session gets a sliding expiry, and a
// cookie session gets its CSRF cookie refreshed alongside.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sessionID := s.sessionID(r)
		if sessionID == "" {
			slog.WarnContext(r.Context(), "authentication required")
			writeError(w, r, http.StatusUnauthorized, codeAuthentication, "authentication required")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), sessionStoreTimeout)
		defer cancel()
		session, err := s.store.GetSession(ctx, sessionID)
		if err != nil {
			slog.ErrorContext(ctx, "auth session dependency unavailable")
			writeError(w, r, http.StatusServiceUnavailable, codeDependency, "authentication service unavailable")
			return
		}
		if session == nil || session.DiscordUserID == "" {
			slog.WarnContext(ctx, "invalid authentication session")
			s.clearAuthCookies(w)
			writeError(w, r, http.StatusUnauthorized, codeAuthentication, "authentication required")
			return
		}

		now := time.Now().UTC()
		var expired, message string
		switch {
		case passed(session.SessionExpiresAt, now):
			expired, message = "authentication session expired", "sign in again to continue"
		case passed(session.TokenExpiresAt, now):
			expired, message = "Discord authorization expired", "Discord authorization expired; sign in again"
		}
		if expired != "" {
			slog.WarnContext(ctx, expired, "actor_discord_user_id", session.DiscordUserID)
			_ = s.store.DeleteSession(ctx, sessionID)
			s.clearAuthCookies(w)
			writeError(w, r, http.StatusUnauthorized, codeReauthenticate, message)
			return
		}

		if session.CSRFToken == "" {
			session.CSRFToken = randomToken()
		}
		ttl := s.cfg.Auth.SessionTTL
		session.LastSeenAt = now
		session.SessionExpiresAt = now.Add(ttl)
		refreshed, err := s.store.RefreshSession(ctx, session, ttl)
		if err != nil {
			slog.ErrorContext(ctx, "auth session refresh dependency unavailable")
			writeError(w, r, http.StatusServiceUnavailable, codeDependency, "authentication service unavailable")
			return
		}
		if !refreshed {
			s.clearAuthCookies(w)
			writeError(w, r, http.StatusUnauthorized, codeReauthenticate, "sign in again to continue")
			return
		}
		if _, ok := cookieValue(r, s.cfg.Auth.SessionCookieName); ok {
			s.setCookie(w, s.cfg.Auth.CSRFCookieName, session.CSRFToken, int(ttl.Seconds()), false)
		}
		next.ServeHTTP(w, r.WithContext(withSession(r.Context(), session)))
	})
}

// csrf protects cookie-authenticated writes. A write that carries the session
// cookie must come from an allowed Origin and echo the CSRF cookie in the
// X-CSRF-Token header (double submit). Bearer-authenticated callers are not
// browsers and skip the check.
func (s *Server) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := cookieValue(r, s.cfg.Auth.SessionCookieName); !ok || !isWrite(r.Method) || hasBearerCredential(r) {
			next.ServeHTTP(w, r)
			return
		}
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin == "" || !slices.Contains(s.cfg.API.CORSOrigins, origin) {
			writeError(w, r, http.StatusForbidden, codeCSRF, "CSRF validation failed")
			return
		}
		cookie, _ := cookieValue(r, s.cfg.Auth.CSRFCookieName)
		header := strings.TrimSpace(r.Header.Get(csrfHeader))
		if cookie == "" || header == "" || !secretsEqual(cookie, header) {
			writeError(w, r, http.StatusForbidden, codeCSRF, "CSRF validation failed")
			return
		}
		next.ServeHTTP(w, r)
	})
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

// logout ends the current session.
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), sessionStoreTimeout)
	defer cancel()
	if err := s.store.DeleteSession(ctx, sessionFrom(r.Context()).ID); err != nil {
		slog.ErrorContext(ctx, "auth logout dependency unavailable")
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
		slog.ErrorContext(ctx, "auth compromise revocation dependency unavailable")
		writeError(w, r, http.StatusServiceUnavailable, codeDependency, "session revocation unavailable")
		return
	}
	s.clearAuthCookies(w)
	w.WriteHeader(http.StatusNoContent)
}

// sessionUser is the dashboard's view of the signed-in Discord user. It
// carries no credentials.
func sessionUser(session *quack.AuthSession) map[string]any {
	return map[string]any{
		"id":          session.DiscordUserID,
		"username":    session.Username,
		"global_name": session.GlobalName,
		"avatar":      session.Avatar,
		"avatar_url":  discordAvatarURL(session.DiscordUserID, session.Avatar),
	}
}

// discordAvatarURL returns the CDN URL for an avatar hash, or "" if the user
// has none. Hashes starting with "a_" are animated.
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

func withSession(ctx context.Context, session *quack.AuthSession) context.Context {
	return context.WithValue(ctx, sessionKey, session)
}

// sessionFrom returns the session loaded by requireAuth.
func sessionFrom(ctx context.Context) *quack.AuthSession {
	session, _ := ctx.Value(sessionKey).(*quack.AuthSession)
	return session
}

// passed reports whether a deadline is set and now is at or after it.
func passed(deadline, now time.Time) bool {
	return !deadline.IsZero() && !now.Before(deadline)
}

// sessionID reads the session credential: a bearer token if present,
// otherwise the session cookie.
func (s *Server) sessionID(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); auth != "" {
		scheme, token, ok := strings.Cut(auth, " ")
		if ok && strings.EqualFold(scheme, "Bearer") {
			return strings.TrimSpace(token)
		}
	}
	if cookie, ok := cookieValue(r, s.cfg.Auth.SessionCookieName); ok {
		return strings.TrimSpace(cookie)
	}
	return ""
}

func hasBearerCredential(r *http.Request) bool {
	parts := strings.Fields(r.Header.Get("Authorization"))
	return len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") && parts[1] != ""
}

// cookieValue returns the named cookie, query-unescaped as it was set.
func cookieValue(r *http.Request, name string) (string, bool) {
	cookie, err := r.Cookie(name)
	if err != nil {
		return "", false
	}
	value, _ := url.QueryUnescape(cookie.Value)
	return value, true
}

// setCookie sets a host-only, SameSite=Lax cookie on path /. A negative
// maxAge deletes it.
func (s *Server) setCookie(w http.ResponseWriter, name, value string, maxAge int, httpOnly bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    url.QueryEscape(value),
		MaxAge:   maxAge,
		Path:     "/",
		Secure:   s.cfg.Auth.CookieSecure,
		HttpOnly: httpOnly,
		SameSite: http.SameSiteLaxMode,
	})
}

// setAuthCookies sets the HttpOnly session cookie and the script-readable
// CSRF cookie that the dashboard echoes in X-CSRF-Token.
func (s *Server) setAuthCookies(w http.ResponseWriter, sessionID, csrfToken string, maxAge int) {
	s.setCookie(w, s.cfg.Auth.SessionCookieName, sessionID, maxAge, true)
	s.setCookie(w, s.cfg.Auth.CSRFCookieName, csrfToken, maxAge, false)
}

func (s *Server) clearAuthCookies(w http.ResponseWriter) {
	s.setAuthCookies(w, "", "", -1)
}

// randomToken returns 32 random bytes in hex. It serves as the CSRF
// double-submit challenge, which only proves same-origin JavaScript, and as
// an idempotency lease token.
func randomToken() string {
	var token [32]byte
	_, _ = rand.Read(token[:]) // crypto/rand.Read never fails; it crashes instead.
	return hex.EncodeToString(token[:])
}

// secretsEqual compares shared secrets in constant time.
func secretsEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
