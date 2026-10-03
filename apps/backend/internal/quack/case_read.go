package quack

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
)

// CaseListInput holds the raw case filters from a request.
type CaseListInput struct {
	Limit                  string
	Offset                 string
	TargetDiscordUserID    string
	ModeratorDiscordUserID string
	TemplateID             string
	Validity               string
	CaseNumber             string
	ActionResult           string
	AppealStatus           string
	CreatedAfter           string
	CreatedBefore          string
}

// CaseListResponse is a page of cases.
type CaseListResponse struct {
	Cases  []CaseResponse `json:"cases"`
	Total  int64          `json:"total"`
	Limit  int            `json:"limit"`
	Offset int            `json:"offset"`
}

// CaseProfileResponse is a page of one member's cases plus totals across all
// of them.
type CaseProfileResponse struct {
	CaseListResponse
	Summary CaseProfileSummary `json:"summary"`
}

// CaseProfileSummary counts a member's cases by validity and template.
type CaseProfileSummary struct {
	Total      int64            `json:"total"`
	ByValidity map[string]int64 `json:"by_validity"`
	ByTemplate map[string]int64 `json:"by_template"`
}

// CaseResponse is a case as staff see it in lists.
type CaseResponse struct {
	CreatedAt               time.Time                  `json:"created_at"`
	UpdatedAt               time.Time                  `json:"updated_at"`
	ID                      string                     `json:"id"`
	GuildID                 string                     `json:"guild_id"`
	CaseNumber              uint64                     `json:"case_number"`
	TemplateID              *string                    `json:"template_id"`
	TemplateVersion         uint                       `json:"template_version"`
	TargetDiscordUserID     string                     `json:"target_discord_user_id"`
	ModeratorDiscordUserID  string                     `json:"moderator_discord_user_id"`
	Reason                  string                     `json:"reason"`
	Validity                CaseValidity               `json:"validity"`
	Source                  CaseSource                 `json:"source"`
	ContextChannelDiscordID string                     `json:"context_channel_discord_id,omitempty"`
	ContextMessageDiscordID string                     `json:"context_message_discord_id,omitempty"`
	ContextURL              string                     `json:"context_url,omitempty"`
	Metadata                any                        `json:"metadata"`
	ContextValues           []CaseContextValueResponse `json:"context_values"`
	VoidedReason            string                     `json:"voided_reason,omitempty"`
	VoidedAt                *time.Time                 `json:"voided_at,omitempty"`
	ReplacementCaseID       *string                    `json:"replacement_case_id,omitempty"`
	ReplacesCaseID          *string                    `json:"replaces_case_id,omitempty"`
	SelectedLevel           *CaseSelectedLevel         `json:"selected_level,omitempty"`
	Actions                 []CaseActionResponse       `json:"actions"`
}

// CaseDetailResponse is a case with its full history, for staff.
type CaseDetailResponse struct {
	CaseResponse
	TemplateSnapshot *CaseTemplateSnapshotResponse `json:"template_snapshot,omitempty"`
	Actions          []CaseActionDetailResponse    `json:"actions"`
	Events           []CaseEventResponse           `json:"events"`
	Evidence         []CaseEvidenceResponse        `json:"evidence"`
	Notification     *CaseNotificationResponse     `json:"notification,omitempty"`
}

// CaseActionResponse is an execution's state. Lease details stay internal.
type CaseActionResponse struct {
	ID               string                `json:"id"`
	Position         int                   `json:"position"`
	ActionType       ActionType            `json:"action_type"`
	Status           ActionExecutionStatus `json:"status"`
	TemplateActionID *string               `json:"template_action_id"`
	IdempotencyKey   string                `json:"idempotency_key"`
	NotifyUser       bool                  `json:"notify_user"`
	NotificationType string                `json:"notification_type,omitempty"`
	MaxRetries       uint8                 `json:"max_retries"`
	RetryBackoffMS   int                   `json:"retry_backoff_ms"`
	SafeForRetry     bool                  `json:"safe_for_retry"`
	Irreversible     bool                  `json:"irreversible"`
}

