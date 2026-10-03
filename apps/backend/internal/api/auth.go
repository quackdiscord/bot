package api

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
)

// sessionStoreTimeout bounds each session lookup, refresh, or delete.
const sessionStoreTimeout = 5 * time.Second

func withSession(ctx context.Context, session *quack.AuthSession) context.Context {
	return context.WithValue(ctx, sessionKey, session)
}

// sessionFrom returns the session loaded by requireAuth.
func sessionFrom(ctx context.Context) *quack.AuthSession {
	session, _ := ctx.Value(sessionKey).(*quack.AuthSession)
	return session
}

// requireAuth loads the caller's session from a bearer token or the session
// cookie. An expired session or Discord grant is deleted and answered with
// reauthentication_required. A live session gets a sliding expiry, and a
// cookie session gets its CSRF cookie refreshed alongside.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID, correlationID := quack.TraceIDsFromContext(r.Context())
		sessionID := s.sessionID(r)
		if sessionID == "" {
			slog.Warn("authentication required", "request_id", requestID, "correlation_id", correlationID)
			writeError(w, r, http.StatusUnauthorized, codeAuthentication, "authentication required")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), sessionStoreTimeout)
		defer cancel()
		session, err := s.store.GetSession(ctx, sessionID)
		if err != nil {
			slog.Error("auth session dependency unavailable", "request_id", requestID, "correlation_id", correlationID)
			writeError(w, r, http.StatusServiceUnavailable, codeDependency, "authentication service unavailable")
			return
		}
		if session == nil || session.DiscordUserID == "" {
			slog.Warn("invalid authentication session", "request_id", requestID, "correlation_id", correlationID)
			s.clearAuthCookies(w)
			writeError(w, r, http.StatusUnauthorized, codeAuthentication, "authentication required")
			return
		}

		now := time.Now().UTC()
		if !session.SessionExpiresAt.IsZero() && !now.Before(session.SessionExpiresAt) {
			slog.Warn("authentication session expired", "request_id", requestID, "correlation_id", correlationID,
				"actor_discord_user_id", session.DiscordUserID)
			_ = s.store.DeleteSession(ctx, sessionID)
			s.clearAuthCookies(w)
			writeError(w, r, http.StatusUnauthorized, codeReauthenticate, "sign in again to continue")
			return
		}
		if !session.TokenExpiresAt.IsZero() && !now.Before(session.TokenExpiresAt) {
			slog.Warn("Discord authorization expired", "request_id", requestID, "correlation_id", correlationID,
				"actor_discord_user_id", session.DiscordUserID)
			_ = s.store.DeleteSession(ctx, sessionID)
			s.clearAuthCookies(w)
			writeError(w, r, http.StatusUnauthorized, codeReauthenticate, "Discord authorization expired; sign in again")
			return
		}
		if session.CSRFToken == "" {
			token, err := newCSRFToken()
			if err != nil {
				writeError(w, r, http.StatusInternalServerError, codeInternal, "could not refresh authentication session")
				return
			}
			session.CSRFToken = token
		}

		ttl := s.cfg.Auth.SessionTTL
		session.LastSeenAt = now
		session.SessionExpiresAt = now.Add(ttl)
		refreshed, err := s.store.RefreshSession(ctx, session, ttl)
		if err != nil {
			slog.Error("auth session refresh dependency unavailable", "request_id", requestID, "correlation_id", correlationID)
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

// oauthStateCookie is the cookie binding an OAuth state to the browser that
// started sign-in. With secure cookies it takes the __Host- prefix, which
// stops a sibling subdomain from planting one.
func (s *Server) oauthStateCookie() string {
	if s.cfg.Auth.CookieSecure {
		return "__Host-quack_oauth_state"
	}
	return "quack_oauth_state"
}
