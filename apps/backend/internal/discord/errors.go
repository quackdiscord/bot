package discord

import (
	"errors"
	"net/http"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

// classify turns a failed REST call into a quack.DiscordError whose code is
// prefixed with operation. The message never includes Discord's response
// body. For irreversible operations a server or network error may have been
// applied anyway, so it is reported as uncertain instead of retryable.
func classify(operation string, err error, irreversible bool) error {
	var rateLimit *discordgo.RateLimitError
	if errors.As(err, &rateLimit) {
		return quack.DiscordError{Code: operation + "_rate_limited", Message: "Discord rate limit reached", Retryable: true}
	}
	var restErr *discordgo.RESTError
	if !errors.As(err, &restErr) || restErr.Response == nil {
		return quack.DiscordError{
			Code:             operation + "_network_error",
			Message:          "Discord request failed",
			Retryable:        !irreversible,
			OutcomeUncertain: irreversible,
		}
	}
	status := restErr.Response.StatusCode
	code, retryable, uncertain := "discord_failure", false, false
	switch {
	case status == http.StatusBadRequest:
		code = "validation_failed"
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		code = "permission_or_hierarchy_denied"
	case status == http.StatusNotFound:
		code = "unknown_member_or_resource"
	case status == http.StatusTooManyRequests:
		code, retryable = "rate_limited", true
	case status >= 500:
		code, retryable, uncertain = "discord_server_error", !irreversible, irreversible
	}
	return quack.DiscordError{
		Code:             operation + "_" + code,
		Message:          "Discord rejected the moderation request",
		Retryable:        retryable,
		OutcomeUncertain: uncertain,
	}
}

// statusCode returns the HTTP status of a Discord REST error, or 0.
func statusCode(err error) int {
	var restErr *discordgo.RESTError
	if errors.As(err, &restErr) && restErr.Response != nil {
		return restErr.Response.StatusCode
	}
	return 0
}