// CaseActionDetailResponse is an execution with its configuration and
// attempts.
type CaseActionDetailResponse struct {
	CaseActionResponse
	ConfigSnapshot any                         `json:"config_snapshot"`
	AttemptCount   uint8                       `json:"attempt_count"`
	LastErrorCode  string                      `json:"last_error_code,omitempty"`
	LastError      string                      `json:"last_error,omitempty"`
	StartedAt      *time.Time                  `json:"started_at,omitempty"`
	FinishedAt     *time.Time                  `json:"finished_at,omitempty"`
	NextRetryAt    *time.Time                  `json:"next_retry_at,omitempty"`
	Attempts       []CaseActionAttemptResponse `json:"attempts"`
}

// CaseActionAttemptResponse is one recorded call to Discord.
type CaseActionAttemptResponse struct {
	ID              string              `json:"id"`
	ExecutionID     string              `json:"execution_id"`
	AttemptNumber   uint8               `json:"attempt_number"`
	Status          ActionAttemptStatus `json:"status"`
	WorkerID        string              `json:"worker_id,omitempty"`
	StartedAt       time.Time           `json:"started_at"`
	FinishedAt      *time.Time          `json:"finished_at,omitempty"`
	DurationMS      int64               `json:"duration_ms"`
	ErrorCode       string              `json:"error_code,omitempty"`
	ErrorMessage    string              `json:"error_message,omitempty"`
	RequestPayload  any                 `json:"request_payload"`
	ResponsePayload any                 `json:"response_payload"`
}

