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
	if err := s.store.BeginCaseNotificationDelivery(ctx, claimed.ID, claimed.LeaseToken); err != nil {
		return err
	}

	var response map[string]any
	var sendErr error
	switch {
	case s.messenger == nil:
		sendErr = errors.New("discord messenger is not configured")
	case snapshotAppealable(item.TemplateSnapshotJSON) && s.dashboardBaseURL != "":
		response, sendErr = s.messenger.SendCaseNotification(ctx, item.TargetDiscordUserID,
			claimed.PreparedChannelDiscordID, message, s.dashboardBaseURL, item.GuildID, item.ID)
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

// AppealNotificationDispatcher delivers the appeal notification outbox.
// Rows are written in the same transaction as the appeal change, so a
// notification is never lost even if Discord is down when it happens.
type AppealNotificationDispatcher struct {
	store  AppealNotificationStore
	client AppealNotifier
}

// NewAppealNotificationDispatcher returns a dispatcher that sends through
// client.
func NewAppealNotificationDispatcher(store AppealNotificationStore, client AppealNotifier) *AppealNotificationDispatcher {
	return &AppealNotificationDispatcher{store: store, client: client}
}

// DispatchPending sends up to limit pending notifications and records each
// outcome. limit must be between 1 and 100.
func (d *AppealNotificationDispatcher) DispatchPending(ctx context.Context, limit int) error {
	if limit < 1 || limit > 100 {
		return errors.New("appeal notification limit is invalid")
	}
	items, err := d.store.ClaimPendingAppealNotifications(ctx, limit)
	if err != nil {
		return err
	}
	for _, item := range items {
		var messageID string
		var sendErr error
		switch item.Audience {
		case AppealNotificationMember:
			messageID, sendErr = d.client.SendAppealMemberNotification(ctx, item.TargetDiscordUserID, item.Body)
		case AppealNotificationStaff:
			messageID, sendErr = d.client.SendAppealStaffNotification(ctx, item.GuildID, item.Body)
		default:
			sendErr = errors.New("appeal notification audience is invalid")
		}
		params := CompleteAppealNotificationParams{
			NotificationID:    item.ID,
			LeaseToken:        item.LeaseToken,
			DeliveryMessageID: messageID,
			Status:            AppealNotificationSent,
		}
		if sendErr != nil {
			params.Status = AppealNotificationFailed
			params.ErrorCode = appealNotificationErrorCode(sendErr)
		}
		if err := d.store.CompleteAppealNotification(ctx, params); err != nil {
			return err
		}
	}
	return nil
}

// appealNotificationErrorCode reduces a delivery error to a coarse code so
// no Discord response text is stored. It reads the adapter's DiscordError
// classification, never the error text.
func appealNotificationErrorCode(err error) string {
	var discordErr DiscordError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "discord_timeout"
	case !errors.As(err, &discordErr):
		return "discord_delivery_failed"
	case discordErr.HasFailure(DiscordFailurePermissionDenied):
		return "discord_forbidden"
	case discordErr.HasFailure(DiscordFailureRateLimited):
		return "discord_rate_limited"
	default:
		return "discord_delivery_failed"
	}
}
