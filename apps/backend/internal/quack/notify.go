package quack

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// prepareNotification opens the member's DM channel before a kick or ban,
// while the bot still shares a guild with them. Failing to prepare never
// blocks enforcement; the notification just falls back to a plain DM.
func (s *ActionService) prepareNotification(ctx context.Context, item Case) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	notification, err := s.store.GetCaseNotification(ctx, item.ID)
	if err != nil {
		slog.ErrorContext(ctx, "Could not load notification before enforcement", "case_id", item.ID, "error", err)
		return
	}
	if notification == nil || notification.Status != NotificationPending {
		return
	}
	if s.messenger == nil {
		_ = s.store.PrepareCaseNotification(ctx, item.ID, "", "prepared DM adapter is unavailable")
		return
	}
	channelID, prepareErr := s.messenger.PrepareDM(ctx, item.TargetDiscordUserID)
	message := ""
	if prepareErr != nil {
		message = redactDiscordError(prepareErr)
	}
	if err := s.store.PrepareCaseNotification(ctx, item.ID, channelID, message); err != nil {
		slog.ErrorContext(ctx, "Could not record prepared notification", "case_id", item.ID, "error", err)
	}
}

// sendNotification sends the case's notification once its enforcement has
// settled, so the message can report the outcome. It is attempted once;
// failures are recorded on the case, not retried.
func (s *ActionService) sendNotification(ctx context.Context, workerID, caseID string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	claimed, err := s.store.ClaimCaseNotification(ctx, ClaimCaseNotificationParams{CaseID: caseID, WorkerID: workerID})
	if err != nil || claimed == nil {
		return err
	}
	item, err := s.store.GetCaseByID(ctx, caseID)
	if err != nil {
		return err
	}
	if item == nil {
		return ErrCaseNotFound
	}
	guild, err := s.store.GetGuildByID(ctx, item.GuildID)
	if err != nil {
		return err
	}
	settings, err := s.store.GetGuildSettings(ctx, item.GuildID)
	if err != nil {
		return err
	}
	actions, err := s.store.ListCaseActionExecutions(ctx, item.ID)
	if err != nil {
		return err
	}
	message := renderCaseNotification(*item, guild, settings, actions)
	sender, rich := s.messenger.(CaseNotificationSender)
	var request CaseNotificationRequest
	if rich {
		request = s.caseNotificationRequest(ctx, *item, guild, settings, actions)
		request.PreparedChannelDiscordID = claimed.PreparedChannelDiscordID
	}
	if err := s.store.BeginCaseNotificationDelivery(ctx, claimed.ID, claimed.LeaseToken); err != nil {
		return err
	}

	var response map[string]any
	var sendErr error
	switch {
	case s.messenger == nil:
		sendErr = errors.New("discord messenger is not configured")
	case rich:
		var receipt CaseNotificationReceipt
		receipt, sendErr = sender.DeliverCaseNotification(ctx, request)
		message = receipt.RenderedMessage
		response = map[string]any{"message_id": receipt.MessageID}
	case snapshotAppealable(item.TemplateSnapshotJSON) && s.dashboard.MemberAppeal(item.GuildID, item.ID) != "":
		response, sendErr = s.messenger.SendCaseNotification(ctx, item.TargetDiscordUserID,
			claimed.PreparedChannelDiscordID, message, s.dashboard.Base(), item.GuildID, item.ID)
	case claimed.PreparedChannelDiscordID != "":
		response, sendErr = s.messenger.SendPreparedDM(ctx, claimed.PreparedChannelDiscordID, message)
	default:
		response, sendErr = s.messenger.SendDM(ctx, item.TargetDiscordUserID, message)
	}

	params := CompleteCaseNotificationParams{
		NotificationID:           claimed.ID,
		LeaseToken:               claimed.LeaseToken,
		WorkerID:                 workerID,
		RenderedMessage:          message,
		PreparedChannelDiscordID: claimed.PreparedChannelDiscordID,
		Status:                   NotificationSent,
		EventType:                CaseEventNotificationSent,
	}
	if sendErr != nil {
		result := resultFromError(sendErr)
		params.Status = NotificationFailed
		params.ErrorCode = result.ErrorCode
		params.ErrorMessage = result.Error
		params.EventType = CaseEventNotificationFailed
	} else if id, ok := response["message_id"].(string); ok {
		params.DeliveryMessageDiscordID = id
	}
	if err := s.store.CompleteCaseNotification(ctx, params); err != nil {
		return fmt.Errorf("record case notification result: %w", err)
	}
	level := slog.LevelInfo
	if sendErr != nil {
		level = slog.LevelWarn
	}
	slog.Log(ctx, level, "Case notification recorded", "case_id", caseID,
		"status", params.Status, "error_code", params.ErrorCode)
	return nil
}

// CaseNotificationSender is implemented by messengers that render the case
// DM themselves. When the ActionService's Messenger implements it, the DM
// goes through DeliverCaseNotification instead of the plain rendering.
type CaseNotificationSender interface {
	// DeliverCaseNotification DMs the member, through the prepared channel
	// when there is one. It returns what it rendered even when sending
	// fails, so the attempt is kept on the case.
	DeliverCaseNotification(ctx context.Context, request CaseNotificationRequest) (CaseNotificationReceipt, error)
}

