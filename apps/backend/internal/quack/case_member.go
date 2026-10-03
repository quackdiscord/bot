package quack

import (
	"context"
	"strings"
	"time"
)

// MemberCaseSummary is a case as its member sees it in a list. It never
// names the moderator.
type MemberCaseSummary struct {
	ID            string             `json:"id"`
	GuildID       string             `json:"guild_id"`
	CaseNumber    uint64             `json:"case_number"`
	Reason        string             `json:"official_reason"`
	Validity      CaseValidity       `json:"validity"`
	CreatedAt     time.Time          `json:"created_at"`
	SelectedLevel *CaseSelectedLevel `json:"selected_outcome,omitempty"`
	Appealable    bool               `json:"appealable"`
	AppealID      string             `json:"appeal_id,omitempty"`
	AppealStatus  AppealStatus       `json:"appeal_status,omitempty"`
}

// MemberCaseListResponse is a page of a member's own cases.
type MemberCaseListResponse struct {
	Cases  []MemberCaseSummary `json:"cases"`
	Total  int64               `json:"total"`
	Limit  int                 `json:"limit"`
	Offset int                 `json:"offset"`
}

// MemberCaseDetail is a case as its member sees it: public events only, no
// staff identities, and no internal delivery diagnostics.
type MemberCaseDetail struct {
	ID                string                     `json:"id"`
	GuildID           string                     `json:"guild_id"`
	CaseNumber        uint64                     `json:"case_number"`
	TemplateID        *string                    `json:"template_id"`
	Reason            string                     `json:"official_reason"`
	Validity          CaseValidity               `json:"validity"`
	VoidedReason      string                     `json:"voided_reason,omitempty"`
	ReplacementCaseID *string                    `json:"replacement_case_id,omitempty"`
	CreatedAt         time.Time                  `json:"created_at"`
	ContextValues     []CaseContextValueResponse `json:"context"`
	SelectedLevel     *CaseSelectedLevel         `json:"selected_outcome"`
	Enforcement       *MemberEnforcementOutcome  `json:"enforcement,omitempty"`
	Evidence          []CaseEvidenceResponse     `json:"evidence"`
	Events            []CaseEventResponse        `json:"history"`
	Notification      *CaseNotificationResponse  `json:"notification,omitempty"`
	Appealable        bool                       `json:"appealable"`
	AppealID          string                     `json:"appeal_id,omitempty"`
	AppealStatus      AppealStatus               `json:"appeal_status,omitempty"`
}

// MemberEnforcementOutcome is what was done to the member and whether it
// happened.
type MemberEnforcementOutcome struct {
	ActionType ActionType            `json:"action_type"`
	Status     ActionExecutionStatus `json:"status"`
}

// ListMemberCases returns the cases in a guild that target the signed-in
// member. The member does not need to still be in the guild.
func (s *CaseService) ListMemberCases(ctx context.Context, guildID, memberDiscordUserID string, input CaseListInput) (*MemberCaseListResponse, error) {
	guildID = strings.TrimSpace(guildID)
	memberDiscordUserID = strings.TrimSpace(memberDiscordUserID)
	if guildID == "" || memberDiscordUserID == "" {
		return nil, caseValidationError("guild and member identity are required")
	}
	limit, offset, err := parsePage(input.Limit, input.Offset, caseValidationError)
	if err != nil {
		return nil, err
	}
	page, err := s.store.ListCasesFiltered(ctx, ListCasesParams{GuildID: guildID, TargetDiscordUserID: memberDiscordUserID, Limit: limit, Offset: offset})
	if err != nil {
		return nil, err
	}
	responses := make([]MemberCaseSummary, 0, len(page.Cases))
	for _, item := range page.Cases {
		appeal, err := s.store.GetAppealByCaseID(ctx, item.ID)
		if err != nil {
			return nil, err
		}
		summary := MemberCaseSummary{
			ID:            item.ID,
			GuildID:       item.GuildID,
			CaseNumber:    item.CaseNumber,
			Reason:        item.Reason,
			Validity:      item.Validity,
			CreatedAt:     item.CreatedAt,
			SelectedLevel: snapshotSelectedLevel(item.TemplateSnapshotJSON),
			Appealable:    canAppeal(item, appeal),
		}
		if appeal != nil {
			summary.AppealID, summary.AppealStatus = appeal.ID, appeal.Status
		}
		responses = append(responses, summary)
	}
	if err := s.memberReadAudit(ctx, guildID, memberDiscordUserID, "member_case.list", "guild", guildID, AuditResultSuccess, ""); err != nil {
		return nil, err
	}
	return &MemberCaseListResponse{Cases: responses, Total: page.Total, Limit: limit, Offset: offset}, nil
}

