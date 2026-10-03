package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
)

// failedActionResponse is the recovery queue's view of an action execution.
// Leases, config snapshots, and idempotency keys stay internal.
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

type failedActionListResponse struct {
	Executions []failedActionResponse `json:"executions"`
	Total      int64                  `json:"total"`
}

type failedActionEnvelope struct {
	Action failedActionResponse `json:"action"`
}

type reverseActionRequest struct {
	OriginalExecutionID string           `json:"original_execution_id"`
	ActionType          quack.ActionType `json:"action_type"`
	AppealID            *string          `json:"appeal_id"`
	Confirm             bool             `json:"confirm"`
}

func (s *Server) listFailedActions(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := pageParams(r)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, codeValidation, "invalid pagination")
		return
	}
	result, err := s.services.Actions.ListFailures(r.Context(), quack.StaffFromContext(r.Context()), limit, offset)
	if err != nil {
		writeCaseError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, newFailedActionList(result))
}

// retryFailedAction requeues the same action after a live permission check.
func (s *Server) retryFailedAction(w http.ResponseWriter, r *http.Request) {
	result, err := s.services.Actions.Retry(r.Context(), quack.StaffFromContext(r.Context()), r.PathValue("executionID"))
	if err != nil {
		writeCaseError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, failedActionEnvelope{Action: newFailedAction(*result)})
}

// dismissFailedAction removes a failure from the queue; its history stays.
func (s *Server) dismissFailedAction(w http.ResponseWriter, r *http.Request) {
	result, err := s.services.Actions.Dismiss(r.Context(), quack.StaffFromContext(r.Context()), r.PathValue("executionID"))
	if err != nil {
		writeCaseError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, failedActionEnvelope{Action: newFailedAction(*result)})
}

// reverseCaseAction queues a timeout removal or unban. The body must set
// confirm, so a reversal is never a single accidental click.
func (s *Server) reverseCaseAction(w http.ResponseWriter, r *http.Request) {
	var input reverseActionRequest
	if err := decodeJSON(r, &input); err != nil || !input.Confirm {
		writeError(w, r, http.StatusBadRequest, codeValidation, "confirmed reversal payload is required")
		return
	}
	result, err := s.services.Actions.ReverseForAppeal(r.Context(), quack.StaffFromContext(r.Context()), r.PathValue("caseRef"),
		input.OriginalExecutionID, input.ActionType, input.AppealID)
	if err != nil {
		writeCaseError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"action": result})
}

// newFailedActionList always returns an executions array, never null.
func newFailedActionList(result *quack.FailedCaseActionResult) failedActionListResponse {
	response := failedActionListResponse{Executions: []failedActionResponse{}}
	if result == nil {
		return response
	}
	response.Total = result.Total
	for _, execution := range result.Executions {
		response.Executions = append(response.Executions, newFailedAction(execution))
	}
	return response
}

func newFailedAction(execution quack.CaseActionExecution) failedActionResponse {
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

// pageParams reads limit (default 50, at most 100) and offset (default 0).
func pageParams(r *http.Request) (limit, offset int, err error) {
	q := r.URL.Query()
	limit = 50
	if raw := q.Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 100 {
			return 0, 0, errors.New("invalid limit")
		}
	}
	if raw := q.Get("offset"); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return 0, 0, errors.New("invalid offset")
		}
	}
	return limit, offset, nil
}
