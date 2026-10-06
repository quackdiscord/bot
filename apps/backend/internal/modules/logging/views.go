package logging

import (
	"net/url"
	"strings"

	"github.com/quackdiscord/bot/internal/discord"
)

// entry is an event as a log channel may show it: only the details the
// guild opted into, with secrets already redacted.
type entry struct {
	Type                          EventType
	ChannelID, MessageID, ActorID string
	Before, After                 string
	// BeforeKnown is set only for an edit snapshot with content included,
	// to tell "no text before" from "text unknown".
	BeforeKnown                    *bool
	Attachments, BeforeAttachments []AttachmentMetadata
	EmbedTypes                     []string
	// Messages are a bulk deletion's cached messages, shown one by one.
	Messages []entryMessage
	Metadata map[string]string
}

// entryMessage is one cached message of a bulk deletion.
type entryMessage struct {
	MessageID, ActorID, Content string
	Attachments                 []AttachmentMetadata
	EmbedTypes                  []string
}

// eventIcons is the icon each event's log line leads with.
var eventIcons = map[EventType]string{
	MessageEdit:       "edit",
	MessageDelete:     "delete",
	MessageBulkDelete: "delete",
	MemberJoin:        "join",
	MemberLeave:       "leave",
	DiscordBan:        "ban",
	DiscordUnban:      "unban",
	GuildChange:       "settings",
	ChannelChange:     "settings",
}

// logMessage renders e as the readable post staff see, such as "A message
// from @member was edited." with the text before and after. Member text
// is escaped; mentions are suppressed when it is sent.
func logMessage(e entry) discord.Message {
	parts := append(contentParts(e), bulkParts(e)...)
	if reason := e.Metadata["reason"]; reason != "" {
		parts = append(parts, "Reason:\n"+discord.Quote(discord.PlainText(reason)))
	}
	if name := e.Metadata["name"]; e.Type == GuildChange && name != "" {
		parts = append(parts, "Server: "+discord.PlainText(name))
	}
	if count := e.Metadata["cached_count"]; e.Type == MessageBulkDelete && count != "" {
		parts = append(parts, "Messages with saved content: "+discord.PlainText(count)+".")
	}
	meta := ""
	if e.MessageID != "" {
		meta = "Message " + discord.PlainText(e.MessageID)
	}
	icon := eventIcons[e.Type]
	if icon == "" {
		icon = "info"
	}
	return discord.Conversation(icon, lead(e), "", strings.Join(parts, "\n\n"), meta, false)
}

// lead is the sentence that says what happened.
func lead(e entry) string {
	actor := mention(e.ActorID, "A member")
	target := mention(e.Metadata["target_id"], "A member")
	var text string
	switch e.Type {
	case MessageEdit:
		text = "A message from " + actor + " was edited."
	case MessageDelete:
		text = "A message from " + actor + " was deleted."
	case MessageBulkDelete:
		text = "Messages were deleted."
		if count := e.Metadata["message_count"]; count != "" {
			text = discord.PlainText(count) + " messages were deleted."
		}
	case MemberJoin:
		text = actor + " joined the server."
	case MemberLeave:
		// Their mention would likely render as an unknown user now.
		if name := e.Metadata["username"]; name != "" {
			actor = "**" + discord.PlainText(name) + "**"
		}
		text = actor + " left the server."
	case DiscordBan:
		text = target + " was banned by " + actor + "."
	case DiscordUnban:
		text = target + " was unbanned by " + actor + "."
	case GuildChange:
		text = "Server settings changed."
	case ChannelChange:
		text = "A channel changed."
		switch e.Metadata["operation"] {
		case "created":
			text = "A channel was created."
		case "deleted":
			text = "A channel was deleted."
		case "updated":
			text = "Channel settings changed."
		}
		if name := e.Metadata["name"]; name != "" {
			text = strings.TrimSuffix(text, ".") + ": " + discord.PlainText(name) + "."
		}
	default:
		text = "Server activity was recorded."
	}
	// A channel event names its channel already, and a deleted one could
	// not be linked anyway.
	if e.ChannelID != "" && e.Type != ChannelChange {
		text = strings.TrimSuffix(text, ".") + " in <#" + e.ChannelID + ">."
	}
	return text
}

// contentParts are a single message's text, files, and embeds. A bulk
// deletion shows these per message instead.
func contentParts(e entry) []string {
	if len(e.Messages) > 0 {
		return nil
	}
	edit := e.Type == MessageEdit
	var parts []string
	if e.Before != "" {
		before := discord.Quote(discord.PlainText(e.Before))
		if edit {
			before = "Before:\n" + before
		}
		parts = append(parts, before)
	}
	if edit && e.Before == "" && e.BeforeKnown != nil {
		if *e.BeforeKnown {
			parts = append(parts, "Before: no text.")
		} else {
			parts = append(parts, "Previous text was not available.")
		}
	}
	if e.After != "" {
		parts = append(parts, "After:\n"+discord.Quote(discord.PlainText(e.After)))
	}
	if edit && e.After == "" && e.BeforeKnown != nil {
		parts = append(parts, "After: no text.")
	}
	if edit && len(e.BeforeAttachments) > 0 {
		parts = append(parts, "Files before: "+attachmentLabels(e.BeforeAttachments))
		if len(e.Attachments) == 0 {
			parts = append(parts, "Files after: none.")
		}
	}
	if len(e.Attachments) > 0 {
		label := "Files: "
		if edit {
			label = "Files after: "
		}
		parts = append(parts, label+attachmentLabels(e.Attachments))
	}
	if len(e.EmbedTypes) > 0 {
		parts = append(parts, embedLine(e.EmbedTypes))
	}
	return parts
}

// bulkParts shows each cached message of a bulk deletion with its own
// author and files.
func bulkParts(e entry) []string {
	parts := make([]string, 0, len(e.Messages))
	for _, m := range e.Messages {
		record := mention(m.ActorID, "Unknown author") + " · Message " + discord.PlainText(m.MessageID)
		if m.Content != "" {
			record += "\n" + discord.Quote(discord.PlainText(m.Content))
		}
		if len(m.Attachments) > 0 {
			record += "\nFiles: " + attachmentLabels(m.Attachments)
		}
		if len(m.EmbedTypes) > 0 {
			record += "\n" + embedLine(m.EmbedTypes)
		}
		parts = append(parts, record)
	}
	return parts
}

// mention returns a user mention, or fallback when there is no user.
func mention(userID, fallback string) string {
	if userID == "" {
		return fallback
	}
	return "<@" + userID + ">"
}

// embedLine names a message's embed types.
func embedLine(types []string) string {
	return "Included embeds: " + discord.PlainText(strings.Join(types, ", ")) + "."
}

// attachmentLabels joins the files' labels.
func attachmentLabels(attachments []AttachmentMetadata) string {
	labels := make([]string, len(attachments))
	for i, a := range attachments {
		labels[i] = attachmentLabel(a)
	}
	return strings.Join(labels, ", ")
}

// attachmentLabel links a file's name to its Discord download while that
// link lasts. A missing or unsafe URL leaves the plain name, so neither the
// URL nor the name can break out of the Markdown link.
func attachmentLabel(a AttachmentMetadata) string {
	name := discord.PlainText(a.Filename)
	parsed, err := url.Parse(a.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil ||
		strings.ContainsAny(a.URL, "<>\r\n\t ") {
		return name
	}
	return "[" + name + "](<" + parsed.String() + ">)"
}
