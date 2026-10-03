package discordbot

import (
	"errors"
	"net/http"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

// classifyDiscordError encapsulates the classify discord error rule so callers share one consistent package implementation.
func classifyDiscordError(code string, err error) error {
	return classifyDiscordOperation(code, err, false)
}

// classifyDiscordOperation produces redacted retry and ambiguity semantics for persisted attempts.
func classifyDiscordOperation(operation string, err error, irreversible bool) error {
	var rateLimit *discordgo.RateLimitError
	if errors.As(err, &rateLimit) {
		return quack.DiscordError{Code: operation + "_rate_limited", Message: "Discord rate limit reached", Retryable: true}
	}
	var restError *discordgo.RESTError
	if errors.As(err, &restError) && restError.Response != nil {
		status := restError.Response.StatusCode
		code := "discord_failure"
		retryable := false
		uncertain := false
		switch {
		case status == http.StatusBadRequest:
			code = "validation_failed"
		case status == http.StatusUnauthorized || status == http.StatusForbidden:
			code = "permission_or_hierarchy_denied"
		case status == http.StatusNotFound:
			code = "unknown_member_or_resource"
		case status == http.StatusTooManyRequests:
			code = "rate_limited"
			retryable = true
		case status >= 500:
			code = "discord_server_error"
			retryable = !irreversible
			uncertain = irreversible
		}
		return quack.DiscordError{Code: operation + "_" + code, Message: "Discord rejected the moderation request", Retryable: retryable, OutcomeUncertain: uncertain}
	}
	return quack.DiscordError{Code: operation + "_network_error", Message: "Discord request failed", Retryable: !irreversible, OutcomeUncertain: irreversible}
}
