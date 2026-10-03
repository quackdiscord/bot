package routes

import (
	"net/http"

	"github.com/quackdiscord/bot/internal/httpapi/apierror"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/httpapi/middleware"
	"github.com/quackdiscord/bot/internal/quack"
)

// listUserGuilds returns user guilds subject to authorization, ordering, and filtering constraints.
// @Summary List manageable Discord guilds
// @Tags Guilds
// @Produce json
// @Security CookieAuth
// @Success 200 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 502 {object} map[string]interface{}
// @Router /guilds [get]
func listUserGuilds(c *gin.Context, services *Deps) {
	session := middleware.GetAuthSession(c)
	if session == nil {
		apierror.Write(c, http.StatusUnauthorized, apierror.CodeAuthentication, "missing auth session")
		return
	}

	guilds, err := services.Guilds.ListUserManageableGuilds(c.Request.Context(), session)
	if err != nil {
		apierror.Write(c, http.StatusBadGateway, apierror.CodeDependency, "failed to list discord guilds")
		return
	}

	c.JSON(http.StatusOK, gin.H{"guilds": guilds})
}

// guildMe encapsulates the guild me rule so callers share one consistent package implementation.
// @Summary Get current guild staff context
// @Tags Guilds
// @Produce json
// @Param discordGuildID path string true "Discord guild ID"
// @Security CookieAuth
// @Success 200 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Router /guilds/{discordGuildID}/me [get]
func guildMe(c *gin.Context) {
	guildContext := middleware.GetGuildContext(c)
	if guildContext == nil {
		apierror.Write(c, http.StatusInternalServerError, apierror.CodeInternal, "missing guild context")
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"guild": gin.H{
			"id":                    guildContext.Guild.ID,
			"discord_guild_id":      guildContext.Guild.DiscordGuildID,
			"name":                  guildContext.Guild.Name,
			"icon_url":              guildContext.Guild.IconURL,
			"owner_discord_user_id": guildContext.Guild.OwnerDiscordUserID,
		},
		"staff": gin.H{
			"id":                    guildContext.Staff.ID,
			"discord_user_id":       guildContext.Staff.DiscordUserID,
			"display_name":          guildContext.Staff.LastKnownDisplayName,
			"permission_bits":       quack.PermissionBitsString(guildContext.PermissionBits),
			"is_admin":              guildContext.IsAdmin,
			"is_moderator":          guildContext.IsModerator,
			"last_active_at":        guildContext.Staff.LastActiveAt,
			"last_seen_permissions": quack.PermissionBitsString(guildContext.Staff.LastSeenPermissionBits),
		},
		"permissions": quack.PermissionMapStrings(guildContext.Permissions),
	})
}
