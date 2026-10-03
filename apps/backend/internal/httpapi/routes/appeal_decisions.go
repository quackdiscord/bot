package routes

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/httpapi/apierror"
	"github.com/quackdiscord/bot/internal/httpapi/middleware"
	"github.com/quackdiscord/bot/internal/quack"
)

// requestAppealInformation asks the member for more appeal context.
// @Summary Request more appeal information
// @Tags Appeals
// @Accept json
// @Produce json
// @Param discordGuildID path string true "Discord guild ID"
// @Param appealID path string true "Appeal ID"
// @Param Idempotency-Key header string true "Retry-safe request key"
// @Param decision body quack.AppealDecisionInput true "Decision reason"
// @Security CookieAuth
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} apierror.Response
// @Failure 403 {object} apierror.Response
// @Failure 404 {object} apierror.Response
// @Failure 409 {object} apierror.Response
// @Router /guilds/{discordGuildID}/appeals/{appealID}/request-information [post]
func requestAppealInformation(c *gin.Context, appeals *quack.AppealService) {
	transitionAppeal(c, appeals, "request-information")
}

// reopenAppeal returns an appeal to staff review.
// @Summary Reopen an appeal
// @Tags Appeals
// @Accept json
// @Produce json
// @Param discordGuildID path string true "Discord guild ID"
// @Param appealID path string true "Appeal ID"
// @Param Idempotency-Key header string true "Retry-safe request key"
// @Param decision body quack.AppealDecisionInput true "Decision reason"
// @Security CookieAuth
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} apierror.Response
// @Failure 403 {object} apierror.Response
// @Failure 404 {object} apierror.Response
// @Failure 409 {object} apierror.Response
// @Router /guilds/{discordGuildID}/appeals/{appealID}/reopen [post]
func reopenAppeal(c *gin.Context, appeals *quack.AppealService) {
	transitionAppeal(c, appeals, "reopen")
}

// acceptAppeal accepts an appeal for separate reversal review.
// @Summary Accept an appeal
// @Tags Appeals
// @Accept json
// @Produce json
// @Param discordGuildID path string true "Discord guild ID"
// @Param appealID path string true "Appeal ID"
// @Param Idempotency-Key header string true "Retry-safe request key"
// @Param decision body quack.AppealDecisionInput true "Decision reason"
// @Security CookieAuth
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} apierror.Response
// @Failure 403 {object} apierror.Response
// @Failure 404 {object} apierror.Response
// @Failure 409 {object} apierror.Response
// @Router /guilds/{discordGuildID}/appeals/{appealID}/accept [post]
func acceptAppeal(c *gin.Context, appeals *quack.AppealService) {
	transitionAppeal(c, appeals, "accept")
}

// rejectAppeal rejects an appeal with a staff reason.
// @Summary Reject an appeal
// @Tags Appeals
// @Accept json
// @Produce json
// @Param discordGuildID path string true "Discord guild ID"
// @Param appealID path string true "Appeal ID"
// @Param Idempotency-Key header string true "Retry-safe request key"
// @Param decision body quack.AppealDecisionInput true "Decision reason"
// @Security CookieAuth
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} apierror.Response
// @Failure 403 {object} apierror.Response
// @Failure 404 {object} apierror.Response
// @Failure 409 {object} apierror.Response
// @Router /guilds/{discordGuildID}/appeals/{appealID}/reject [post]
func rejectAppeal(c *gin.Context, appeals *quack.AppealService) {
	transitionAppeal(c, appeals, "reject")
}

// closeAppeal closes an appeal without changing the case.
// @Summary Close an appeal
// @Tags Appeals
// @Accept json
// @Produce json
// @Param discordGuildID path string true "Discord guild ID"
// @Param appealID path string true "Appeal ID"
// @Param Idempotency-Key header string true "Retry-safe request key"
// @Param decision body quack.AppealDecisionInput true "Decision reason"
// @Security CookieAuth
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} apierror.Response
// @Failure 403 {object} apierror.Response
// @Failure 404 {object} apierror.Response
// @Failure 409 {object} apierror.Response
// @Router /guilds/{discordGuildID}/appeals/{appealID}/close [post]
func closeAppeal(c *gin.Context, appeals *quack.AppealService) {
	transitionAppeal(c, appeals, "close")
}

// transitionAppeal applies one explicit staff appeal state transition.
func transitionAppeal(c *gin.Context, appeals *quack.AppealService, transition string) {
	var input quack.AppealDecisionInput
	if err := decodeStrictJSON(c, &input); err != nil {
		apierror.Write(c, http.StatusBadRequest, apierror.CodeValidation, "invalid appeal decision payload")
		return
	}
	ctx := middleware.GetGuildContext(c)
	var result *quack.AppealResponse
	var err error
	switch transition {
	case "request-information":
		result, err = appeals.RequestInformation(c.Request.Context(), ctx, c.Param("appealID"), input.Reason)
	case "reopen":
		result, err = appeals.Reopen(c.Request.Context(), ctx, c.Param("appealID"), input.Reason)
	case "accept":
		result, err = appeals.Accept(c.Request.Context(), ctx, c.Param("appealID"), input.Reason)
	case "reject":
		result, err = appeals.Reject(c.Request.Context(), ctx, c.Param("appealID"), input.Reason)
	case "close":
		result, err = appeals.Close(c.Request.Context(), ctx, c.Param("appealID"), input.Reason)
	default:
		err = quack.ErrAppealValidation
	}
	if err != nil {
		writeAppealError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"appeal": result})
}

type appealReversalRequest struct {
	OriginalExecutionID string           `json:"original_execution_id"`
	ActionType          quack.ActionType `json:"action_type"`
	Confirm             bool             `json:"confirm"`
}

// reverseAcceptedAppeal queues a separately confirmed reversal for an accepted appeal.
// @Summary Reverse an accepted appeal action
// @Tags Appeals
// @Accept json
// @Produce json
// @Param discordGuildID path string true "Discord guild ID"
// @Param appealID path string true "Appeal ID"
// @Param Idempotency-Key header string true "Retry-safe request key"
// @Param reversal body appealReversalRequest true "Confirmed reversal"
// @Security CookieAuth
// @Success 202 {object} map[string]interface{}
// @Failure 400 {object} apierror.Response
// @Failure 403 {object} apierror.Response
// @Failure 404 {object} apierror.Response
// @Failure 409 {object} apierror.Response
// @Router /guilds/{discordGuildID}/appeals/{appealID}/reversals [post]
func reverseAcceptedAppeal(c *gin.Context, services *Deps, appeals *quack.AppealService) {
	var input appealReversalRequest
	if err := decodeStrictJSON(c, &input); err != nil || !input.Confirm {
		apierror.Write(c, http.StatusBadRequest, apierror.CodeValidation, "confirmed reversal payload is required")
		return
	}
	appeal, err := appeals.GetStaff(c.Request.Context(), middleware.GetGuildContext(c), c.Param("appealID"))
	if err != nil {
		writeAppealError(c, err)
		return
	}
	if appeal.Status != quack.AppealStatusAccepted {
		writeAppealError(c, quack.ErrAppealConflict)
		return
	}
	appealID := appeal.ID
	result, err := services.Actions.ReverseForAppeal(c.Request.Context(), middleware.GetGuildContext(c), appeal.CaseID, input.OriginalExecutionID, input.ActionType, &appealID)
	if err != nil {
		writeCaseError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"action": result})
}
