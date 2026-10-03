package discord

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

// Discord's message limits, enforced before sending so a long value is cut
// instead of rejected.
const (
	embedTitleLimit       = 256
	embedDescriptionLimit = 4096
	embedFieldNameLimit   = 256
	embedFieldValueLimit  = 1024
	embedFieldLimit       = 25
	embedFooterLimit      = 2048
	customIDLimit         = 100
)

// Embed colors, taken from Discord's own palette.
const (
	colorMain    = 0x5865F2
	colorSuccess = 0x57F287
	colorWarning = 0xFEE75C
	colorError   = 0xED4245
)

// blankField stands in for an empty embed field name or value, which Discord
// rejects. A zero-width space renders as nothing.
const blankField = "\u200b"

// Message is a message Quack sends, either as an interaction response or as
// a followup. Mentions are suppressed unless AllowedMentions says otherwise.
type Message struct {
	Content         string
	Embeds          []*discordgo.MessageEmbed
	Components      []discordgo.MessageComponent
	Files           []*discordgo.File
	Ephemeral       bool
	AllowedMentions *discordgo.MessageAllowedMentions
}

// Edit changes an interaction response that was already sent. Nil fields
// are left as they are.
type Edit struct {
	Content         *string
	Embeds          *[]*discordgo.MessageEmbed
	Components      *[]discordgo.MessageComponent
	Files           []*discordgo.File
	AllowedMentions *discordgo.MessageAllowedMentions
}

// embedBuilder builds an embed while enforcing Discord's limits.
type embedBuilder struct {
	embed *discordgo.MessageEmbed
}

// Content returns a text-only message.
func Content(content string, ephemeral bool) Message {
	return Message{Content: content, Ephemeral: ephemeral}
}

// EditMessage returns an edit that replaces a response with m entirely.
func EditMessage(m Message) Edit {
	return Edit{
		Content:         &m.Content,
		Embeds:          &m.Embeds,
		Components:      &m.Components,
		Files:           m.Files,
		AllowedMentions: m.AllowedMentions,
	}
}

// Ephemeral answers with a message only the invoking user can see.
func Ephemeral(m Message) *discordgo.InteractionResponse {
	m.Ephemeral = true
	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: m.responseData(),
	}
}

// DeferEphemeral acknowledges with a private "thinking" state that the task
// later edits.
func DeferEphemeral() *discordgo.InteractionResponse {
	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
	}
}

// DeferUpdate acknowledges a component without changing its message yet.
func DeferUpdate() *discordgo.InteractionResponse {
	return &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredMessageUpdate}
}

// Modal opens a form whose submission is routed by customID.
func Modal(title, customID string, components []discordgo.MessageComponent) *discordgo.InteractionResponse {
	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{Title: title, CustomID: customID, Components: components},
	}
}

// Error answers privately with Quack's standard error embed.
func Error(content string) *discordgo.InteractionResponse {
	return Ephemeral(embedMessage(errorEmbed(content), true))
}

// ErrorEdit replaces a deferred response with Quack's standard error embed.
func ErrorEdit(content string) Edit {
	return EditMessage(embedMessage(errorEmbed(content), false))
}

// Publish posts message publicly after a private deferred acknowledgement.
// Discord treats the first followup to a deferred response as an edit of it,
// which would keep the result private, so Publish first completes the
// acknowledgement, then posts the result, then deletes the acknowledgement.
// A failure before the result exists leaves the acknowledgement in place.
func Publish(responder Responder, message Message) (*discordgo.Message, error) {
	if _, err := responder.EditOriginal(EditMessage(Content("Posting result…", true))); err != nil {
		return nil, err
	}
	message.Ephemeral = false
	published, err := responder.Followup(message)
	if err != nil {
		return nil, err
	}
	if err := responder.DeleteOriginal(); err != nil {
		slog.Warn("Could not remove private interaction acknowledgement", "error_type", "discord_response")
	}
	return published, nil
}

