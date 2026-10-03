package discord

import (
	"errors"
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

const (
	colorMain    = 0x5865F2
	colorSuccess = 0x57F287
	colorWarning = 0xFEE75C
	colorError   = 0xED4245
)

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

func embedMessage(embed *discordgo.MessageEmbed, ephemeral bool) Message {
	return Message{Embeds: []*discordgo.MessageEmbed{embed}, Ephemeral: ephemeral}
}

func noMentions(allowed *discordgo.MessageAllowedMentions) *discordgo.MessageAllowedMentions {
	if allowed == nil {
		return &discordgo.MessageAllowedMentions{}
	}
	return allowed
}

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

func (e Edit) webhookEdit() *discordgo.WebhookEdit {
	return &discordgo.WebhookEdit{
		Content:         e.Content,
		Embeds:          e.Embeds,
		Components:      e.Components,
		Files:           e.Files,
		AllowedMentions: noMentions(e.AllowedMentions),
	}
}

// Ephemeral answers with a message only the invoking user can see.
func Ephemeral(m Message) *discordgo.InteractionResponse {
	m.Ephemeral = true
	return &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource, Data: m.responseData()}
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

func autocomplete(choices []*discordgo.ApplicationCommandOptionChoice) *discordgo.InteractionResponse {
	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionApplicationCommandAutocompleteResult,
		Data: &discordgo.InteractionResponseData{Choices: choices},
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

// embed builds an embed while enforcing Discord's limits.
type embed struct{ e *discordgo.MessageEmbed }

func newEmbed(title, description string, color int) *embed {
	return &embed{e: &discordgo.MessageEmbed{
		Title:       Truncate(strings.TrimSpace(title), embedTitleLimit),
		Description: Truncate(description, embedDescriptionLimit),
		Color:       color,
	}}
}

func errorEmbed(description string) *discordgo.MessageEmbed {
	return newEmbed("Error", description, colorError).stamp().build()
}

func warningEmbed(title, description string) *discordgo.MessageEmbed {
	return newEmbed(title, description, colorWarning).stamp().build()
}

// field appends a field, dropping it past Discord's field limit and filling
// blank names or values with a zero-width space Discord accepts.
func (b *embed) field(name string, value any, inline bool) *embed {
	if len(b.e.Fields) >= embedFieldLimit {
		return b
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "​"
	}
	text := fmt.Sprint(value)
	if strings.TrimSpace(text) == "" {
		text = "​"
	}
	b.e.Fields = append(b.e.Fields, &discordgo.MessageEmbedField{
		Name:   Truncate(name, embedFieldNameLimit),
		Value:  Truncate(text, embedFieldValueLimit),
		Inline: inline,
	})
	return b
}

func (b *embed) footer(text string) *embed {
	b.e.Footer = &discordgo.MessageEmbedFooter{Text: Truncate(text, embedFooterLimit)}
	return b
}

func (b *embed) stamp() *embed {
	b.e.Timestamp = time.Now().UTC().Format(time.RFC3339)
	return b
}

func (b *embed) build() *discordgo.MessageEmbed { return b.e }

// Custom ID errors.
var (
	ErrCustomIDInvalid = errors.New("custom id is invalid")
	ErrCustomIDTooLong = errors.New("custom id exceeds Discord limit")
)

// CustomID is the routing identity Quack puts in a button, select menu, or
// modal: "namespace:action:version:payload". The router dispatches on
// namespace and action; payload carries the IDs the handler needs.
type CustomID struct {
	Namespace string
	Action    string
	Version   string
	Payload   string
}

// EncodeCustomID formats id, rejecting missing parts, separators inside the
// routing parts, and values over Discord's 100-character limit.
func EncodeCustomID(id CustomID) (string, error) {
	namespace := strings.TrimSpace(id.Namespace)
	action := strings.TrimSpace(id.Action)
	version := strings.TrimSpace(id.Version)
	if namespace == "" || action == "" || version == "" {
		return "", ErrCustomIDInvalid
	}
	if strings.Contains(namespace, ":") || strings.Contains(action, ":") || strings.Contains(version, ":") {
		return "", ErrCustomIDInvalid
	}
	value := namespace + ":" + action + ":" + version + ":" + strings.TrimSpace(id.Payload)
	if len([]rune(value)) > customIDLimit {
		return "", ErrCustomIDTooLong
	}
	return value, nil
}

// DecodeCustomID parses a custom ID produced by EncodeCustomID.
func DecodeCustomID(value string) (CustomID, error) {
	parts := strings.SplitN(strings.TrimSpace(value), ":", 4)
	if len(parts) != 4 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return CustomID{}, ErrCustomIDInvalid
	}
	if len([]rune(value)) > customIDLimit {
		return CustomID{}, ErrCustomIDTooLong
	}
	return CustomID{Namespace: parts[0], Action: parts[1], Version: parts[2], Payload: parts[3]}, nil
}

// MustCustomID is EncodeCustomID for IDs built from code, where an invalid
// ID is a programming error.
func MustCustomID(id CustomID) string {
	value, err := EncodeCustomID(id)
	if err != nil {
		panic(fmt.Sprintf("invalid custom id: %v", err))
	}
	return value
}

// Button returns an interactive button routed by customID.
func Button(customID, label string, style discordgo.ButtonStyle, disabled bool) discordgo.Button {
	return discordgo.Button{CustomID: customID, Label: Truncate(label, 80), Style: style, Disabled: disabled}
}

func linkButton(url, label string) discordgo.Button {
	return discordgo.Button{URL: url, Label: Truncate(label, 80), Style: discordgo.LinkButton}
}

// Row puts up to five components in one action row; extras are dropped.
func Row(components ...discordgo.MessageComponent) discordgo.ActionsRow {
	if len(components) > 5 {
		components = components[:5]
	}
	return discordgo.ActionsRow{Components: components}
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