// CaseNotificationRequest is everything a case DM may show and where to send
// it. It holds only member-visible facts: no staff identities, evidence,
// action configuration, or delivery errors.
type CaseNotificationRequest struct {
	TargetDiscordUserID      string
	PreparedChannelDiscordID string
	GuildID, CaseID          string
	CaseNumber               uint64
	CreatedAt                time.Time
	GuildName                string
	// RuleName and Reason are the template name and official reason frozen
	// in the case snapshot.
	RuleName, Reason string
	ContextValues    []CaseContextValueResponse
	// Introduction and Footer are the guild's notification branding.
	Introduction, Footer string
	Outcomes             []CaseNotificationOutcome
	// Appealable says the template allowed appeals. AppealURL is the
	// dashboard page to appeal from, set only when a dashboard is
	// configured.
	Appealable bool
	AppealURL  string
}

// CaseNotificationOutcome is one enforcement as the member is told about it.
// TimeoutUntil is set only when Discord confirmed the timeout's end.
type CaseNotificationOutcome struct {
	ActionType   ActionType
	Status       ActionExecutionStatus
	TimeoutUntil *time.Time
}

// CaseNotificationReceipt is what DeliverCaseNotification rendered and, on
// success, the DM's message ID.
type CaseNotificationReceipt struct {
	RenderedMessage, MessageID string
}

// caseNotificationRequest gathers the facts for a case DM. Attempts are read
// only to report when a timeout ends; if they cannot be read, the end is
// left out.
func (s *ActionService) caseNotificationRequest(ctx context.Context, item Case, guild *Guild, settings *GuildSettings, actions []CaseActionExecution) CaseNotificationRequest {
	request := CaseNotificationRequest{
		TargetDiscordUserID: item.TargetDiscordUserID,
		GuildID:             item.GuildID,
		CaseID:              item.ID,
		CaseNumber:          item.CaseNumber,
		CreatedAt:           item.CreatedAt,
		RuleName:            snapshotRuleName(item.TemplateSnapshotJSON),
		Reason:              item.Reason,
		ContextValues:       parseContextValues(item.ContextValuesJSON),
		Appealable:          snapshotAppealable(item.TemplateSnapshotJSON),
	}
	if guild != nil {
		request.GuildName = guild.Name
	}
	if settings != nil {
		request.Introduction = settings.NotificationIntroduction
		request.Footer = settings.NotificationFooter
	}
	if request.Appealable {
		request.AppealURL = s.dashboard.MemberAppeal(item.GuildID, item.ID)
	}
	var timeouts []string
	for _, action := range actions {
		if action.endsAt() {
			timeouts = append(timeouts, action.ID)
		}
	}
	var attempts []CaseActionAttempt
	if len(timeouts) > 0 {
		attempts, _ = s.store.ListCaseActionAttempts(ctx, timeouts)
	}
	for _, action := range actions {
		if action.ReversalOfExecutionID != nil {
			continue
		}
		request.Outcomes = append(request.Outcomes, CaseNotificationOutcome{
			ActionType:   action.ActionType,
			Status:       action.Status,
			TimeoutUntil: recordedTimeoutUntil(action, attempts),
		})
	}
	return request
}

// renderCaseNotification writes the member's DM. Guild text is truncated
// and never interpreted, and the whole message fits Discord's 2000
// characters.
func renderCaseNotification(item Case, guild *Guild, settings *GuildSettings, actions []CaseActionExecution) string {
	guildName := "this server"
	if guild != nil && strings.TrimSpace(guild.Name) != "" {
		guildName = guild.Name
	}
	var parts []string
	if settings != nil && strings.TrimSpace(settings.NotificationIntroduction) != "" {
		parts = append(parts, truncateRunes(strings.TrimSpace(settings.NotificationIntroduction), 150))
	}
	parts = append(parts,
		fmt.Sprintf("Moderation case #%d in %s", item.CaseNumber, guildName),
		"Reason: "+truncateRunes(item.Reason, 200))
	for _, value := range parseContextValues(item.ContextValuesJSON) {
		if value.Value != nil {
			parts = append(parts, fmt.Sprintf("%s: %s",
				truncateRunes(value.Label, 40), truncateRunes(fmt.Sprint(value.Value), 50)))
		}
	}
	outcome := "No Discord enforcement action was configured."
	if len(actions) > 0 {
		outcome = fmt.Sprintf("Outcome: %s (%s)", actions[0].ActionType.Label(), actions[0].Status.Label())
	}
	parts = append(parts, outcome)
	if snapshotAppealable(item.TemplateSnapshotJSON) {
		parts = append(parts, "This case can be appealed from your Quack dashboard.")
	}
	if settings != nil && strings.TrimSpace(settings.NotificationFooter) != "" {
		parts = append(parts, truncateRunes(strings.TrimSpace(settings.NotificationFooter), 150))
	}
	return truncateRunes(strings.Join(parts, "\n"), 2000)
}

// redactDiscordError turns an adapter error into text safe to store.
func redactDiscordError(err error) string {
	var discordErr DiscordError
	if errors.As(err, &discordErr) {
		return discordErr.Error()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "Discord request timed out"
	}
	return "Discord request failed"
}