// CaseEventResponse is a case timeline entry.
type CaseEventResponse struct {
	ID                 string          `json:"id"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
	EventType          CaseEventType   `json:"event_type"`
	ActorDiscordUserID string          `json:"actor_discord_user_id,omitempty"`
	ActorType          string          `json:"actor_type"`
	Visibility         EventVisibility `json:"visibility"`
	Body               string          `json:"body"`
	Metadata           any             `json:"metadata"`
}

// CaseNotificationResponse is the delivery state of a case's notification.
type CaseNotificationResponse struct {
	Status        NotificationStatus `json:"status"`
	AttemptCount  uint8              `json:"attempt_count"`
	LastErrorCode string             `json:"last_error_code,omitempty"`
	LastError     string             `json:"last_error,omitempty"`
	SentAt        *time.Time         `json:"sent_at,omitempty"`
}

// List returns a filtered page of the guild's cases.
func (s *CaseService) List(ctx context.Context, guildContext *GuildStaffContext, input CaseListInput) (*CaseListResponse, error) {
	const action = string(AuditActionCaseSearch)
	params, err := caseListParams(guildContext, input)
	if err != nil {
		if errors.Is(err, ErrCasePermissionDenied) {
			_ = s.audit(ctx, guildContext, staffAttribution, action, "case", "list", AuditResultDenied, "permission_denied")
		}
		return nil, err
	}
	page, err := s.store.ListCasesFiltered(ctx, params)
	if err != nil {
		return nil, err
	}
	responses, err := s.caseResponses(ctx, page.Cases)
	if err != nil {
		return nil, err
	}
	if err := s.audit(ctx, guildContext, staffAttribution, action, "case", "list", AuditResultSuccess, ""); err != nil {
		return nil, err
	}
	return &CaseListResponse{Cases: responses, Total: page.Total, Limit: params.Limit, Offset: params.Offset}, nil
}

// Get returns one case, by ID or case number, with its full history.
func (s *CaseService) Get(ctx context.Context, guildContext *GuildStaffContext, caseRef string) (*CaseDetailResponse, error) {
	const action = string(AuditActionCaseRead)
	caseRef = strings.TrimSpace(caseRef)
	if err := requireCaseRead(guildContext); err != nil {
		_ = s.audit(ctx, guildContext, staffAttribution, action, "case", caseRef, AuditResultDenied, "permission_denied")
		return nil, err
	}
	if caseRef == "" {
		return nil, caseValidationError("case reference is required")
	}
	item, err := s.store.GetCaseByIDOrNumber(ctx, guildContext.Guild.ID, caseRef)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, ErrCaseNotFound
	}
	actions, err := s.store.ListCaseActionExecutions(ctx, item.ID)
	if err != nil {
		return nil, err
	}
	events, err := s.store.ListCaseEvents(ctx, item.ID)
	if err != nil {
		return nil, err
	}
	executionIDs := make([]string, 0, len(actions))
	for _, action := range actions {
		executionIDs = append(executionIDs, action.ID)
	}
	attempts, err := s.store.ListCaseActionAttempts(ctx, executionIDs)
	if err != nil {
		return nil, err
	}
	evidence, attachments, err := s.store.ListCaseEvidence(ctx, item.ID)
	if err != nil {
		return nil, err
	}
	notification, err := s.store.GetCaseNotification(ctx, item.ID)
	if err != nil {
		return nil, err
	}
	if err := s.audit(ctx, guildContext, staffAttribution, action, "case", item.ID, AuditResultSuccess, ""); err != nil {
		return nil, err
	}
	return &CaseDetailResponse{
		CaseResponse:     caseResponse(*item, actions),
		TemplateSnapshot: parseTemplateSnapshot(item.TemplateSnapshotJSON),
		Actions:          caseActionDetailResponses(actions, attempts),
		Events:           caseEventResponses(events),
		Evidence:         caseEvidenceResponses(evidence, attachments, false),
		Notification:     caseNotificationResponse(notification, false),
	}, nil
}

// UserHistory returns a page of one member's cases and their all-time
// totals.
func (s *CaseService) UserHistory(ctx context.Context, guildContext *GuildStaffContext, targetDiscordUserID string, input CaseListInput) (*CaseProfileResponse, error) {
	targetDiscordUserID = strings.TrimSpace(targetDiscordUserID)
	if targetDiscordUserID == "" {
		return nil, caseValidationError("target discord user id is required")
	}
	input.TargetDiscordUserID = targetDiscordUserID
	list, err := s.List(ctx, guildContext, input)
	if err != nil {
		return nil, err
	}
	summary, err := s.store.TargetCaseSummary(ctx, guildContext.Guild.ID, targetDiscordUserID)
	if err != nil {
		return nil, err
	}
	err = s.audit(ctx, guildContext, staffAttribution, string(AuditActionCaseHistoryRead),
		"member", targetDiscordUserID, AuditResultSuccess, "")
	if err != nil {
		return nil, err
	}
	byValidity := make(map[string]int64, len(summary.ByValidity))
	for validity, count := range summary.ByValidity {
		byValidity[string(validity)] = count
	}
	return &CaseProfileResponse{
		CaseListResponse: *list,
		Summary: CaseProfileSummary{
			Total:      summary.Total,
			ByValidity: byValidity,
			ByTemplate: summary.ByTemplate,
		},
	}, nil
}

// Actions returns a case's executions without auditing a read. Adapters use
// it to follow enforcement progress on a case they just created.
func (s *CaseService) Actions(ctx context.Context, caseID string) ([]CaseActionExecution, error) {
	return s.store.ListCaseActionExecutions(ctx, caseID)
}

func requireCaseRead(guildContext *GuildStaffContext) error {
	if guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil {
		return caseValidationError("missing guild context")
	}
	if !guildContext.Can(PermissionActionCaseRead) {
		return ErrCasePermissionDenied
	}
	return nil
}

// caseListParams checks read access and validates the raw filters.
func caseListParams(guildContext *GuildStaffContext, input CaseListInput) (ListCasesParams, error) {
	if err := requireCaseRead(guildContext); err != nil {
		return ListCasesParams{}, err
	}
	limit, offset, err := parsePage(input.Limit, input.Offset, caseValidationError)
	if err != nil {
		return ListCasesParams{}, err
	}
	validity := CaseValidity(strings.TrimSpace(input.Validity))
	if validity != "" && validity != CaseValidityValid && validity != CaseValidityVoided {
		return ListCasesParams{}, caseValidationError("validity is invalid")
	}
	caseNumber := strings.TrimSpace(input.CaseNumber)
	if caseNumber != "" {
		if parsed, err := strconv.ParseUint(caseNumber, 10, 64); err != nil || parsed == 0 {
			return ListCasesParams{}, caseValidationError("case_number is invalid")
		}
	}
	actionResult := strings.TrimSpace(input.ActionResult)
	if actionResult != "" && !validActionExecutionStatus(ActionExecutionStatus(actionResult)) {
		return ListCasesParams{}, caseValidationError("action_result is invalid")
	}
	appealStatus := strings.TrimSpace(input.AppealStatus)
	if appealStatus != "" && !validAppealStatus(AppealStatus(appealStatus)) {
		return ListCasesParams{}, caseValidationError("appeal_status is invalid")
	}
	createdAfter, err := parseFilterTime(input.CreatedAfter, caseValidationError)
	if err != nil {
		return ListCasesParams{}, err
	}
	createdBefore, err := parseFilterTime(input.CreatedBefore, caseValidationError)
	if err != nil {
		return ListCasesParams{}, err
	}
	return ListCasesParams{
		GuildID:                guildContext.Guild.ID,
		TargetDiscordUserID:    strings.TrimSpace(input.TargetDiscordUserID),
		ModeratorDiscordUserID: strings.TrimSpace(input.ModeratorDiscordUserID),
		TemplateID:             strings.TrimSpace(input.TemplateID),
		Validity:               validity,
		CaseNumber:             caseNumber,
		ActionResult:           actionResult,
		AppealStatus:           appealStatus,
		CreatedAfter:           createdAfter,
		CreatedBefore:          createdBefore,
		Limit:                  limit,
		Offset:                 offset,
	}, nil
}

func caseResponse(item Case, actions []CaseActionExecution) CaseResponse {
	response := CaseResponse{
		CreatedAt:               item.CreatedAt,
		UpdatedAt:               item.UpdatedAt,
		ID:                      item.ID,
		GuildID:                 item.GuildID,
		CaseNumber:              item.CaseNumber,
		TemplateID:              item.TemplateID,
		TemplateVersion:         item.TemplateVersion,
		TargetDiscordUserID:     item.TargetDiscordUserID,
		ModeratorDiscordUserID:  item.ModeratorDiscordUserID,
		Reason:                  item.Reason,
		Validity:                item.Validity,
		Source:                  item.Source,
		ContextChannelDiscordID: item.ContextChannelDiscordID,
		ContextMessageDiscordID: item.ContextMessageDiscordID,
		ContextURL:              item.ContextURL,
		Metadata:                parseJSON(item.MetadataJSON),
		ContextValues:           parseContextValues(item.ContextValuesJSON),
		VoidedReason:            item.VoidedReason,
		VoidedAt:                item.VoidedAt,
		ReplacementCaseID:       item.ReplacementCaseID,
		ReplacesCaseID:          item.ReplacesCaseID,
		SelectedLevel:           snapshotSelectedLevel(item.TemplateSnapshotJSON),
		Actions:                 make([]CaseActionResponse, 0, len(actions)),
	}
	for _, action := range actions {
		response.Actions = append(response.Actions, caseActionResponse(action))
	}
	return response
}

// caseResponses builds responses for a page of cases, loading all their
// actions in one query.
func (s *CaseService) caseResponses(ctx context.Context, cases []Case) ([]CaseResponse, error) {
	responses := make([]CaseResponse, 0, len(cases))
	if len(cases) == 0 {
		return responses, nil
	}
	ids := make([]string, len(cases))
	for i, item := range cases {
		ids[i] = item.ID
	}
	actions, err := s.store.ListCaseActionsForCases(ctx, ids)
	if err != nil {
		return nil, err
	}
	byCase := make(map[string][]CaseActionExecution, len(cases))
	for _, action := range actions {
		byCase[action.CaseID] = append(byCase[action.CaseID], action)
	}
	for _, item := range cases {
		responses = append(responses, caseResponse(item, byCase[item.ID]))
	}
	return responses, nil
}

func caseActionResponse(action CaseActionExecution) CaseActionResponse {
	return CaseActionResponse{
		ID:               action.ID,
		Position:         action.Position,
		ActionType:       action.ActionType,
		Status:           action.Status,
		TemplateActionID: action.TemplateActionID,
		IdempotencyKey:   action.IdempotencyKey,
		NotifyUser:       action.NotifyUser,
		NotificationType: action.NotificationType,
		MaxRetries:       action.MaxRetries,
		RetryBackoffMS:   action.RetryBackoffMS,
		SafeForRetry:     action.SafeForRetry,
		Irreversible:     action.ActionType.Irreversible(),
	}
}

func caseActionDetailResponses(actions []CaseActionExecution, attempts []CaseActionAttempt) []CaseActionDetailResponse {
	byExecution := map[string][]CaseActionAttemptResponse{}
	for _, attempt := range attempts {
		byExecution[attempt.ExecutionID] = append(byExecution[attempt.ExecutionID], CaseActionAttemptResponse{
			ID:              attempt.ID,
			ExecutionID:     attempt.ExecutionID,
			AttemptNumber:   attempt.AttemptNumber,
			Status:          attempt.Status,
			WorkerID:        attempt.WorkerID,
			StartedAt:       attempt.StartedAt,
			FinishedAt:      attempt.FinishedAt,
			DurationMS:      attempt.DurationMS,
			ErrorCode:       attempt.ErrorCode,
			ErrorMessage:    attempt.ErrorMessage,
			RequestPayload:  parseJSON(attempt.RequestPayloadJSON),
			ResponsePayload: parseJSON(attempt.ResponsePayloadJSON),
		})
	}
	responses := make([]CaseActionDetailResponse, 0, len(actions))
	for _, action := range actions {
		responses = append(responses, CaseActionDetailResponse{
			CaseActionResponse: caseActionResponse(action),
			ConfigSnapshot:     parseJSON(action.ConfigSnapshotJSON),
			AttemptCount:       action.AttemptCount,
			LastErrorCode:      action.LastErrorCode,
			LastError:          action.LastError,
			StartedAt:          action.StartedAt,
			FinishedAt:         action.FinishedAt,
			NextRetryAt:        action.NextRetryAt,
			Attempts:           byExecution[action.ID],
		})
	}
	return responses
}

func caseEventResponses(events []CaseEvent) []CaseEventResponse {
	responses := make([]CaseEventResponse, 0, len(events))
	for _, event := range events {
		responses = append(responses, CaseEventResponse{
			ID:                 event.ID,
			CreatedAt:          event.CreatedAt,
			UpdatedAt:          event.UpdatedAt,
			EventType:          event.EventType,
			ActorDiscordUserID: event.ActorDiscordUserID,
			ActorType:          event.ActorType,
			Visibility:         event.Visibility,
			Body:               event.Body,
			Metadata:           parseJSON(event.MetadataJSON),
		})
	}
	return responses
}

// caseNotificationResponse hides delivery errors from members.
func caseNotificationResponse(item *CaseNotification, member bool) *CaseNotificationResponse {
	if item == nil {
		return nil
	}
	response := &CaseNotificationResponse{
		Status:        item.Status,
		AttemptCount:  item.AttemptCount,
		LastErrorCode: item.LastErrorCode,
		LastError:     item.LastError,
		SentAt:        item.SentAt,
	}
	if member {
		response.LastErrorCode = ""
		response.LastError = ""
	}
	return response
}
