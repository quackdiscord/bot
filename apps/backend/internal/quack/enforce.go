package quack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// enforcement is one execution about to be attempted.
type enforcement struct {
	Case           Case
	Execution      CaseActionExecution
	Config         map[string]any
	DiscordGuildID string
}

// attemptResult is the outcome of one attempt. A zero ErrorCode means
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

// resultFromError classifies a Discord error. Anything the adapter did not
// classify counts as uncertain, since Discord may have applied the request
// before the error, and its message is replaced so raw responses never reach
// storage.
func resultFromError(err error) attemptResult {
	if err == nil {
		return attemptResult{}
	}
	var discordErr DiscordError
	if errors.As(err, &discordErr) {
		result := permanentFailure(discordErr.Code, discordErr.Error())
		result.Retryable = discordErr.Retryable
		result.OutcomeUncertain = discordErr.OutcomeUncertain
		return result
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return attemptResult{ErrorCode: "context_cancelled", Error: "Discord request was interrupted", OutcomeUncertain: true}
	}
	return attemptResult{ErrorCode: "discord_error", Error: "Discord request failed", OutcomeUncertain: true}
}

// enforce performs one attempt of e in Discord.
func (s *ActionService) enforce(ctx context.Context, e enforcement) attemptResult {
	target := e.Case.TargetDiscordUserID
	if e.Execution.ActionType == ActionSendDM {
		if s.messenger == nil {
			return permanentFailure("discord_unavailable", "discord action client is not configured")
		}
		message := configString(e.Config, "message")
		if message == "" {
			message = fmt.Sprintf("You received a moderation case in this server: %s", e.Case.Reason)
		}
		return resultFrom(s.messenger.SendDM(ctx, target, message))
	}

	switch e.Execution.ActionType {
	case ActionTimeoutUser, ActionKickUser, ActionBanUser, ActionRemoveTimeout, ActionUnbanUser:
	default:
		return permanentFailure("unsupported_action", fmt.Sprintf("action type %s is not supported", e.Execution.ActionType))
	}
	if s.enforcer == nil {
		return permanentFailure("discord_unavailable", "Discord enforcement is not configured")
	}
	reason := discordAuditReason(e.Case)
	switch e.Execution.ActionType {
	case ActionTimeoutUser:
		duration := configInt(e.Config, "duration_seconds")
		if duration <= 0 {
			return permanentFailure("invalid_action_config", "timeout duration is missing")
		}
		return resultFrom(s.enforcer.TimeoutMember(ctx, e.DiscordGuildID, target, duration, reason))
	case ActionKickUser:
		return resultFrom(s.enforcer.KickMember(ctx, e.DiscordGuildID, target, reason))
	case ActionBanUser:
		return resultFrom(s.enforcer.BanMember(ctx, e.DiscordGuildID, target, configInt(e.Config, "delete_message_seconds"), reason))
	case ActionRemoveTimeout:
		return resultFrom(s.enforcer.RemoveMemberTimeout(ctx, e.DiscordGuildID, target, reason))
	default:
		return resultFrom(s.enforcer.UnbanMember(ctx, e.DiscordGuildID, target, reason))
	}
}

func resultFrom(response map[string]any, err error) attemptResult {
	if err != nil {
		return resultFromError(err)
	}
	return attemptResult{Response: response}
}

// discordAuditReason is the reason shown in the guild's Discord audit log,
// cut to Discord's 512-character limit.
func discordAuditReason(item Case) string {
	return truncateRunes(fmt.Sprintf("Quack case #%d: %s", item.CaseNumber, strings.TrimSpace(item.Reason)), 512)
}

// parseActionConfig decodes an execution's configuration, treating missing
// or malformed JSON as empty.
func parseActionConfig(body string) map[string]any {
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
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

// configInt reads an integer setting. Fractional, infinite, and
// out-of-range values read as 0 so they fail validation instead of being
// rounded into a real duration.
func configInt(config map[string]any, key string) int {
	value, ok := config[key]
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case float64:
		bound := math.Ldexp(1, strconv.IntSize-1)
		if math.IsNaN(typed) || math.IsInf(typed, 0) || math.Trunc(typed) != typed || typed < -bound || typed >= bound {
			return 0
		}
		return int(typed)
	case int:
		return typed
	case json.Number:
		parsed, _ := strconv.Atoi(typed.String())
		return parsed
	default:
		parsed, _ := strconv.Atoi(strings.TrimSpace(fmt.Sprint(typed)))
		return parsed
	}
}