// Truncate cuts value to limit runes without splitting a character.
func Truncate(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

// Button returns an interactive button routed by customID.
func Button(customID, label string, style discordgo.ButtonStyle, disabled bool) discordgo.Button {
	return discordgo.Button{CustomID: customID, Label: Truncate(label, 80), Style: style, Disabled: disabled}
}

// Row puts up to five components in one action row; extras are dropped.
func Row(components ...discordgo.MessageComponent) discordgo.ActionsRow {
	if len(components) > 5 {
		components = components[:5]
	}
	return discordgo.ActionsRow{Components: components}
}

// ModalValue returns the value of the text input named customID in a modal
// submission. discordgo decodes rows as pointers, but tests and older
// payloads use values, so both are accepted.
func ModalValue(data discordgo.ModalSubmitInteractionData, customID string) string {
	for _, component := range data.Components {
		var row *discordgo.ActionsRow
		switch value := component.(type) {
		case *discordgo.ActionsRow:
			row = value
		case discordgo.ActionsRow:
			row = &value
		default:
			continue
		}
		for _, child := range row.Components {
			switch input := child.(type) {
			case *discordgo.TextInput:
				if input.CustomID == customID {
					return input.Value
				}
			case discordgo.TextInput:
				if input.CustomID == customID {
					return input.Value
				}
			}
		}
	}
	return ""
}

// responseData converts m into an interaction response body.
func (m Message) responseData() *discordgo.InteractionResponseData {
	data := &discordgo.InteractionResponseData{
		Content:         m.Content,
		Embeds:          m.Embeds,
		Components:      m.Components,
		AllowedMentions: noMentions(m.AllowedMentions),
	}
	if m.Ephemeral {
		data.Flags = discordgo.MessageFlagsEphemeral
	}
	return data
}

// webhookParams converts m into a followup message body.
func (m Message) webhookParams() *discordgo.WebhookParams {
	params := &discordgo.WebhookParams{
		Content:         m.Content,
		Embeds:          m.Embeds,
		Components:      m.Components,
		Files:           m.Files,
		AllowedMentions: noMentions(m.AllowedMentions),
	}
	if m.Ephemeral {
		params.Flags = discordgo.MessageFlagsEphemeral
	}
	return params
}

// webhookEdit converts e into a webhook message edit.
func (e Edit) webhookEdit() *discordgo.WebhookEdit {
	return &discordgo.WebhookEdit{
		Content:         e.Content,
		Embeds:          e.Embeds,
		Components:      e.Components,
		Files:           e.Files,
		AllowedMentions: noMentions(e.AllowedMentions),
	}
}

// newEmbed starts an embed with a trimmed, truncated title and description.
func newEmbed(title, description string, color int) *embedBuilder {
	return &embedBuilder{embed: &discordgo.MessageEmbed{
		Title:       Truncate(strings.TrimSpace(title), embedTitleLimit),
		Description: Truncate(description, embedDescriptionLimit),
		Color:       color,
	}}
}

// field appends a field, dropping it past Discord's field limit and filling
// blank names or values with blankField.
func (b *embedBuilder) field(name string, value any, inline bool) *embedBuilder {
	if len(b.embed.Fields) >= embedFieldLimit {
		return b
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = blankField
	}
	text := fmt.Sprint(value)
	if strings.TrimSpace(text) == "" {
		text = blankField
	}
	b.embed.Fields = append(b.embed.Fields, &discordgo.MessageEmbedField{
		Name:   Truncate(name, embedFieldNameLimit),
		Value:  Truncate(text, embedFieldValueLimit),
		Inline: inline,
	})
	return b
}

// footer sets the footer text.
func (b *embedBuilder) footer(text string) *embedBuilder {
	b.embed.Footer = &discordgo.MessageEmbedFooter{Text: Truncate(text, embedFooterLimit)}
	return b
}

// stamp sets the timestamp to now.
func (b *embedBuilder) stamp() *embedBuilder {
	b.embed.Timestamp = time.Now().UTC().Format(time.RFC3339)
	return b
}

// build returns the finished embed.
func (b *embedBuilder) build() *discordgo.MessageEmbed { return b.embed }

// embedMessage wraps a single embed in a message.
func embedMessage(embed *discordgo.MessageEmbed, ephemeral bool) Message {
	return Message{Embeds: []*discordgo.MessageEmbed{embed}, Ephemeral: ephemeral}
}

// errorEmbed is Quack's standard error embed.
func errorEmbed(description string) *discordgo.MessageEmbed {
	return newEmbed("Error", description, colorError).stamp().build()
}

// warningEmbed is Quack's standard warning embed.
func warningEmbed(title, description string) *discordgo.MessageEmbed {
	return newEmbed(title, description, colorWarning).stamp().build()
}

// noMentions returns allowed, or an empty allow list that suppresses every
// mention when the message did not set one.
func noMentions(allowed *discordgo.MessageAllowedMentions) *discordgo.MessageAllowedMentions {
	if allowed == nil {
		return &discordgo.MessageAllowedMentions{}
	}
	return allowed
}

// autocomplete answers an autocomplete request with choices.
func autocomplete(choices []*discordgo.ApplicationCommandOptionChoice) *discordgo.InteractionResponse {
	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionApplicationCommandAutocompleteResult,
		Data: &discordgo.InteractionResponseData{Choices: choices},
	}
}

// linkButton returns a button that opens url.
func linkButton(url, label string) discordgo.Button {
	return discordgo.Button{URL: url, Label: Truncate(label, 80), Style: discordgo.LinkButton}
}

// pagination returns Prev and Next buttons routed to namespace:prefix_prev
// and namespace:prefix_next, disabled at either end.
func pagination(namespace, prefix, payload string, page, totalPages int) ([]discordgo.MessageComponent, error) {
	prevID, err := EncodeCustomID(CustomID{Namespace: namespace, Action: prefix + "_prev", Version: "v1", Payload: payload})
	if err != nil {
		return nil, err
	}
	nextID, err := EncodeCustomID(CustomID{Namespace: namespace, Action: prefix + "_next", Version: "v1", Payload: payload})
	if err != nil {
		return nil, err
	}
	return []discordgo.MessageComponent{Row(
		Button(prevID, "Prev", discordgo.SecondaryButton, page <= 1),
		Button(nextID, "Next", discordgo.PrimaryButton, page >= totalPages),
	)}, nil
}
