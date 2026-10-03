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
	if err := s.auditStaff(ctx, guildContext, string(AuditActionAppealRead), item.ID); err != nil {
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
	page, err := s.store.ListAppeals(ctx, AppealListParams{
		GuildID: guildContext.Guild.ID,
		Status:  status,
		Limit:   limit,
		Offset:  offset,
	})
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
	if err := s.auditStaff(ctx, guildContext, string(AuditActionAppealQueueRead), "list"); err != nil {
		return nil, err
	}
	return &AppealListResponse{Appeals: responses, Total: page.Total, Limit: limit, Offset: offset}, nil
}

// appealTransition is one staff decision: the states it applies to, the
// state it leads to, and the timeline event it records.
type appealTransition struct {
	from     []AppealStatus
	to       AppealStatus
	event    AppealEventType
	voidCase bool
}

// The staff decisions on an appeal.
var (
	requestInformation = appealTransition{
		from:  []AppealStatus{AppealStatusPending},
		to:    AppealStatusNeedsInformation,
		event: AppealEventInformationAsked,
	}
	reopenAppeal = appealTransition{
		from:  []AppealStatus{AppealStatusRejected, AppealStatusClosed},
		to:    AppealStatusNeedsInformation,
		event: AppealEventReopened,
	}
	acceptAppeal = appealTransition{
		from:     []AppealStatus{AppealStatusPending},
		to:       AppealStatusAccepted,
		event:    AppealEventAccepted,
		voidCase: true,
	}
	rejectAppeal = appealTransition{
		from:  []AppealStatus{AppealStatusPending},
		to:    AppealStatusRejected,
		event: AppealEventRejected,
	}
	closeAppeal = appealTransition{
		from:  []AppealStatus{AppealStatusPending, AppealStatusNeedsInformation},
		to:    AppealStatusClosed,
		event: AppealEventClosed,
	}
)

// RequestInformation asks the member for more information on a pending
// appeal.
func (s *AppealService) RequestInformation(ctx context.Context, guildContext *GuildStaffContext, appealID, reason string) (*AppealResponse, error) {
	return s.transition(ctx, guildContext, appealID, reason, requestInformation)
}

// Reopen asks for more information on a rejected or closed appeal. The
// same appeal is reused; a case never gets a second one.
func (s *AppealService) Reopen(ctx context.Context, guildContext *GuildStaffContext, appealID, reason string) (*AppealResponse, error) {
	return s.transition(ctx, guildContext, appealID, reason, reopenAppeal)
}

// Accept accepts a pending appeal and voids its case in the same
// transaction. Voiding queues reversals of the case's succeeded timeouts and
// bans, linked to the appeal.
func (s *AppealService) Accept(ctx context.Context, guildContext *GuildStaffContext, appealID, reason string) (*AppealResponse, error) {
	return s.transition(ctx, guildContext, appealID, reason, acceptAppeal)
}

// Reject rejects a pending appeal. The case stays valid.
func (s *AppealService) Reject(ctx context.Context, guildContext *GuildStaffContext, appealID, reason string) (*AppealResponse, error) {
	return s.transition(ctx, guildContext, appealID, reason, rejectAppeal)
}

// Close closes an undecided appeal without a decision. The case stays
// valid.
func (s *AppealService) Close(ctx context.Context, guildContext *GuildStaffContext, appealID, reason string) (*AppealResponse, error) {
	return s.transition(ctx, guildContext, appealID, reason, closeAppeal)
}

// transition applies a staff decision. The store enforces from atomically,
// so two staff members deciding at once cannot both succeed.
func (s *AppealService) transition(ctx context.Context, guildContext *GuildStaffContext, appealID, reason string, change appealTransition) (*AppealResponse, error) {
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
	intent, err := s.decisionIntent(ctx, guildContext, item, change.to, reason)
	if err != nil {
		return nil, err
	}
	actorID, bits := guildContext.Staff.DiscordUserID, guildContext.PermissionBits
	params := TransitionAppealParams{
		GuildID:            item.GuildID,
		AppealID:           item.ID,
		ActorDiscordUserID: actorID,
		AllowedFrom:        change.from,
		To:                 change.to,
		Reason:             reason,
		VoidCase:           change.voidCase,
		Event: AppealEvent{
			EventType:          string(change.event),
			ActorDiscordUserID: actorID,
			ActorType:          "staff",
			Body:               reason,
			MetadataJSON:       "{}",
		},
		AppealAudit: webAudit(ctx, item.GuildID, actorID, bits,
			"appeal."+string(change.event), "appeal", item.ID, AuditResultSuccess),
		Notification: AppealNotification{
			TargetDiscordUserID: item.TargetDiscordUserID,
			Audience:            AppealNotificationMember,
			Status:              AppealNotificationPending,
			Body:                memberNotificationBody(change.to, reason),
			DecisionIntentJSON:  marshalJSONObject(intent),
		},
	}
	if change.voidCase {
		caseAudit := webAudit(ctx, item.GuildID, actorID, bits,
			string(AuditActionCaseVoidAppeal), "case", "", AuditResultSuccess)
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

// decisionIntent freezes what the member's notice about a decision says:
// the guild's name, the case number, and, for an accepted appeal, the
// guild's rejoin invite.
func (s *AppealService) decisionIntent(ctx context.Context, guildContext *GuildStaffContext, item *Appeal, to AppealStatus, reason string) (AppealDecisionIntent, error) {
	intent := AppealDecisionIntent{Version: 1, Status: to, Reason: reason, GuildName: guildContext.Guild.Name}
	if item.CaseID != nil {
		appealed, err := s.store.GetCaseByID(ctx, *item.CaseID)
		if err != nil {
			return intent, err
		}
		if appealed == nil || appealed.GuildID != item.GuildID {
			return intent, ErrAppealNotFound
		}
		intent.CaseID, intent.CaseNumber = appealed.ID, appealed.CaseNumber
	}
	if to == AppealStatusAccepted {
		settings, err := s.store.GetGuildSettings(ctx, item.GuildID)
		if err != nil {
			return intent, err
		}
		if settings != nil {
			intent.RejoinURL = settings.AppealRejoinURL
		}
	}
	return intent, nil
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

// auditStaff records a successful staff read of the guild's appeals.
func (s *AppealService) auditStaff(ctx context.Context, guildContext *GuildStaffContext, action, resourceID string) error {
	entry := webAudit(ctx, guildContext.Guild.ID, guildContext.Staff.DiscordUserID, guildContext.PermissionBits,
		action, "appeal", resourceID, AuditResultSuccess)
	return recordAudit(ctx, s.store, &entry)
}

func requireAppealReview(guildContext *GuildStaffContext) error {
	if guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil || !guildContext.Can(PermissionActionAppealReview) {
		return ErrAppealPermissionDenied
	}
	return nil
}
