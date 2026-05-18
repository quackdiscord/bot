package middleware

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/app"
	"github.com/quackdiscord/bot/structs"
)

const ContextGuildKey = "guild_context"

// RequireGuildContext is a middleware function that requires a valid guild context
func RequireGuildContext(services *app.Services, requiredAction structs.PermissionAction) gin.HandlerFunc {
	return func(c *gin.Context) {
		session := GetAuthSession(c)
		if session == nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing auth session"})
			return
		}

		guildContext, err := services.Guilds.ResolveStaffContext(c.Request.Context(), session, c.Param("discordGuildID"))
		if err != nil {
			status := http.StatusForbidden
			if errors.Is(err, app.ErrBotNotInGuild) {
				status = http.StatusNotFound
			}

			c.AbortWithStatusJSON(status, gin.H{"error": err.Error()})
			return
		}

		if requiredAction != "" && !guildContext.Can(requiredAction) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "missing required guild permission"})
			return
		}

		c.Set(ContextGuildKey, guildContext)
		c.Next()
	}
}

// GetGuildContext retrieves the guild context from Gin context
func GetGuildContext(c *gin.Context) *app.GuildStaffContext {
	v, ok := c.Get(ContextGuildKey)
	if !ok {
		return nil
	}

	guildContext, ok := v.(*app.GuildStaffContext)
	if !ok {
		return nil
	}

	return guildContext
}
