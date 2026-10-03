package discord

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordtext"
	"github.com/quackdiscord/bot/internal/quack"
)

// Discord's limits, enforced before sending so a long value is cut or
// attached instead of rejected.
const (
	contentLimit  = 2000
	customIDLimit = 100

	// contentBudget is what ForApplication keeps inline when a message is
	// too long, leaving room for the note that the rest is attached.
	contentBudget = 1750
)

// Message is a message Quack sends: an interaction response, a followup, a
// channel post, or a DM. Content may hold discordtext icon placeholders and
// command references such as "/case view"; both are resolved for the
// sending application just before the message leaves. Mentions are
// suppressed unless AllowedMentions says otherwise.
type Message struct {
	Content         string
	Embeds          []*discordgo.MessageEmbed
	Components      []discordgo.MessageComponent
	Files           []*discordgo.File
	Ephemeral       bool
	AllowedMentions *discordgo.MessageAllowedMentions
}

// Edit changes a message Quack already sent. Nil fields are left as they
// are.
type Edit struct {
	// PrivateError marks an error edit. Under AsyncPublic it is delivered
	// privately to the invoking user instead of replacing the public reply.
	PrivateError    bool
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

// Conversation returns a message in Quack's standard layout; see
// discordtext.Conversation. Callers escape member-controlled values with
// PlainText.
func Conversation(icon, lead, quote, detail, meta string, ephemeral bool) Message {
	return Content(discordtext.Conversation(icon, lead, quote, detail, meta), ephemeral)
}

// Signal returns a short message behind the icon for key, adding the icon
// only if body does not already start with one.
func Signal(icon, body string, ephemeral bool) Message {
	return Content(discordtext.WithIcon(icon, body), ephemeral)
}

// Quote block-quotes already-escaped text.
func Quote(body string) string { return discordtext.Quote(body) }

// PlainText escapes member-controlled text before it goes into Quack's
// Markdown.
func PlainText(value string) string { return discordtext.Plain(value) }

// RelativeTime renders value as Discord's live, localized "3 days ago"
// timestamp, or "" for the zero time.
func RelativeTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return fmt.Sprintf("<t:%d:R>", value.Unix())
}