// GetMemberCase returns a case to the member it targets. Anyone else gets
// ErrCaseNotFound, and the attempt is audited.
func (s *CaseService) GetMemberCase(ctx context.Context, caseID, memberDiscordUserID string) (*MemberCaseDetail, error) {
	memberDiscordUserID = strings.TrimSpace(memberDiscordUserID)
	item, err := s.store.GetCaseByID(ctx, strings.TrimSpace(caseID))
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, ErrCaseNotFound
	}
	if item.TargetDiscordUserID != memberDiscordUserID {
		_ = s.memberReadAudit(ctx, item.GuildID, memberDiscordUserID, "member_case.read", "case", item.ID, AuditResultDenied, "not_case_target")
		return nil, ErrCaseNotFound
	}
	if err := s.memberReadAudit(ctx, item.GuildID, memberDiscordUserID, "member_case.read", "case", item.ID, AuditResultSuccess, ""); err != nil {
		return nil, err
	}
	evidence, attachments, err := s.store.ListCaseEvidence(ctx, item.ID)
	if err != nil {
		return nil, err
	}
	events, err := s.store.ListCaseEvents(ctx, item.ID)
	if err != nil {
		return nil, err
	}
	publicEvents := make([]CaseEvent, 0, len(events))
	for _, event := range events {
		if event.Visibility == EventVisibilityPublic {
			event.ActorDiscordUserID = ""
			event.MetadataJSON = "{}"
			publicEvents = append(publicEvents, event)
		}
	}
	notification, err := s.store.GetCaseNotification(ctx, item.ID)
	if err != nil {
		return nil, err
	}
	actions, err := s.store.ListCaseActionExecutions(ctx, item.ID)
	if err != nil {
		return nil, err
	}
	var enforcement *MemberEnforcementOutcome
	if len(actions) > 0 {
		enforcement = &MemberEnforcementOutcome{ActionType: actions[0].ActionType, Status: actions[0].Status}
	}
	appeal, err := s.store.GetAppealByCaseID(ctx, item.ID)
	if err != nil {
		return nil, err
	}
	detail := &MemberCaseDetail{
		ID:                item.ID,
		GuildID:           item.GuildID,
		CaseNumber:        item.CaseNumber,
		TemplateID:        item.TemplateID,
		Reason:            item.Reason,
		Validity:          item.Validity,
		VoidedReason:      item.VoidedReason,
		ReplacementCaseID: item.ReplacementCaseID,
		CreatedAt:         item.CreatedAt,
		ContextValues:     parseContextValues(item.ContextValuesJSON),
		SelectedLevel:     snapshotSelectedLevel(item.TemplateSnapshotJSON),
		Enforcement:       enforcement,
		Evidence:          caseEvidenceResponses(evidence, attachments, true),
		Events:            caseEventResponses(publicEvents),
		Notification:      caseNotificationResponse(notification, true),
		Appealable:        canAppeal(*item, appeal),
	}
	if appeal != nil {
		detail.AppealID, detail.AppealStatus = appeal.ID, appeal.Status
	}
	return detail, nil
}

func (s *CaseService) memberReadAudit(ctx context.Context, guildID, actorID, action, resourceType, resourceID string, result AuditResult, failureReason string) error {
	requestID, correlationID := TraceIDsFromContext(ctx)
	return recordAudit(ctx, s.store, &AuditLogEntry{
		GuildID:            guildID,
		ActorDiscordUserID: actorID,
		Source:             AuditSourceWeb,
		Action:             action,
		ResourceType:       resourceType,
		ResourceID:         resourceID,
		Result:             result,
		FailureReason:      failureReason,
		RequestID:          requestID,
		CorrelationID:      correlationID,
		MetadataJSON:       "{}",
	})
}
