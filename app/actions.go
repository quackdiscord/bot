package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
)

type ActionService struct {
	store    *storage.Store
	discord  DiscordActionClient
	handlers map[structs.ActionType]ActionExecutor
}

type ActionExecutor interface {
	Execute(ctx context.Context, action ActionExecutionContext) ActionResult
}

type ActionExecutorFunc func(ctx context.Context, action ActionExecutionContext) ActionResult

func (f ActionExecutorFunc) Execute(ctx context.Context, action ActionExecutionContext) ActionResult {
	return f(ctx, action)
}

type ActionExecutionContext struct {
	Case      structs.Case
	Settings  structs.GuildSettings
	Execution structs.CaseActionExecution
	Config    map[string]any
}

type ActionResult struct {
	Retryable bool
	ErrorCode string
	Error     string
	Response  map[string]any
}

func NewActionService(store *storage.Store, discord DiscordActionClient) *ActionService {
	service := &ActionService{
		store:   store,
		discord: discord,
		handlers: map[structs.ActionType]ActionExecutor{
			structs.ActionRecordWarning: ActionExecutorFunc(recordWarningAction),
			structs.ActionTimeoutUser:   ActionExecutorFunc(unsupportedIrreversibleAction),
			structs.ActionKickUser:      ActionExecutorFunc(unsupportedIrreversibleAction),
			structs.ActionBanUser:       ActionExecutorFunc(unsupportedIrreversibleAction),
		},
	}
	service.handlers[structs.ActionSendDM] = ActionExecutorFunc(service.sendDMAction)
	service.handlers[structs.ActionWriteModLog] = ActionExecutorFunc(service.writeModLogAction)
	return service
}

func (s *ActionService) ProcessCaseActions(ctx context.Context, caseID string) error {
	if s == nil || s.store == nil {
		return errors.New("action service is not configured")
	}

	workerID := actionWorkerID()
	for {
		claimed, err := s.store.ClaimNextCaseAction(ctx, storage.ClaimCaseActionParams{
			CaseID:   strings.TrimSpace(caseID),
			WorkerID: workerID,
		})
		if err != nil {
			return err
		}
		if claimed == nil {
			return nil
		}

		if err := s.processClaimedAction(ctx, workerID, *claimed); err != nil {
			return err
		}
	}
}

func (s *ActionService) processClaimedAction(ctx context.Context, workerID string, claimed storage.ClaimedCaseAction) error {
	handler, ok := s.handlers[claimed.Execution.ActionType]
	if !ok || handler == nil {
		handler = ActionExecutorFunc(unsupportedAction)
	}

	config := parseConfigMap(claimed.Execution.ConfigSnapshotJSON)
	actionContext := ActionExecutionContext{
		Case:      claimed.Case,
		Settings:  claimed.Settings,
		Execution: claimed.Execution,
		Config:    config,
	}
	result := handler.Execute(ctx, actionContext)
	requestPayload := map[string]any{
		"case_id":      claimed.Case.ID,
		"execution_id": claimed.Execution.ID,
		"action_type":  claimed.Execution.ActionType,
		"config":       config,
	}

	attemptStatus := structs.ActionAttemptSucceeded
	executionStatus := structs.ActionExecutionSucceeded
	eventType := structs.CaseEventActionSucceeded
	eventBody := fmt.Sprintf("Action %s succeeded", claimed.Execution.ActionType)
	var nextRetryAt *time.Time

	if result.Error != "" {
		attemptStatus = structs.ActionAttemptFailed
		eventType = structs.CaseEventActionFailed
		eventBody = fmt.Sprintf("Action %s failed: %s", claimed.Execution.ActionType, result.Error)
		executionStatus = structs.ActionExecutionFailed
		if shouldRetryAction(claimed.Execution, result) {
			next := nextRetryTime(claimed.Execution)
			nextRetryAt = &next
			executionStatus = structs.ActionExecutionRetrying
			eventBody = fmt.Sprintf("Action %s failed and will retry: %s", claimed.Execution.ActionType, result.Error)
		}
	}

	if result.Response == nil {
		result.Response = map[string]any{}
	}
	if result.Error != "" {
		result.Response["error"] = result.Error
	}

	err := s.store.CompleteCaseAction(ctx, storage.CompleteCaseActionParams{
		ExecutionID:         claimed.Execution.ID,
		AttemptNumber:       claimed.Execution.AttemptCount,
		WorkerID:            workerID,
		AttemptStatus:       attemptStatus,
		ExecutionStatus:     executionStatus,
		ErrorCode:           result.ErrorCode,
		ErrorMessage:        result.Error,
		RequestPayloadJSON:  mustMarshalJSONObject(requestPayload),
		ResponsePayloadJSON: mustMarshalJSONObject(result.Response),
		NextRetryAt:         nextRetryAt,
		EventType:           eventType,
		EventBody:           eventBody,
		EventMetadataJSON: mustMarshalJSONObject(map[string]any{
			"execution_id": claimed.Execution.ID,
			"action_type":  claimed.Execution.ActionType,
			"retrying":     executionStatus == structs.ActionExecutionRetrying,
		}),
	})
	if err != nil {
		return err
	}

	if executionStatus == structs.ActionExecutionFailed && !continueOnError(claimed.Case, claimed.Execution.Position) {
		return s.store.SkipCaseActions(ctx, storage.SkipCaseActionsParams{
			CaseID:        claimed.Case.ID,
			AfterPosition: claimed.Execution.Position,
			Reason:        fmt.Sprintf("previous action %s failed", claimed.Execution.ID),
		})
	}
	if executionStatus == structs.ActionExecutionRetrying && nextRetryAt != nil {
		scheduleCaseActions(claimed.Case.ID, time.Until(*nextRetryAt))
	}

	return nil
}

