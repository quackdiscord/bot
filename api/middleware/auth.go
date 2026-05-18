package middleware

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/lib"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
	"github.com/rs/zerolog/log"
)

const (
	ContextSessionKey = "auth_session"
	ContextUserIDKey  = "auth_user_id"
)

// RequireAuth is a middleware function that requires a valid authentication session
// Accepts bearer token or auth cookie
func RequireAuth(s *storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		sessionID := extractSessionID(c)
		if sessionID == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing auth session"})
			return
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()

		session, err := s.GetSession(ctx, sessionID)
		if err != nil {
			log.Error().Err(err).Msg("failed to load auth session")
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "failed to load auth session"})
			return
		}

		if session == nil || session.DiscordUserID == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid auth session"})
			return
		}

		now := time.Now().UTC()
		if !session.SessionExpiresAt.IsZero() && now.After(session.SessionExpiresAt) {
			_ = s.DeleteSession(ctx, sessionID)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "auth session expired"})
			return
		}

		session.LastSeenAt = now
		ttl := time.Duration(lib.Config.Auth.SessionTTLHours) * time.Hour
		session.SessionExpiresAt = now.Add(ttl)
		if err := s.SaveSession(ctx, session, ttl); err != nil {
			log.Error().Err(err).Msg("failed to refresh auth session")
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "failed to refresh auth session"})
			return
		}

		c.Set(ContextSessionKey, session)
		c.Set(ContextUserIDKey, session.DiscordUserID)
		c.Next()
	}
}

// GetAuthSession retrieves the auth session from Gin context
func GetAuthSession(c *gin.Context) *structs.AuthSession {
	v, ok := c.Get(ContextSessionKey)
	if !ok {
		return nil
	}

	session, ok := v.(*structs.AuthSession)
	if !ok {
		return nil
	}

	return session
}

// extractSessionID extracts the session ID from the Auth header or cookie
func extractSessionID(c *gin.Context) string {
	auth := c.GetHeader("Authorization")
	if auth != "" {
		parts := strings.SplitN(auth, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			return strings.TrimSpace(parts[1])
		}
	}

	cookie, err := c.Cookie(lib.Config.Auth.SessionCookieName)
	if err == nil {
		return strings.TrimSpace(cookie)
	}

	return ""
}
