package quack

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"
)

// compactEventLimit is how many of the latest timeline events GetCompact
// returns.
const compactEventLimit = 6

// Get returns one case, by ID or case number, with its full history. Only
// denials are audited.
func (s *CaseService) Get(ctx context.Context, guildContext *GuildStaffContext, caseRef string) (*CaseDetailResponse, error) {
	return s.detail(ctx, guildContext, caseRef, false)
}

// GetCompact is Get for space-limited views such as Discord: it returns only
// the latest six timeline events and loads attempts only for succeeded
// timeouts, to report when they end. Authorization and auditing match Get.
func (s *CaseService) GetCompact(ctx context.Context, guildContext *GuildStaffContext, caseRef string) (*CaseDetailResponse, error) {
	return s.detail(ctx, guildContext, caseRef, true)
}

// detail loads a staff case detail. compact limits the timeline
// and attempts as GetCompact describes.
func (s *CaseService) detail(ctx context.Context, guildContext *GuildStaffContext, caseRef string, compact bool) (*CaseDetailResponse, error) {
	item, err := s.readCase(ctx, guildContext, caseRef)
	if err != nil {
		return nil, err
	}
	actions, err := s.store.ListCaseActionExecutions(ctx, item.ID)
	if err != nil {
		return nil, err
	}
	var events []CaseEvent
	if compact {
		events, err = s.store.ListRecentCaseEvents(ctx, item.ID, compactEventLimit)
	} else {
		events, err = s.store.ListCaseEvents(ctx, item.ID)
	}
	if err != nil {
		return nil, err
	}
	executionIDs := make([]string, 0, len(actions))
	for _, action := range actions {
		if !compact || action.endsAt() {
			executionIDs = append(executionIDs, action.ID)
		}
	}
	var attempts []CaseActionAttempt
	if len(executionIDs) > 0 {
		if attempts, err = s.store.ListCaseActionAttempts(ctx, executionIDs); err != nil {
			return nil, err
		}
	}
	evidence, attachments, err := s.store.ListCaseEvidence(ctx, item.ID)
	if err != nil {
		return nil, err
	}
	notification, err := s.store.GetCaseNotification(ctx, item.ID)
	if err != nil {
		return nil, err
	}
	response := caseResponse(*item, actions)
	response.EvidenceIncomplete = evidenceIncomplete(evidence)
	for i := range response.Actions {
		response.Actions[i].TimeoutUntil = recordedTimeoutUntil(actions[i], attempts)
	}
	return &CaseDetailResponse{
		CaseResponse:     response,
		TemplateSnapshot: parseTemplateSnapshot(item.TemplateSnapshotJSON),
		Actions:          caseActionDetailResponses(actions, attempts),
		Events:           caseEventResponses(events),
		Evidence:         caseEvidenceResponses(evidence, attachments, false),
		Notification:     caseNotificationResponse(notification, false),
	}, nil
}

// readCase checks case read access and loads the guild's case by ID or
// number, auditing a denial.
func (s *CaseService) readCase(ctx context.Context, guildContext *GuildStaffContext, caseRef string) (*Case, error) {
	caseRef = strings.TrimSpace(caseRef)
	if err := requireCaseRead(guildContext); err != nil {
		_ = s.audit(ctx, guildContext, staffAttribution, string(AuditActionCaseRead), "case", caseRef, AuditResultDenied, "permission_denied")
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
	return item, nil
}

// CaseEvidencePage is one captured message of a case, for views that page
// through evidence one item at a time. Position is 1-based and clamped into
// 1..Total; Evidence is nil when the case has none.
type CaseEvidencePage struct {
	CaseID        string
	CaseNumber    uint64
	ContextValues []CaseContextValueResponse
	Evidence      *CaseEvidenceResponse
	Position      int
	Total         int64
}

// EvidencePage returns the case's evidence item at position, oldest first,
// without loading the rest. Authorization and auditing match Get.
func (s *CaseService) EvidencePage(ctx context.Context, guildContext *GuildStaffContext, caseRef string, position int) (*CaseEvidencePage, error) {
	item, err := s.readCase(ctx, guildContext, caseRef)
	if err != nil {
		return nil, err
	}
	snapshot, attachments, total, err := s.store.GetCaseEvidencePage(ctx, item.ID, position)
	if err != nil {
		return nil, err
	}
	page := &CaseEvidencePage{
		CaseID:        item.ID,
		CaseNumber:    item.CaseNumber,
		ContextValues: parseContextValues(item.ContextValuesJSON),
		Position:      min(max(position, 1), max(int(total), 1)),
		Total:         total,
	}
	if snapshot != nil {
		page.Evidence = &caseEvidenceResponses([]CaseEvidenceSnapshot{*snapshot}, attachments, false)[0]
	}
	return page, nil
}

// recordedTimeoutUntil is when a succeeded timeout ends, read from the
// response Discord gave its successful attempt. It never estimates: without
// a recorded expiry, or for any other action, it returns nil.
func recordedTimeoutUntil(action CaseActionExecution, attempts []CaseActionAttempt) *time.Time {
	if !action.endsAt() {
		return nil
	}
	for _, attempt := range slices.Backward(attempts) {
		if attempt.ExecutionID != action.ID || attempt.Status != ActionAttemptSucceeded {
			continue
		}
		var payload struct {
			Until string `json:"timeout_until"`
		}
		if json.Unmarshal([]byte(attempt.ResponsePayloadJSON), &payload) != nil {
			continue
		}
		if until, err := time.Parse(time.RFC3339, payload.Until); err == nil {
			return &until
		}
	}
	return nil
}
