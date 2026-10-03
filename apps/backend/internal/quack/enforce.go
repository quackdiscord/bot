package quack

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// attemptResult is the outcome of one attempt. An empty Error means
// success.
type attemptResult struct {
	Retryable        bool
	ErrorCode        string
	Error            string
	Response         map[string]any
	OutcomeUncertain bool
}

func permanentFailure(code, message string) attemptResult {
	return attemptResult{ErrorCode: code, Error: message}
}

func retryableFailure(code, message string) attemptResult {
	return attemptResult{Retryable: true, ErrorCode: code, Error: message}
}

// enforce performs one attempt of execution against the case's member in
// Discord.
func (s *ActionService) enforce(ctx context.Context, discordGuildID string, item Case, execution CaseActionExecution, config actionConfig) attemptResult {
	switch execution.ActionType {
	case ActionTimeoutUser, ActionKickUser, ActionBanUser, ActionRemoveTimeout, ActionUnbanUser:
	default:
		return permanentFailure("unsupported_action", fmt.Sprintf("action type %s is not supported", execution.ActionType))
	}
	if s.enforcer == nil {
		return permanentFailure("discord_unavailable", "Discord enforcement is not configured")
	}
	target := item.TargetDiscordUserID
	reason := discordAuditReason(item)
	var response map[string]any
	var err error
	switch execution.ActionType {
	case ActionTimeoutUser:
		if config.DurationSeconds <= 0 {
			return permanentFailure("invalid_action_config", "timeout duration is missing")
		}
		response, err = s.enforcer.TimeoutMember(ctx, discordGuildID, target, config.DurationSeconds, reason)
	case ActionKickUser:
		response, err = s.enforcer.KickMember(ctx, discordGuildID, target, reason)
	case ActionBanUser:
		response, err = s.enforcer.BanMember(ctx, discordGuildID, target, config.DeleteMessageSeconds, reason)
	case ActionRemoveTimeout:
		response, err = s.enforcer.RemoveMemberTimeout(ctx, discordGuildID, target, reason)
	case ActionUnbanUser:
		response, err = s.enforcer.UnbanMember(ctx, discordGuildID, target, reason)
	}
	if err != nil {
		return resultFromError(err)
	}
	return attemptResult{Response: response}
}

// resultFromError classifies a Discord error. Anything the adapter did not
// classify counts as uncertain, since Discord may have applied the request
// before the error, and its message is replaced so raw responses never reach
// storage.
func resultFromError(err error) attemptResult {
	var discordErr DiscordError
	if errors.As(err, &discordErr) {
		return attemptResult{
			Retryable:        discordErr.Retryable,
			ErrorCode:        discordErr.Code,
			Error:            discordErr.Error(),
			OutcomeUncertain: discordErr.OutcomeUncertain,
		}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return attemptResult{ErrorCode: "context_cancelled", Error: "Discord request was interrupted", OutcomeUncertain: true}
	}
	return attemptResult{ErrorCode: "discord_error", Error: "Discord request failed", OutcomeUncertain: true}
}

// discordAuditReason is the reason shown in the guild's Discord audit log,
// cut to Discord's 512-character limit.
func discordAuditReason(item Case) string {
	return truncateRunes(fmt.Sprintf("Quack case #%d: %s", item.CaseNumber, strings.TrimSpace(item.Reason)), 512)
}
