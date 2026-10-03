package quack

import (
	"context"
	"errors"
	"log/slog"
	"strings"
)

// AppealDecisionInput is the reason staff give for a decision. Members see
// it.
type AppealDecisionInput struct {
	Reason string `json:"reason"`
}

// AppealListResponse is a page of appeals.
type AppealListResponse struct {
	Appeals []AppealResponse `json:"appeals"`
	Total   int64            `json:"total"`
	Limit   int              `json:"limit"`
	Offset  int              `json:"offset"`
}

// GetStaff returns an appeal to staff who can review appeals.
func (s *AppealService) GetStaff(ctx context.Context, guildContext *GuildStaffContext, appealID string) (*AppealResponse, error) {
	if err := requireAppealReview(guildContext); err != nil {
		return nil, err
	}
	item, err := s.store.GetAppealByID(ctx, strings.TrimSpace(appealID))
	if err != nil {
		return nil, err
	}
	if item == nil || item.GuildID != guildContext.Guild.ID {
		return nil, ErrAppealNotFound
	}
	entry := appealAudit(ctx, item.GuildID, guildContext.Staff.DiscordUserID, guildContext.PermissionBits, string(AuditActionAppealRead), "appeal", item.ID, AuditResultSuccess)
	if err := recordAudit(ctx, s.store, &entry); err != nil {
		return nil, err
	}
	return s.response(ctx, item, false)
}

// ListStaff returns a page of the guild's appeals, optionally filtered by
// status.
func (s *AppealService) ListStaff(ctx context.Context, guildContext *GuildStaffContext, status AppealStatus, limit, offset int) (*AppealListResponse, error) {
	if err := requireAppealReview(guildContext); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 100 || offset < 0 || (status != "" && !validAppealStatus(status)) {
		return nil, appealValidationError("invalid appeal queue filter")
	}
	page, err := s.store.ListAppeals(ctx, AppealListParams{GuildID: guildContext.Guild.ID, Status: status, Limit: limit, Offset: offset})
	if err != nil {
		return nil, err
	}
	responses := make([]AppealResponse, 0, len(page.Appeals))
	for i := range page.Appeals {
		response, err := s.response(ctx, &page.Appeals[i], false)
		if err != nil {
			return nil, err
		}
		responses = append(responses, *response)
	}
	entry := appealAudit(ctx, guildContext.Guild.ID, guildContext.Staff.DiscordUserID, guildContext.PermissionBits, string(AuditActionAppealQueueRead), "appeal", "list", AuditResultSuccess)
	if err := recordAudit(ctx, s.store, &entry); err != nil {
		return nil, err
	}
	return &AppealListResponse{Appeals: responses, Total: page.Total, Limit: limit, Offset: offset}, nil
}

// RequestInformation asks the member for more information on a pending
// appeal.
func (s *AppealService) RequestInformation(ctx context.Context, guildContext *GuildStaffContext, appealID, reason string) (*AppealResponse, error) {
	return s.transition(ctx, guildContext, appealID, reason, []AppealStatus{AppealStatusPending}, AppealStatusNeedsInformation, AppealEventInformationAsked, false)
}

// Reopen asks for more information on a rejected or closed appeal. The
// same appeal is reused; a case never gets a second one.
func (s *AppealService) Reopen(ctx context.Context, guildContext *GuildStaffContext, appealID, reason string) (*AppealResponse, error) {
	return s.transition(ctx, guildContext, appealID, reason, []AppealStatus{AppealStatusRejected, AppealStatusClosed}, AppealStatusNeedsInformation, AppealEventReopened, false)
}

// Accept accepts a pending appeal and voids its case in the same
// transaction. Reversing a timeout or ban is a separate, explicit step.
func (s *AppealService) Accept(ctx context.Context, guildContext *GuildStaffContext, appealID, reason string) (*AppealResponse, error) {
	return s.transition(ctx, guildContext, appealID, reason, []AppealStatus{AppealStatusPending}, AppealStatusAccepted, AppealEventAccepted, true)
}

