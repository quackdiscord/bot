// Package discordtext writes Quack's Discord prose without depending on
// Discord's transport types: icon placeholders, Markdown escaping, quoting,
// and the conversation layout every message shares. The sending adapter
// resolves placeholders for its own application just before sending, because
// each bot identity uploads its own custom emoji.
package discordtext

//go:generate node ../../../../assets/icons/quack/generate-catalog.mjs

import (
	"regexp"
	"strings"

	"github.com/quackdiscord/bot/internal/quack"
)

// iconPattern matches only Quack's own placeholders, never arbitrary emoji.
var iconPattern = regexp.MustCompile(`\{\{quack:([a-z_]+)\}\}`)

// Icon returns the placeholder for the icon named key. The emoji is chosen
// later by Resolve, once the sending application is known.
func Icon(key string) string { return "{{quack:" + key + "}}" }

// Resolve replaces icon placeholders with applicationID's uploaded emoji. An
// application without uploads, or an unknown key, drops the placeholder so
// the prose still reads cleanly.
func Resolve(content, applicationID string) string {
	return strings.TrimSpace(iconPattern.ReplaceAllStringFunc(content, func(marker string) string {
		key := strings.TrimSuffix(strings.TrimPrefix(marker, "{{quack:"), "}}")
		return applicationIcons[applicationID][key]
	}))
}

// Plain escapes member-controlled text before it goes into Quack's Markdown,
// including the braces of a forged icon placeholder. Mentions are disabled
// separately by the sending adapter.
func Plain(value string) string {
	return plainReplacer.Replace(value)
}

// plainReplacer escapes every character Discord Markdown or Resolve treats
// as syntax.
var plainReplacer = strings.NewReplacer(
	"\\", "\\\\", "*", "\\*", "_", "\\_", "`", "\\`", "~", "\\~", "|", "\\|", ">", "\\>",
	"[", "\\[", "]", "\\]", "#", "\\#", "{", "\\{", "}", "\\}",
)

// Quote keeps every line of already-escaped text inside one block quote. It
// returns "" for blank text.
func Quote(value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return "> " + strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\n", "\n> ")
}

// Conversation is the layout of every Quack message: an icon and a lead
// sentence with the outcome, an optional quote of the context, the details
// or next steps, and a quiet subtext line of references. Empty parts are
// left out. Callers escape member-controlled values with Plain.
func Conversation(icon, lead, quote, detail, meta string) string {
	parts := []string{strings.TrimSpace(Icon(icon) + " " + lead)}
	if quote = Quote(quote); quote != "" {
		parts = append(parts, quote)
	}
	if strings.TrimSpace(detail) != "" {
		parts = append(parts, detail)
	}
	body := strings.Join(parts, "\n\n")
	if strings.TrimSpace(meta) != "" {
		body += "\n-# " + strings.ReplaceAll(meta, "\n", " · ")
	}
	return body
}

// WithIcon prefixes body with the icon for key, unless body already starts
// with an icon, so a rendered Conversation is never decorated twice.
func WithIcon(key, body string) string {
	if strings.HasPrefix(body, "{{quack:") {
		return body
	}
	return Icon(key) + " " + body
}

// ActionSentence describes an action's progress to members and staff in a
// sentence, without internal status names.
func ActionSentence(action quack.ActionType, status quack.ActionExecutionStatus) string {
	name := action.Label()
	switch status {
	case quack.ActionExecutionSucceeded:
		switch action {
		case quack.ActionTimeoutUser:
			return "Timeout applied."
		case quack.ActionKickUser:
			return "Member kicked."
		case quack.ActionBanUser:
			return "Member banned."
		case quack.ActionRemoveTimeout:
			return "Member is no longer timed out."
		case quack.ActionUnbanUser:
			return "Member is no longer banned."
		case quack.ActionSendDM:
			return "Message sent."
		}
		return name + " completed."
	case quack.ActionExecutionPending:
		return name + " queued."
	case quack.ActionExecutionRunning:
		return name + " in progress."
	case quack.ActionExecutionRetrying:
		return name + " will be retried."
	case quack.ActionExecutionFailed:
		return name + " couldn’t be completed. Staff review needed."
	case quack.ActionExecutionCancelled:
		return name + " cancelled."
	default:
		return name + " status is unavailable."
	}
}
