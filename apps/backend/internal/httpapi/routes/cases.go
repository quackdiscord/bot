package routes

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/quackdiscord/bot/internal/httpapi/apierror"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/httpapi/middleware"
	"github.com/quackdiscord/bot/internal/quack"
)

// caseCreateRequest is the strict dashboard contract; adapter-owned reason and source cannot be supplied by callers.
type caseCreateRequest struct {
	TemplateID              string                        `json:"template_id"`
	TargetDiscordUserID     string                        `json:"target_discord_user_id"`
	ContextChannelDiscordID string                        `json:"context_channel_discord_id"`
	ContextMessageDiscordID string                        `json:"context_message_discord_id"`
	ContextURL              string                        `json:"context_url"`
	Metadata                json.RawMessage               `json:"metadata" swaggertype:"object"`
	ContextValues           []quack.CaseContextValueInput `json:"context_values"`
	EvidenceLinks           []string                      `json:"evidence_links"`
	ReplacesCaseID          string                        `json:"replaces_case_id"`
}

// listCases returns cases subject to authorization, ordering, and filtering constraints.
// @Summary List guild cases
// @Tags Cases
// @Produce json
// @Param discordGuildID path string true "Discord guild ID"
// @Param limit query int false "Page size"
// @Param offset query int false "Page offset"
// @Param target_discord_user_id query string false "Target Discord user ID"
// @Param template_id query string false "Template ID"
// @Param validity query string false "Case validity"
// @Security CookieAuth
// @Success 200 {object} quack.CaseListResponse
// @Failure 400 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Router /guilds/{discordGuildID}/cases [get]
func listCases(c *gin.Context, services *Deps) {
	result, err := services.Cases.List(c.Request.Context(), middleware.GetGuildContext(c), caseListInput(c))
	if err != nil {
		writeCaseError(c, err)
		return
	}

	c.JSON(http.StatusOK, result)
}

// createCase creates case while preserving validation, authorization, and persistence invariants.
// @Summary Create a moderation case
// @Tags Cases
// @Accept json
// @Produce json
// @Param discordGuildID path string true "Discord guild ID"
// @Param Idempotency-Key header string true "Retry-safe request key"
// @Param case body caseCreateRequest true "Case definition"
// @Security CookieAuth
// @Success 201 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /guilds/{discordGuildID}/cases [post]
func createCase(c *gin.Context, services *Deps) {
	var input caseCreateRequest
	if err := decodeStrictJSON(c, &input); err != nil {
		apierror.Write(c, http.StatusBadRequest, apierror.CodeValidation, "invalid case payload")
		return
	}

	created, err := services.Cases.Create(c.Request.Context(), middleware.GetGuildContext(c), quack.CaseInput{
		TemplateID:              input.TemplateID,
		TargetDiscordUserID:     input.TargetDiscordUserID,
		Source:                  quack.CaseSourceDashboard,
		ContextChannelDiscordID: input.ContextChannelDiscordID,
		ContextMessageDiscordID: input.ContextMessageDiscordID,
		ContextURL:              input.ContextURL,
		Metadata:                input.Metadata,
		ContextValues:           input.ContextValues, EvidenceLinks: input.EvidenceLinks, ReplacesCaseID: input.ReplacesCaseID, IdempotencyKey: c.GetHeader("Idempotency-Key"),
	})
	if err != nil {
		writeCaseError(c, err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{"case": created})
}

type voidCaseRequest struct {
	Reason            string  `json:"reason"`
	ReplacementCaseID *string `json:"replacement_case_id"`
}

// voidCase preserves the case and creates immutable correction history.
// @Summary Void a moderation case
// @Tags Cases
// @Accept json
// @Produce json
// @Param discordGuildID path string true "Discord guild ID"
// @Param caseRef path string true "Case ID or number"
// @Param Idempotency-Key header string true "Retry-safe request key"
// @Param request body voidCaseRequest true "Void reason and replacement"
// @Security CookieAuth
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /guilds/{discordGuildID}/cases/{caseRef}/void [post]
func voidCase(c *gin.Context, services *Deps) {
	var input voidCaseRequest
	if err := decodeStrictJSON(c, &input); err != nil {
		apierror.Write(c, http.StatusBadRequest, apierror.CodeValidation, "invalid void payload")
		return
	}
	result, err := services.Cases.Void(c.Request.Context(), middleware.GetGuildContext(c), c.Param("caseRef"), input.Reason, input.ReplacementCaseID)
	if err != nil {
		writeCaseError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"case": result})
}

// getCase retrieves case without exposing the underlying adapter implementation.
// @Summary Get a guild case
// @Tags Cases
// @Produce json
// @Param discordGuildID path string true "Discord guild ID"
// @Param caseRef path string true "Case ID or number"
// @Security CookieAuth
// @Success 200 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /guilds/{discordGuildID}/cases/{caseRef} [get]
func getCase(c *gin.Context, services *Deps) {
	result, err := services.Cases.Get(c.Request.Context(), middleware.GetGuildContext(c), c.Param("caseRef"))
	if err != nil {
		writeCaseError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"case": result})
}

// listUserCases returns user cases subject to authorization, ordering, and filtering constraints.
// @Summary List a guild member's case history
// @Tags Cases
// @Produce json
// @Param discordGuildID path string true "Discord guild ID"
// @Param targetDiscordUserID path string true "Target Discord user ID"
// @Param limit query int false "Page size"
// @Param offset query int false "Page offset"
// @Security CookieAuth
// @Success 200 {object} quack.CaseProfileResponse
// @Failure 400 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Router /guilds/{discordGuildID}/users/{targetDiscordUserID}/cases [get]
func listUserCases(c *gin.Context, services *Deps) {
	result, err := services.Cases.UserHistory(c.Request.Context(), middleware.GetGuildContext(c), c.Param("targetDiscordUserID"), caseListInput(c))
	if err != nil {
		writeCaseError(c, err)
		return
	}

	c.JSON(http.StatusOK, result)
}

// caseListInput encapsulates the case list input rule so callers share one consistent package implementation.
func caseListInput(c *gin.Context) quack.CaseListInput {
	return quack.CaseListInput{
		Limit:                  c.Query("limit"),
		Offset:                 c.Query("offset"),
		TargetDiscordUserID:    c.Query("target_discord_user_id"),
		ModeratorDiscordUserID: c.Query("moderator_discord_user_id"),
		TemplateID:             c.Query("template_id"),
		Validity:               c.Query("validity"),
		CaseNumber:             c.Query("case_number"), ActionResult: c.Query("action_result"), AppealStatus: c.Query("appeal_status"), CreatedAfter: c.Query("created_after"), CreatedBefore: c.Query("created_before"),
	}
}

// writeCaseError maps case error into the preserved HTTP error response contract.
func writeCaseError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, quack.ErrCaseValidation):
		apierror.Write(c, http.StatusBadRequest, apierror.CodeValidation, err.Error())
	case errors.Is(err, quack.ErrCasePermissionDenied), errors.Is(err, quack.ErrAuthorizationDenied):
		apierror.Write(c, http.StatusForbidden, apierror.CodeAuthorization, err.Error())
	case errors.Is(err, quack.ErrCaseTemplateNotAvailable):
		apierror.Write(c, http.StatusNotFound, apierror.CodeNotFound, err.Error())
	case errors.Is(err, quack.ErrCaseNotFound):
		apierror.Write(c, http.StatusNotFound, apierror.CodeNotFound, err.Error())
	default:
		apierror.Write(c, http.StatusInternalServerError, apierror.CodeInternal, "case operation failed")
	}
}