// ActionSentence describes an action's progress in a sentence; see
// discordtext.ActionSentence.
func ActionSentence(action quack.ActionType, status quack.ActionExecutionStatus) string {
	return discordtext.ActionSentence(action, status)
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

// EditMessage returns an edit that replaces a message with m entirely:
// content, embeds, and components are all set, so old ones are cleared.
func EditMessage(m Message) Edit {
	content := m.Content
	embeds := append([]*discordgo.MessageEmbed{}, m.Embeds...)
	components := append([]discordgo.MessageComponent{}, m.Components...)
	return Edit{
		Content:         &content,
		Embeds:          &embeds,
		Components:      &components,
		Files:           m.Files,
		AllowedMentions: m.AllowedMentions,
	}
}

// ForApplication resolves icons and command mentions for applicationID.
// Content that would still exceed Discord's limit keeps its leading whole
// paragraphs inline and attaches the full text as message.txt, so a long
// staff record is never cut off. m itself is not changed.
func (m Message) ForApplication(applicationID string) Message {
	m.Content = ResolveCommandMentions(discordtext.Resolve(m.Content, applicationID), applicationID)
	if utf16Len(m.Content) <= contentLimit {
		return m
	}
	full := m.Content
	// Whole paragraphs only, so a link, quote, or emoji is never cut.
	var kept []string
	for _, paragraph := range strings.Split(full, "\n\n") {
		if utf16Len(strings.Join(append(kept, paragraph), "\n\n")) > contentBudget {
			break
		}
		kept = append(kept, paragraph)
	}
	m.Content = strings.Join(kept, "\n\n")
	if m.Content == "" {
		m.Content = "The full message is attached."
	} else {
		m.Content += "\n\n-# Full details are attached."
	}
	m.Files = append(append([]*discordgo.File(nil), m.Files...), &discordgo.File{
		Name:        "message.txt",
		ContentType: "text/plain; charset=utf-8",
		Reader:      strings.NewReader(full),
	})
	return m
}

// ForApplication resolves an edit's content like Message.ForApplication,
// without changing which fields it replaces.
func (e Edit) ForApplication(applicationID string) Edit {
	if e.Content == nil {
		return e
	}
	m := Message{Content: *e.Content, Files: e.Files}.ForApplication(applicationID)
	e.Content, e.Files = &m.Content, m.Files
	return e
}

// PrepareResponse resolves a message response for applicationID. Modals,
// autocomplete answers, and deferred acknowledgements pass through as they
// are. response itself is not changed.
func PrepareResponse(response *discordgo.InteractionResponse, applicationID string) *discordgo.InteractionResponse {
	if response == nil || response.Data == nil {
		return response
	}
	if response.Type != discordgo.InteractionResponseChannelMessageWithSource &&
		response.Type != discordgo.InteractionResponseUpdateMessage {
		return response
	}
	result, data := *response, *response.Data
	m := Message{Content: data.Content, Files: data.Files}.ForApplication(applicationID)
	data.Content, data.Files = m.Content, m.Files
	if len(data.Embeds) == 0 {
		data.Flags |= discordgo.MessageFlagsSuppressEmbeds
	}
	data.AllowedMentions = noMentions(data.AllowedMentions)
	result.Data = &data
	return &result
}

// responseData converts m into an interaction response body.
func (m Message) responseData() *discordgo.InteractionResponseData {
	data := &discordgo.InteractionResponseData{
		Content:         m.Content,
		Embeds:          m.Embeds,
		Components:      m.Components,
		Files:           m.Files,
		AllowedMentions: noMentions(m.AllowedMentions),
	}
	if m.Ephemeral {
		data.Flags = discordgo.MessageFlagsEphemeral
	}
	return data
}

// webhookParams converts m into a followup message body.
func (m Message) webhookParams() *discordgo.WebhookParams {
	return &discordgo.WebhookParams{
		Content:         m.Content,
		Embeds:          m.Embeds,
		Components:      m.Components,
		Files:           m.Files,
		AllowedMentions: noMentions(m.AllowedMentions),
		Flags:           m.flags(),
	}
}

// sendParams converts m into a channel or DM message body.
func (m Message) sendParams() *discordgo.MessageSend {
	return &discordgo.MessageSend{
		Content:         m.Content,
		Embeds:          m.Embeds,
		Components:      m.Components,
		Files:           m.Files,
		AllowedMentions: noMentions(m.AllowedMentions),
		Flags:           m.flags(),
	}
}

// flags hides link previews, which would bury Quack's text under member
// links, and marks ephemeral messages. A message with its own embeds keeps
// them visible.
func (m Message) flags() discordgo.MessageFlags {
	var flags discordgo.MessageFlags
	if len(m.Embeds) == 0 {
		flags |= discordgo.MessageFlagsSuppressEmbeds
	}
	if m.Ephemeral {
		flags |= discordgo.MessageFlagsEphemeral
	}
	return flags
}

// webhookEdit converts e into a webhook message edit. An edit that rewrites
// the content also drops old attachments, which Discord would otherwise
// keep.
func (e Edit) webhookEdit() *discordgo.WebhookEdit {
	edit := &discordgo.WebhookEdit{
		Content:         e.Content,
		Embeds:          e.Embeds,
		Components:      e.Components,
		Files:           e.Files,
		AllowedMentions: noMentions(e.AllowedMentions),
	}
	if e.Content != nil {
		edit.Attachments = &[]*discordgo.MessageAttachment{}
	}
	return edit
}

// noMentions returns allowed, or an empty allow list that suppresses every
// mention when the message did not set one.
func noMentions(allowed *discordgo.MessageAllowedMentions) *discordgo.MessageAllowedMentions {
	if allowed == nil {
		return &discordgo.MessageAllowedMentions{}
	}
	return allowed
}

// utf16Len measures text the way Discord counts its limits.
func utf16Len(text string) int {
	return len(utf16.Encode([]rune(text)))
}

// pageLink matches the single-line Markdown links Quack's views write.
var pageLink = regexp.MustCompile(`\[[^\n]*?\]\([^\n]*?\)`)

// TextPages splits text into pages of at most limit UTF-16 units without
// losing anything: joined, the pages equal text. It breaks after a newline
// if it can, then after a space, and moves a Markdown link that would
// straddle a break to the next page. Callers resolve icons first and leave
// room for their own heading and controls when choosing limit.
func TextPages(text string, limit int) []string {
	if limit < 2 {
		panic("discord: text page limit must fit a Unicode character")
	}
	if text == "" {
		return []string{""}
	}
	var pages []string
	for text != "" {
		units, end := 0, len(text)
		for index, char := range text {
			width := 1
			if char > 0xffff {
				width = 2
			}
			if units+width > limit {
				end = index
				break
			}
			units += width
		}
		if end < len(text) {
			if split := strings.LastIndexByte(text[:end], '\n'); split >= 0 {
				end = split + 1
			} else if split := strings.LastIndexByte(text[:end], ' '); split >= 0 {
				end = split + 1
			}
			// A single link longer than the whole budget still has to split.
			for _, link := range pageLink.FindAllStringIndex(text, -1) {
				if link[0] >= end {
					break
				}
				if link[1] > end {
					if link[0] > 0 {
						end = link[0]
					} else if utf16Len(text[:link[1]]) <= limit {
						end = link[1]
					}
					break
				}
			}
		}
		pages = append(pages, text[:end])
		text = text[end:]
	}
	return pages
}
