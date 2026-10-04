package logging

import (
	"strings"
	"testing"
)

// TestBanLogNamesModeratorAndTargetWithoutInternalMetadata keeps the
// moderator and the banned member apart and shows only known details.
func TestBanLogNamesModeratorAndTargetWithoutInternalMetadata(t *testing.T) {
	message := logMessage(entry{Type: DiscordBan, ActorID: "moderator", Metadata: map[string]string{
		"target_id": "member", "reason": "Repeated spam", "worker": "private",
	}})
	if !strings.Contains(message.Content, "<@member> was banned by <@moderator>") ||
		!strings.Contains(message.Content, "Reason:\n> Repeated spam") {
		t.Fatalf("missing ban context: %s", message.Content)
	}
	for _, hidden := range []string{"worker", "private", "target"} {
		if strings.Contains(message.Content, hidden) {
			t.Fatalf("internal metadata leaked: %s", message.Content)
		}
	}
}

func TestLogUsesReadableChannelAndBulkDeleteDetails(t *testing.T) {
	channel := logMessage(entry{Type: ChannelChange, ChannelID: "deleted",
		Metadata: map[string]string{"operation": "deleted", "name": "old-channel"}})
	if !strings.Contains(channel.Content, "A channel was deleted: old-channel.") || strings.Contains(channel.Content, " in <#deleted>") {
		t.Fatalf("awkward deletion copy: %s", channel.Content)
	}
	bulk := logMessage(entry{Type: MessageBulkDelete, ChannelID: "channel",
		Metadata: map[string]string{"message_count": "20", "cached_count": "1"}})
	if !strings.Contains(bulk.Content, "20 messages were deleted in <#channel>.") ||
		!strings.Contains(bulk.Content, "Messages with saved content: 1.") {
		t.Fatalf("missing bulk context: %s", bulk.Content)
	}
}

func TestEditLogDistinguishesEmptyTextAndRemovedFiles(t *testing.T) {
	known, unknown := true, false
	message := logMessage(entry{Type: MessageEdit, BeforeKnown: &known, After: "new",
		BeforeAttachments: []AttachmentMetadata{{Filename: "proof.png"}}})
	for _, want := range []string{"Before: no text.", "After:\n> new", "Files before: proof.png", "Files after: none."} {
		if !strings.Contains(message.Content, want) {
			t.Fatalf("missing %q: %s", want, message.Content)
		}
	}
	message = logMessage(entry{Type: MessageEdit, BeforeKnown: &unknown, After: "new"})
	if !strings.Contains(message.Content, "Previous text was not available.") {
		t.Fatalf("unknown text presented as empty: %s", message.Content)
	}
}

// TestBulkLogShowsEachAuthorWithTheirOwnFiles keeps each file under the
// message it belonged to.
func TestBulkLogShowsEachAuthorWithTheirOwnFiles(t *testing.T) {
	message := logMessage(entry{Type: MessageBulkDelete, Messages: []entryMessage{
		{MessageID: "one", ActorID: "alice", Content: "first text", Attachments: []AttachmentMetadata{{Filename: "first.png"}}},
		{MessageID: "two", ActorID: "bob", Content: "second text", Attachments: []AttachmentMetadata{{Filename: "second.png"}}},
	}, Metadata: map[string]string{"message_count": "2", "cached_count": "2"}})
	for _, part := range []string{"<@alice> · Message one", "first text", "Files: first.png", "<@bob> · Message two", "second text", "Files: second.png"} {
		if !strings.Contains(message.Content, part) {
			t.Fatalf("missing %q: %s", part, message.Content)
		}
	}
	if strings.Index(message.Content, "first.png") > strings.Index(message.Content, "<@bob>") {
		t.Fatal("files detached from author")
	}
}

func TestLogLayoutAndEscaping(t *testing.T) {
	message := logMessage(entry{Type: MessageDelete, ChannelID: "channel", MessageID: "123", ActorID: "member", Before: "**bold** {{quack:ban}}"})
	want := "{{quack:delete}} A message from <@member> was deleted in <#channel>.\n\n" +
		"> \\*\\*bold\\*\\* \\{\\{quack:ban\\}\\}\n-# Message 123"
	if message.Content != want {
		t.Fatalf("content =\n%s\nwant\n%s", message.Content, want)
	}
	if message.Ephemeral {
		t.Fatal("log post is ephemeral")
	}
}

// TestAttachmentLabelsRejectUnsafeDestinations keeps a forged URL from
// injecting Markdown links while keeping the file name.
func TestAttachmentLabelsRejectUnsafeDestinations(t *testing.T) {
	for _, raw := range []string{"", "javascript:alert(1)", "https://user:secret@example.com/proof",
		"https://example.com/a>)[evil](https://other.test)", "https://example.com/a\nb"} {
		if got := attachmentLabel(AttachmentMetadata{Filename: "proof.png", URL: raw}); got != "proof.png" {
			t.Fatalf("unsafe destination rendered: %q", got)
		}
	}
	got := attachmentLabel(AttachmentMetadata{Filename: "[proof].png", URL: "https://cdn.discordapp.com/a(b).png"})
	if !strings.Contains(got, `\[proof\]`) || !strings.Contains(got, "(<https://cdn.discordapp.com/a(b).png>)") {
		t.Fatal(got)
	}
}

// TestDeletedAttachmentLinksSurvive checks single and bulk deletions link
// the file to Discord's download.
func TestDeletedAttachmentLinksSurvive(t *testing.T) {
	const link = "https://cdn.discordapp.com/attachments/channel/file/proof.png?ex=123&sig=abc"
	files := []AttachmentMetadata{{DiscordID: "file", Filename: "proof.png", URL: link}}
	for _, e := range []entry{
		{Type: MessageDelete, Attachments: files},
		{Type: MessageBulkDelete, Messages: []entryMessage{{MessageID: "message", ActorID: "member", Attachments: files}}},
	} {
		if message := logMessage(e); !strings.Contains(message.Content, "[proof.png](<"+link+">)") {
			t.Fatalf("%s lost downloadable link: %s", e.Type, message.Content)
		}
	}
}