func recordWarningAction(ctx context.Context, action ActionExecutionContext) ActionResult {
	_ = ctx
	return ActionResult{Response: map[string]any{
		"recorded": true,
		"case_id":  action.Case.ID,
	}}
}

func (s *ActionService) sendDMAction(ctx context.Context, action ActionExecutionContext) ActionResult {
	if s.discord == nil {
		return permanentActionError("discord_unavailable", "discord action client is not configured")
	}

	message := configString(action.Config, "message")
	if message == "" {
		message = fmt.Sprintf("You received a moderation case in this server: %s", action.Case.Reason)
	}

	response, err := s.discord.SendDM(ctx, action.Case.TargetDiscordUserID, message)
	if err != nil {
		return actionErrorFromDiscord(err)
	}
	return ActionResult{Response: response}
}

func (s *ActionService) writeModLogAction(ctx context.Context, action ActionExecutionContext) ActionResult {
	if s.discord == nil {
		return permanentActionError("discord_unavailable", "discord action client is not configured")
	}

	channelID := configString(action.Config, "channel_id")
	if channelID == "" {
		channelID = strings.TrimSpace(action.Settings.ModLogChannelDiscordID)
	}
	if channelID == "" {
		return permanentActionError("mod_log_channel_missing", "mod log channel is not configured")
	}

	message := configString(action.Config, "message")
	if message == "" {
		message = fmt.Sprintf("Case #%d: <@%s> was moderated by <@%s> for %s", action.Case.CaseNumber, action.Case.TargetDiscordUserID, action.Case.ModeratorDiscordUserID, action.Case.Reason)
	}

	response, err := s.discord.SendModLog(ctx, channelID, message)
	if err != nil {
		return actionErrorFromDiscord(err)
	}
	return ActionResult{Response: response}
}

func unsupportedAction(ctx context.Context, action ActionExecutionContext) ActionResult {
	_ = ctx
	return permanentActionError("unsupported_action", fmt.Sprintf("action type %s is not supported", action.Execution.ActionType))
}

func unsupportedIrreversibleAction(ctx context.Context, action ActionExecutionContext) ActionResult {
	_ = ctx
	return permanentActionError("irreversible_action_unsupported", fmt.Sprintf("action type %s is not enabled in this phase", action.Execution.ActionType))
}

func shouldRetryAction(execution structs.CaseActionExecution, result ActionResult) bool {
	if !result.Retryable || !execution.SafeForRetry {
		return false
	}
	return execution.AttemptCount <= execution.MaxRetries
}

func nextRetryTime(execution structs.CaseActionExecution) time.Time {
	backoff := execution.RetryBackoffMS
	if backoff <= 0 {
		backoff = 1000
	}
	return time.Now().UTC().Add(time.Duration(backoff) * time.Millisecond)
}

func continueOnError(caseModel structs.Case, position int) bool {
	var snapshot struct {
		Actions []struct {
			Position        int  `json:"position"`
			ContinueOnError bool `json:"continue_on_error"`
		} `json:"actions"`
	}
	if err := json.Unmarshal([]byte(caseModel.TemplateSnapshotJSON), &snapshot); err != nil {
		return false
	}
	for _, action := range snapshot.Actions {
		if action.Position == position {
			return action.ContinueOnError
		}
	}
	return false
}

func parseConfigMap(body string) map[string]any {
	if strings.TrimSpace(body) == "" {
		return map[string]any{}
	}

	var config map[string]any
	if err := json.Unmarshal([]byte(body), &config); err != nil || config == nil {
		return map[string]any{}
	}
	return config
}

func configString(config map[string]any, key string) string {
	value, ok := config[key]
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func mustMarshalJSONObject(value any) string {
	body, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(body)
}

func permanentActionError(code, message string) ActionResult {
	return ActionResult{ErrorCode: code, Error: message}
}

func retryableActionError(code, message string) ActionResult {
	return ActionResult{Retryable: true, ErrorCode: code, Error: message}
}

func actionWorkerID() string {
	return fmt.Sprintf("action-worker:%d", time.Now().UTC().UnixNano())
}
