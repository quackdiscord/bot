package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"log/slog"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/config"
	"github.com/quackdiscord/bot/internal/httpapi/apierror"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

const (
	ContextSessionKey = "auth_session"
	ContextUserIDKey  = "auth_user_id"
)

// RequireAuth is a middleware function that requires a valid authentication session
// Accepts bearer token or auth cookie
func RequireAuth(s quack.Repository, auth config.Auth) gin.HandlerFunc {
	return func(c *gin.Context) {
		sessionID := ExtractSessionID(c, auth.SessionCookieName)
		if sessionID == "" {
			slog.Warn("authentication required", "request_id", quack.RequestIDFromContext(c.Request.Context()), "correlation_id", quack.CorrelationIDFromContext(c.Request.Context()))
			apierror.Write(c, http.StatusUnauthorized, apierror.CodeAuthentication, "authentication required")
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()

		session, err := s.GetSession(ctx, sessionID)
		if err != nil {
			slog.Error("auth session dependency unavailable", "request_id", quack.RequestIDFromContext(c.Request.Context()), "correlation_id", quack.CorrelationIDFromContext(c.Request.Context()))
			apierror.Write(c, http.StatusServiceUnavailable, apierror.CodeDependency, "authentication service unavailable")
			return
		}

		if session == nil || session.DiscordUserID == "" {
			slog.Warn("invalid authentication session", "request_id", quack.RequestIDFromContext(c.Request.Context()), "correlation_id", quack.CorrelationIDFromContext(c.Request.Context()))
			expireAuthCookies(c, auth)
			apierror.Write(c, http.StatusUnauthorized, apierror.CodeAuthentication, "authentication required")
			return
		}

		now := time.Now().UTC()
		if !session.SessionExpiresAt.IsZero() && !now.Before(session.SessionExpiresAt) {
			slog.Warn("authentication session expired", "request_id", quack.RequestIDFromContext(c.Request.Context()), "correlation_id", quack.CorrelationIDFromContext(c.Request.Context()), "actor_discord_user_id", session.DiscordUserID)
			_ = s.DeleteSession(ctx, sessionID)
			expireAuthCookies(c, auth)
			apierror.Write(c, http.StatusUnauthorized, apierror.CodeReauthenticate, "sign in again to continue")
			return
		}
		if !session.TokenExpiresAt.IsZero() && !now.Before(session.TokenExpiresAt) {
			slog.Warn("Discord authorization expired", "request_id", quack.RequestIDFromContext(c.Request.Context()), "correlation_id", quack.CorrelationIDFromContext(c.Request.Context()), "actor_discord_user_id", session.DiscordUserID)
			_ = s.DeleteSession(ctx, sessionID)
			expireAuthCookies(c, auth)
			apierror.Write(c, http.StatusUnauthorized, apierror.CodeReauthenticate, "Discord authorization expired; sign in again")
			return
		}
		if session.CSRFToken == "" {
			csrfToken, err := NewCSRFToken()
			if err != nil {
				apierror.Write(c, http.StatusInternalServerError, apierror.CodeInternal, "could not refresh authentication session")
				return
			}
			session.CSRFToken = csrfToken
		}

		session.LastSeenAt = now
		ttl := auth.SessionTTL
		session.SessionExpiresAt = now.Add(ttl)
		refreshed, err := s.RefreshSession(ctx, session, ttl)
		if err != nil {
			slog.Error("auth session refresh dependency unavailable", "request_id", quack.RequestIDFromContext(c.Request.Context()), "correlation_id", quack.CorrelationIDFromContext(c.Request.Context()))
			apierror.Write(c, http.StatusServiceUnavailable, apierror.CodeDependency, "authentication service unavailable")
			return
		}
		if !refreshed {
			expireAuthCookies(c, auth)
			apierror.Write(c, http.StatusUnauthorized, apierror.CodeReauthenticate, "sign in again to continue")
			return
		}
		if _, err := c.Cookie(auth.SessionCookieName); err == nil {
			setCSRFCookie(c, auth, session.CSRFToken, int(ttl.Seconds()))
		}

		c.Set(ContextSessionKey, session)
		c.Set(ContextUserIDKey, session.DiscordUserID)
		c.Next()
	}
}

// NewCSRFToken creates the non-secret random challenge used by the double-submit browser contract.
func NewCSRFToken() (string, error) {
	var body [32]byte
	if _, err := rand.Read(body[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(body[:]), nil
}

// setCSRFCookie repairs or refreshes the browser's host-only double-submit cookie.
func setCSRFCookie(c *gin.Context, auth config.Auth, token string, maxAge int) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(auth.CSRFCookieName, token, maxAge, "/", "", auth.CookieSecure, false)
}

// expireAuthCookies invalidates both browser credentials without exposing their values.
func expireAuthCookies(c *gin.Context, auth config.Auth) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(auth.SessionCookieName, "", -1, "/", "", auth.CookieSecure, true)
	c.SetCookie(auth.CSRFCookieName, "", -1, "/", "", auth.CookieSecure, false)
}

// GetAuthSession retrieves the auth session from Gin context
func GetAuthSession(c *gin.Context) *model.AuthSession {
	v, ok := c.Get(ContextSessionKey)
	if !ok {
		return nil
	}

	session, ok := v.(*model.AuthSession)
	if !ok {
		return nil
	}

	return session
}

// ExtractSessionID extracts a bearer token or session cookie, allowing dashboard and API clients to share authentication middleware.
func ExtractSessionID(c *gin.Context, cookieName string) string {
	auth := c.GetHeader("Authorization")
	if auth != "" {
		parts := strings.SplitN(auth, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			return strings.TrimSpace(parts[1])
		}
	}

	cookie, err := c.Cookie(cookieName)
	if err == nil {
		return strings.TrimSpace(cookie)
	}

	return ""
}
