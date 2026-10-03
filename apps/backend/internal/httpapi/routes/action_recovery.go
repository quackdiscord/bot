package routes

import (
	"net/http"
	"time"

	"github.com/quackdiscord/bot/internal/httpapi/apierror"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/httpapi/middleware"
	"github.com/quackdiscord/bot/internal/quack"
)

// failedActionResponse is the public recovery-queue projection of an action
// execution. Worker leases, configuration snapshots, and idempotency state stay
// inside the domain model.
type failedActionResponse struct {
	ID            string                      `json:"id"`
	CaseID        string                      `json:"case_id"`
	ActionType    quack.ActionType            `json:"action_type"`
	Status        quack.ActionExecutionStatus `json:"status"`
	AttemptCount  uint8                       `json:"attempt_count"`
	MaxRetries    uint8                       `json:"max_retries"`
	SafeForRetry  bool                        `json:"safe_for_retry"`
	LastErrorCode string                      `json:"last_error_code,omitempty"`
	LastError     string                      `json:"last_error,omitempty"`
	CreatedAt     time.Time                   `json:"created_at"`
	UpdatedAt     time.Time                   `json:"updated_at"`
}

// failedActionListResponse is the stable paginated action-recovery contract.
type failedActionListResponse struct {
	Executions []failedActionResponse `json:"executions"`
	Total      int64                  `json:"total"`
}

// failedActionEnvelope wraps a changed recovery item consistently for retry
// and dismiss responses.
type failedActionEnvelope struct {
	Action failedActionResponse `json:"action"`
}

// listFailedActions returns the active staff recovery queue.
// @Summary List failed case actions
// @Tags Action recovery
// @Produce json
// @Param discordGuildID path string true "Discord guild ID"
// @Param limit query int false "Page size"
// @Param offset query int false "Page offset"
// @Security CookieAuth
// @Success 200 {object} failedActionListResponse
// @Failure 400 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Router /guilds/{discordGuildID}/action-failures [get]
func listFailedActions(c *gin.Context, services *Deps) {
	limit, offset, err := parsePageInts(c.Query("limit"), c.Query("offset"))
	if err != nil {
		apierror.Write(c, http.StatusBadRequest, apierror.CodeValidation, "invalid pagination")
		return
	}
	result, err := services.Actions.ListFailures(c.Request.Context(), middleware.GetGuildContext(c), limit, offset)
	if err != nil {
		writeCaseError(c, err)
		return
	}
	c.JSON(http.StatusOK, failedActionListResponseFromModel(result))
}

// retryFailedAction requests a live-authorized retry of the same immutable action.
// @Summary Retry a failed case action
// @Tags Action recovery
// @Produce json
// @Param discordGuildID path string true "Discord guild ID"
// @Param executionID path string true "Action execution ID"
// @Param Idempotency-Key header string true "Retry-safe request key"
// @Security CookieAuth
// @Success 202 {object} failedActionEnvelope
// @Failure 403 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /guilds/{discordGuildID}/action-failures/{executionID}/retry [post]
func retryFailedAction(c *gin.Context, services *Deps) {
	result, err := services.Actions.Retry(c.Request.Context(), middleware.GetGuildContext(c), c.Param("executionID"))
	if err != nil {
		writeCaseError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, failedActionEnvelope{
		Action: failedActionResponseFromModel(*result),
	})
}

// dismissFailedAction removes a failure from active review without deleting history.
// @Summary Dismiss a failed case action
// @Tags Action recovery
// @Produce json
// @Param discordGuildID path string true "Discord guild ID"
// @Param executionID path string true "Action execution ID"
// @Param Idempotency-Key header string true "Retry-safe request key"
// @Security CookieAuth
// @Success 200 {object} failedActionEnvelope
// @Failure 403 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /guilds/{discordGuildID}/action-failures/{executionID}/dismiss [post]
func dismissFailedAction(c *gin.Context, services *Deps) {
	result, err := services.Actions.Dismiss(c.Request.Context(), middleware.GetGuildContext(c), c.Param("executionID"))
	if err != nil {
		writeCaseError(c, err)
		return
	}
	c.JSON(http.StatusOK, failedActionEnvelope{
		Action: failedActionResponseFromModel(*result),
	})
}

// failedActionListResponseFromModel converts the domain pagination result to
// the documented HTTP shape and guarantees a JSON array for an empty queue.
func failedActionListResponseFromModel(result *quack.FailedCaseActionResult) failedActionListResponse {
	response := failedActionListResponse{
		Executions: make([]failedActionResponse, 0),
	}
	if result == nil {
		return response
	}

	response.Total = result.Total
	response.Executions = make([]failedActionResponse, 0, len(result.Executions))
	for _, execution := range result.Executions {
		response.Executions = append(response.Executions, failedActionResponseFromModel(execution))
	}
	return response
}

// failedActionResponseFromModel selects only fields needed for staff recovery
// and applies the API's snake_case JSON naming.
func failedActionResponseFromModel(execution quack.CaseActionExecution) failedActionResponse {
	return failedActionResponse{
		ID:            execution.ID,
		CaseID:        execution.CaseID,
		ActionType:    execution.ActionType,
		Status:        execution.Status,
		AttemptCount:  execution.AttemptCount,
		MaxRetries:    execution.MaxRetries,
		SafeForRetry:  execution.SafeForRetry,
		LastErrorCode: execution.LastErrorCode,
		LastError:     execution.LastError,
		CreatedAt:     execution.CreatedAt,
		UpdatedAt:     execution.UpdatedAt,
	}
}

type reverseActionRequest struct {
	OriginalExecutionID string           `json:"original_execution_id"`
	ActionType          quack.ActionType `json:"action_type"`
	AppealID            *string          `json:"appeal_id"`
	Confirm             bool             `json:"confirm"`
}

// reverseCaseAction queues an explicitly confirmed timeout removal or unban.
// @Summary Reverse a case action
// @Tags Action recovery
// @Accept json
// @Produce json
// @Param discordGuildID path string true "Discord guild ID"
// @Param caseRef path string true "Case ID or number"
// @Param Idempotency-Key header string true "Retry-safe request key"
// @Param request body reverseActionRequest true "Confirmed reversal"
// @Security CookieAuth
// @Success 202 {object} map[string]interface{}
// @Failure 400 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /guilds/{discordGuildID}/cases/{caseRef}/reversals [post]
func reverseCaseAction(c *gin.Context, services *Deps) {
	var input reverseActionRequest
	if err := decodeStrictJSON(c, &input); err != nil || !input.Confirm {
		apierror.Write(c, http.StatusBadRequest, apierror.CodeValidation, "confirmed reversal payload is required")
		return
	}
	result, err := services.Actions.ReverseForAppeal(c.Request.Context(), middleware.GetGuildContext(c), c.Param("caseRef"), input.OriginalExecutionID, input.ActionType, input.AppealID)
	if err != nil {
		writeCaseError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"action": result})
}
