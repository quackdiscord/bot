package routes

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/quackdiscord/bot/internal/httpapi/apierror"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/httpapi/middleware"
	"github.com/quackdiscord/bot/internal/quack"
)

// templateChangeHandler receives successful policy changes that may invalidate
// an optional automation reference.
type templateChangeHandler interface {
	HandleTemplateChange(context.Context, string, string)
}

// listTemplates returns templates subject to authorization, ordering, and filtering constraints.
// @Summary List case templates
// @Tags Templates
// @Produce json
// @Param discordGuildID path string true "Discord guild ID"
// @Security CookieAuth
// @Success 200 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Failure 500 {object} map[string]interface{}
// @Router /guilds/{discordGuildID}/templates [get]
func listTemplates(c *gin.Context, services *Deps) {
	guildContext := middleware.GetGuildContext(c)
	templates, err := services.Templates.List(c.Request.Context(), guildContext)
	if err != nil {
		apierror.Write(c, http.StatusInternalServerError, apierror.CodeInternal, "failed to list templates")
		return
	}

	c.JSON(http.StatusOK, gin.H{"templates": templates})
}

// createTemplate creates template while preserving validation, authorization, and persistence invariants.
// @Summary Create a case template
// @Tags Templates
// @Accept json
// @Produce json
// @Param discordGuildID path string true "Discord guild ID"
// @Param Idempotency-Key header string true "Retry-safe request key"
// @Param template body quack.TemplateInput true "Template definition"
// @Security CookieAuth
// @Success 201 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Router /guilds/{discordGuildID}/templates [post]
func createTemplate(c *gin.Context, services *Deps) {
	var input quack.TemplateInput
	if err := bindTemplateInput(c, &input); err != nil {
		apierror.Write(c, http.StatusBadRequest, apierror.CodeValidation, "invalid template payload")
		return
	}

	template, err := services.Templates.Create(c.Request.Context(), middleware.GetGuildContext(c), input)
	if err != nil {
		writeTemplateError(c, err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{"template": template})
}

// getTemplate retrieves template without exposing the underlying adapter implementation.
// @Summary Get a case template
// @Tags Templates
// @Produce json
// @Param discordGuildID path string true "Discord guild ID"
// @Param templateID path string true "Template ID"
// @Security CookieAuth
// @Success 200 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /guilds/{discordGuildID}/templates/{templateID} [get]
func getTemplate(c *gin.Context, services *Deps) {
	template, err := services.Templates.Get(c.Request.Context(), middleware.GetGuildContext(c), c.Param("templateID"))
	if err != nil {
		writeTemplateError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"template": template})
}

// updateTemplate updates template while retaining validation, compatibility, and audit requirements.
// @Summary Update a case template
// @Tags Templates
// @Accept json
// @Produce json
// @Param discordGuildID path string true "Discord guild ID"
// @Param templateID path string true "Template ID"
// @Param Idempotency-Key header string true "Retry-safe request key"
// @Param template body quack.TemplateInput true "Template definition"
// @Security CookieAuth
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Failure 409 {object} map[string]interface{}
// @Router /guilds/{discordGuildID}/templates/{templateID} [patch]
func updateTemplate(c *gin.Context, services *Deps, changes templateChangeHandler) {
	var input quack.TemplateInput
	if err := bindTemplateInput(c, &input); err != nil {
		apierror.Write(c, http.StatusBadRequest, apierror.CodeValidation, "invalid template payload")
		return
	}

	template, err := services.Templates.Update(c.Request.Context(), middleware.GetGuildContext(c), c.Param("templateID"), input)
	if err != nil {
		writeTemplateError(c, err)
		return
	}
	if changes != nil {
		guildContext := middleware.GetGuildContext(c)
		if guildContext != nil && guildContext.Guild != nil {
			changes.HandleTemplateChange(c.Request.Context(), guildContext.Guild.ID, template.ID)
		}
	}

	c.JSON(http.StatusOK, gin.H{"template": template})
}

