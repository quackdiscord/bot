package routes

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/api/middleware"
	"github.com/quackdiscord/bot/app"
	"github.com/rs/zerolog/log"
)

func listUserGuilds(c *gin.Context, services *app.Services) {
	log.Info().Msg("listing user guilds")
	session := middleware.GetAuthSession(c)
	if session == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing auth session"})
		return
	}
	log.Info().Msg("auth session found")

	guilds, err := services.Guilds.ListUserManageableGuilds(c.Request.Context(), session)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to list discord guilds"})
		return
	}
	log.Info().Msg("discord guilds listed")

	c.JSON(http.StatusOK, gin.H{"guilds": guilds})
}

func guildMe(c *gin.Context) {
	guildContext := middleware.GetGuildContext(c)
	if guildContext == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "missing guild context"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"guild": gin.H{
			"id":                    guildContext.Guild.ID,
			"discord_guild_id":      guildContext.Guild.DiscordGuildID,
			"name":                  guildContext.Guild.Name,
			"icon_url":              guildContext.Guild.IconURL,
			"owner_discord_user_id": guildContext.Guild.OwnerDiscordUserID,
			"rollout_state":         guildContext.Guild.RolloutState,
		},
		"staff": gin.H{
			"id":                    guildContext.Staff.ID,
			"discord_user_id":       guildContext.Staff.DiscordUserID,
			"display_name":          guildContext.Staff.LastKnownDisplayName,
			"permission_bits":       app.PermissionBitsString(guildContext.PermissionBits),
			"is_owner":              guildContext.IsOwner,
			"is_administrator":      guildContext.IsAdministrator,
			"disabled":              guildContext.Staff.DisabledAt != nil,
			"last_active_at":        guildContext.Staff.LastActiveAt,
			"last_seen_permissions": app.PermissionBitsString(guildContext.Staff.LastSeenPermissionBits),
		},
		"permissions": app.PermissionMapStrings(guildContext.Permissions),
	})
}
