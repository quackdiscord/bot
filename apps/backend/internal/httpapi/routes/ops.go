package routes

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/quackdiscord/bot/internal/httpapi/apierror"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/httpapi/middleware"
	"github.com/quackdiscord/bot/internal/quack"
)

const opsKeyHeader = "X-Quack-Ops-Key"

// globalOpsStatus encapsulates the global ops status rule so callers share one consistent package implementation.
// @Summary Get global operations status
// @Tags Operations
// @Produce json
// @Security OpsKey
// @Success 200 {object} quack.OpsStatusResponse
// @Failure 403 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /ops/status [get]
func globalOpsStatus(c *gin.Context, services *Deps) {
	if !validOpsKey(c, services) {
		if strings.TrimSpace(services.Config.API.OpsToken) == "" {
			apierror.Write(c, http.StatusNotFound, apierror.CodeNotFound, "ops status is disabled")
			return
		}
		apierror.Write(c, http.StatusForbidden, apierror.CodeAuthorization, "invalid ops key")
		return
	}

	status, err := services.Ops.GlobalStatus(c.Request.Context())
	if err != nil {
		apierror.Write(c, http.StatusInternalServerError, apierror.CodeInternal, "ops status failed")
		return
	}
	c.JSON(http.StatusOK, status)
}

// guildOpsStatus encapsulates the guild ops status rule so callers share one consistent package implementation.
// @Summary Get guild operations status
// @Tags Operations
// @Produce json
// @Param discordGuildID path string true "Discord guild ID"
// @Security CookieAuth
// @Success 200 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /guilds/{discordGuildID}/ops/status [get]
func guildOpsStatus(c *gin.Context, services *Deps) {
	guildID, ok := guildOpsAuthorized(c, services)
	if !ok {
		return
	}

	status, err := services.Ops.GuildStatus(c.Request.Context(), guildID)
	if err != nil {
		apierror.Write(c, http.StatusInternalServerError, apierror.CodeInternal, "ops status failed")
		return
	}
	health, healthErr := services.Guilds.OperationalGuildHealth(c.Request.Context(), c.Param("discordGuildID"))
	if healthErr != nil {
		health = quack.GuildOperationalHealth{Degraded: true, Reasons: []string{"guild_health_unavailable"}}
	}
	c.JSON(http.StatusOK, gin.H{"operations": status, "guild_health": health})
}

// guildOpsAuthorized encapsulates the guild ops authorized rule so callers share one consistent package implementation.
func guildOpsAuthorized(c *gin.Context, services *Deps) (string, bool) {
	discordGuildID := strings.TrimSpace(c.Param("discordGuildID"))
	if discordGuildID == "" {
		apierror.Write(c, http.StatusBadRequest, apierror.CodeValidation, "missing discord guild id")
		return "", false
	}

	if validOpsKey(c, services) {
		guild, err := services.Store.GetGuildByDiscordID(c.Request.Context(), discordGuildID)
		if err != nil {
			apierror.Write(c, http.StatusInternalServerError, apierror.CodeInternal, "guild lookup failed")
			return "", false
		}
		if guild == nil {
			apierror.Write(c, http.StatusNotFound, apierror.CodeNotFound, "guild not found")
			return "", false
		}
		return guild.ID, true
	}

	sessionID := middleware.ExtractSessionID(c, services.Config.Auth.SessionCookieName)
	if sessionID == "" {
		apierror.Write(c, http.StatusUnauthorized, apierror.CodeAuthentication, "missing auth session")
		return "", false
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()

	session, err := services.Store.GetSession(ctx, sessionID)
	if err != nil {
		apierror.Write(c, http.StatusInternalServerError, apierror.CodeInternal, "failed to load auth session")
		return "", false
	}
	if session == nil || session.DiscordUserID == "" {
		apierror.Write(c, http.StatusUnauthorized, apierror.CodeAuthentication, "invalid auth session")
		return "", false
	}
	if !session.SessionExpiresAt.IsZero() && time.Now().UTC().After(session.SessionExpiresAt) {
		apierror.Write(c, http.StatusUnauthorized, apierror.CodeAuthentication, "auth session expired")
		return "", false
	}

	guildContext, err := services.Guilds.ResolveStaffContext(ctx, session, discordGuildID)
	if err != nil {
		status := http.StatusForbidden
		if errors.Is(err, quack.ErrBotNotInGuild) {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return "", false
	}
	if !guildContext.IsAdmin {
		apierror.Write(c, http.StatusForbidden, apierror.CodeAuthorization, "guild administrator access required")
		return "", false
	}
	return guildContext.Guild.ID, true
}

// validOpsKey checks valid ops key before state is read or changed.
func validOpsKey(c *gin.Context, services *Deps) bool {
	if services == nil {
		return false
	}
	configured := strings.TrimSpace(services.Config.API.OpsToken)
	if configured == "" {
		return false
	}
	return c.GetHeader(opsKeyHeader) == configured
}