// Reject rejects a pending appeal. The case stays valid.
func (s *AppealService) Reject(ctx context.Context, guildContext *GuildStaffContext, appealID, reason string) (*AppealResponse, error) {
	return s.transition(ctx, guildContext, appealID, reason, []AppealStatus{AppealStatusPending}, AppealStatusRejected, AppealEventRejected, false)
}

// Close closes an undecided appeal without a decision. The case stays
// valid.
func (s *AppealService) Close(ctx context.Context, guildContext *GuildStaffContext, appealID, reason string) (*AppealResponse, error) {
	return s.transition(ctx, guildContext, appealID, reason, []AppealStatus{AppealStatusPending, AppealStatusNeedsInformation}, AppealStatusClosed, AppealEventClosed, false)
}

func (s *AppealService) transition(ctx context.Context, guildContext *GuildStaffContext, appealID, reason string, from []AppealStatus, to AppealStatus, eventType AppealEventType, voidCase bool) (*AppealResponse, error) {
	if err := requireAppealReview(guildContext); err != nil {
		return nil, err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || len([]rune(reason)) > 2000 {
		return nil, appealValidationError("reason must be between 1 and 2000 characters")
	}
	item, err := s.store.GetAppealByID(ctx, strings.TrimSpace(appealID))
	if err != nil {
		return nil, err
	}
	if item == nil || item.GuildID != guildContext.Guild.ID {
		return nil, ErrAppealNotFound
	}
	actorID, bits := guildContext.Staff.DiscordUserID, guildContext.PermissionBits
	params := TransitionAppealParams{
		GuildID:            item.GuildID,
		AppealID:           item.ID,
		ActorDiscordUserID: actorID,
		AllowedFrom:        from,
		To:                 to,
		Reason:             reason,
		VoidCase:           voidCase,
		Event: AppealEvent{
			EventType:          string(eventType),
			ActorDiscordUserID: actorID,
			ActorType:          "staff",
			Body:               reason,
			MetadataJSON:       "{}",
		},
		AppealAudit: appealAudit(ctx, item.GuildID, actorID, bits, "appeal."+string(eventType), "appeal", item.ID, AuditResultSuccess),
		Notification: AppealNotification{
			TargetDiscordUserID: item.TargetDiscordUserID,
			Audience:            AppealNotificationMember,
			Status:              AppealNotificationPending,
			Body:                memberNotificationBody(to, reason),
		},
	}
	if voidCase {
		caseAudit := appealAudit(ctx, item.GuildID, actorID, bits, string(AuditActionCaseVoidAppeal), "case", "", AuditResultSuccess)
		params.CaseAudit = &caseAudit
	}
	updated, err := s.store.TransitionAppeal(ctx, params)
	if errors.Is(err, ErrAppealStateConflict) || errors.Is(err, ErrAppealCaseIneligible) {
		return nil, ErrAppealConflict
	}
	if err != nil {
		return nil, err
	}
	slog.InfoContext(ctx, "Appeal decision recorded", "guild_id", updated.GuildID, "appeal_id", updated.ID, "status", updated.Status)
	return s.response(ctx, updated, false)
}

// memberNotificationBody is the DM a member gets when staff act on their
// appeal. It quotes the staff reason but never names the staff member.
func memberNotificationBody(status AppealStatus, reason string) string {
	switch status {
	case AppealStatusNeedsInformation:
		return "Staff requested more information on your appeal: " + reason
	case AppealStatusAccepted:
		return "Your appeal was accepted: " + reason
	case AppealStatusRejected:
		return "Your appeal was rejected: " + reason
	default:
		return "Your appeal was closed: " + reason
	}
}

func requireAppealReview(guildContext *GuildStaffContext) error {
	if guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil || !guildContext.Can(PermissionActionAppealReview) {
		return ErrAppealPermissionDenied
	}
	return nil
}
