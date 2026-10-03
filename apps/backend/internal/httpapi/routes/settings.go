package routes

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/quackdiscord/bot/internal/httpapi/apierror"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/httpapi/middleware"
	"github.com/quackdiscord/bot/internal/quack"
)

// getGuildSettings returns the guild-owned setup state to current Manage Guild authorities.
// @Summary Get guild settings
// @Tags Guild settings
// @Produce json
// @Param discordGuildID path string true "Discord guild ID"
// @Security CookieAuth
// @Success 200 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /guilds/{discordGuildID}/settings [get]
func getGuildSettings(c *gin.Context, services *Deps) {
	settings, err := services.Settings.Get(c.Request.Context(), middleware.GetGuildContext(c))
	if err != nil {
		writeGuildSettingsError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"settings": settings})
}

// updateGuildSettings applies a partial authorized guild settings write.
// @Summary Update guild settings
// @Tags Guild settings
// @Accept json
// @Produce json
// @Param discordGuildID path string true "Discord guild ID"
// @Param Idempotency-Key header string true "Retry-safe request key"
// @Param settings body quack.GuildSettingsInput true "Partial guild settings"
// @Security CookieAuth
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /guilds/{discordGuildID}/settings [patch]
func updateGuildSettings(c *gin.Context, services *Deps) {
	var input quack.GuildSettingsInput
	if err := bindGuildSettingsInput(c, &input); err != nil {
		writeGuildSettingsError(c, services.Settings.RejectUpdatePayload(c.Request.Context(), middleware.GetGuildContext(c), err))
		return
	}
	settings, err := services.Settings.Update(c.Request.Context(), middleware.GetGuildContext(c), input)
	if err != nil {
		writeGuildSettingsError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"settings": settings})
}

// acknowledgeStarterPolicyNotice explicitly marks the one-time dashboard setup notice complete.
// @Summary Acknowledge the starter policy notice
// @Tags Guild settings
// @Produce json
// @Param discordGuildID path string true "Discord guild ID"
// @Param Idempotency-Key header string true "Retry-safe request key"
// @Security CookieAuth
// @Success 200 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /guilds/{discordGuildID}/settings/starter-policy-notice/acknowledge [post]
func acknowledgeStarterPolicyNotice(c *gin.Context, services *Deps) {
	settings, err := services.Settings.AcknowledgeStarterPolicyNotice(c.Request.Context(), middleware.GetGuildContext(c))
	if err != nil {
		writeGuildSettingsError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"settings": settings})
}

// bindGuildSettingsInput rejects unknown fields and multiple JSON documents.
func bindGuildSettingsInput(c *gin.Context, input *quack.GuildSettingsInput) error {
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(input); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

// writeGuildSettingsError maps service failures into stable dashboard HTTP responses.
func writeGuildSettingsError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, quack.ErrGuildSettingsValidation):
		apierror.Write(c, http.StatusBadRequest, apierror.CodeValidation, err.Error())
	case errors.Is(err, quack.ErrGuildSettingsPermissionDenied):
		apierror.Write(c, http.StatusForbidden, apierror.CodeAuthorization, err.Error())
	case errors.Is(err, quack.ErrGuildSettingsNotFound):
		apierror.Write(c, http.StatusNotFound, apierror.CodeNotFound, err.Error())
	default:
		apierror.Write(c, http.StatusInternalServerError, apierror.CodeInternal, "guild settings operation failed")
	}
}