// bindTemplateInput rejects retired or unknown product fields instead of silently ignoring them.
func bindTemplateInput(c *gin.Context, input *quack.TemplateInput) error {
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

// archiveTemplate encapsulates the archive template rule so callers share one consistent package implementation.
// @Summary Archive a case template
// @Tags Templates
// @Produce json
// @Param discordGuildID path string true "Discord guild ID"
// @Param templateID path string true "Template ID"
// @Param Idempotency-Key header string true "Retry-safe request key"
// @Security CookieAuth
// @Success 200 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /guilds/{discordGuildID}/templates/{templateID} [delete]
func archiveTemplate(c *gin.Context, services *Deps, changes templateChangeHandler) {
	template, err := services.Templates.Archive(c.Request.Context(), middleware.GetGuildContext(c), c.Param("templateID"))
	if err != nil {
		writeTemplateError(c, err)
		return
	}
	if changes != nil {
		guildContext := middleware.GetGuildContext(c)
		if guildContext != nil && guildContext.Guild != nil {
			changes.HandleTemplateChange(c.Request.Context(), guildContext.Guild.ID, template.ID)
		}
	}

	c.JSON(http.StatusOK, gin.H{"template": template})
}

// restoreTemplate reverses archive without creating a new template identity.
// @Summary Restore an archived template
// @Tags Templates
// @Produce json
// @Param discordGuildID path string true "Discord guild ID"
// @Param templateID path string true "Template ID"
// @Param Idempotency-Key header string true "Retry-safe request key"
// @Security CookieAuth
// @Success 200 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /guilds/{discordGuildID}/templates/{templateID}/restore [post]
func restoreTemplate(c *gin.Context, services *Deps) {
	template, err := services.Templates.Restore(c.Request.Context(), middleware.GetGuildContext(c), c.Param("templateID"))
	if err != nil {
		writeTemplateError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"template": template})
}

// exportTemplate returns guild-neutral moderation policy only.
// @Summary Export a case template policy
// @Tags Templates
// @Produce json
// @Param discordGuildID path string true "Discord guild ID"
// @Param templateID path string true "Template ID"
// @Security CookieAuth
// @Success 200 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /guilds/{discordGuildID}/templates/{templateID}/export [get]
func exportTemplate(c *gin.Context, services *Deps) {
	policy, err := services.Templates.Export(c.Request.Context(), middleware.GetGuildContext(c), c.Param("templateID"))
	if err != nil {
		writeTemplateError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"policy": policy})
}

// importTemplate requires explicit confirmation before activating a new guild-owned identity.
// @Summary Import a case template policy
// @Tags Templates
// @Accept json
// @Produce json
// @Param discordGuildID path string true "Discord guild ID"
// @Param Idempotency-Key header string true "Retry-safe request key"
// @Param template body quack.TemplateImportInput true "Confirmed policy import"
// @Security CookieAuth
// @Success 201 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Router /guilds/{discordGuildID}/templates/import [post]
func importTemplate(c *gin.Context, services *Deps) {
	var input quack.TemplateImportInput
	if err := decodeStrictJSON(c, &input); err != nil {
		apierror.Write(c, http.StatusBadRequest, apierror.CodeValidation, "invalid import payload")
		return
	}
	template, err := services.Templates.Import(c.Request.Context(), middleware.GetGuildContext(c), input)
	if err != nil {
		writeTemplateError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"template": template})
}

// writeTemplateError maps template error into the preserved HTTP error response contract.
func writeTemplateError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, quack.ErrTemplatePermissionDenied):
		apierror.Write(c, http.StatusForbidden, apierror.CodeAuthorization, "template access denied")
	case errors.Is(err, quack.ErrTemplateValidation):
		apierror.Write(c, http.StatusBadRequest, apierror.CodeValidation, err.Error())
	case errors.Is(err, quack.ErrTemplateNotFound):
		apierror.Write(c, http.StatusNotFound, apierror.CodeNotFound, err.Error())
	case errors.Is(err, quack.ErrTemplateCompatibilityReviewRequired):
		var compatibilityError *quack.TemplateCompatibilityReviewError
		if errors.As(err, &compatibilityError) {
			c.JSON(http.StatusConflict, gin.H{
				"error":                quack.ErrTemplateCompatibilityReviewRequired.Error(),
				"template_id":          compatibilityError.TemplateID,
				"compatibility_reason": compatibilityError.Reason,
			})
			return
		}
		apierror.Write(c, http.StatusConflict, apierror.CodeConflict, quack.ErrTemplateCompatibilityReviewRequired.Error())
	default:
		apierror.Write(c, http.StatusInternalServerError, apierror.CodeInternal, "template operation failed")
	}
}
