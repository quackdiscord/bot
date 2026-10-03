package middleware

import (
	"errors"
	"net/http"

	"log/slog"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/httpapi/apierror"
	"github.com/quackdiscord/bot/internal/quack"
)

const ContextGuildKey = "guild_context"

// ContextAuthorizedWriteKey carries the write policy staged by the outer HTTP platform.
const ContextAuthorizedWriteKey = "authorized_write_policy"

// ContinueAuthorizedWrite runs staged replay only after the route has checked its specific capability.
func ContinueAuthorizedWrite(c *gin.Context) {
	if value, ok := c.Get(ContextAuthorizedWriteKey); ok {
		c.Set(ContextAuthorizedWriteKey, nil)
		if protect, ok := value.(gin.HandlerFunc); ok {
			protect(c)
			return
		}
	}
	c.Next()
}

// RequireGuildContext is a middleware function that requires a valid guild context
func RequireGuildContext(services *quack.Services, requiredAction quack.PermissionAction) gin.HandlerFunc {
	return func(c *gin.Context) {
		session := GetAuthSession(c)
		if session == nil {
			apierror.Write(c, http.StatusUnauthorized, apierror.CodeAuthentication, "authentication required")
			return
		}

		guildContext, err := services.Guilds.ResolveStaffContext(c.Request.Context(), session, c.Param("discordGuildID"))
		if err != nil {
			slog.Warn("live guild authorization denied", "request_id", quack.RequestIDFromContext(c.Request.Context()), "correlation_id", quack.CorrelationIDFromContext(c.Request.Context()), "actor_discord_user_id", session.DiscordUserID, "discord_guild_id", c.Param("discordGuildID"))
			status := http.StatusForbidden
			if errors.Is(err, quack.ErrBotNotInGuild) {
				status = http.StatusNotFound
			}
			if status == http.StatusNotFound {
				apierror.Write(c, status, apierror.CodeNotFound, "guild not found")
			} else {
				apierror.Write(c, status, apierror.CodeAuthorization, "live guild authorization unavailable")
			}
			return
		}

		if err := services.Guilds.Authorize(c.Request.Context(), guildContext, requiredAction, quack.AuditSourceAPI); err != nil {
			slog.Warn("guild permission denied", "request_id", quack.RequestIDFromContext(c.Request.Context()), "correlation_id", quack.CorrelationIDFromContext(c.Request.Context()), "actor_discord_user_id", session.DiscordUserID, "discord_guild_id", c.Param("discordGuildID"), "permission_action", string(requiredAction))
			apierror.Write(c, http.StatusForbidden, apierror.CodeAuthorization, "access denied")
			return
		}

		c.Set(ContextGuildKey, guildContext)
		if requiredAction != "" {
			ContinueAuthorizedWrite(c)
			return
		}
		c.Next()
	}
}

// GetGuildContext retrieves the guild context from Gin context
func GetGuildContext(c *gin.Context) *quack.GuildStaffContext {
	v, ok := c.Get(ContextGuildKey)
	if !ok {
		return nil
	}

	guildContext, ok := v.(*quack.GuildStaffContext)
	if !ok {
		return nil
	}

	return guildContext
}
