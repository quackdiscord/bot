package discord

import (
	"io"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/bwmarrin/discordgo"
)

// TestTextTransportsPreserveLongContentAndControls covers the text limit,
// Unicode, mention suppression, and attachment replacement.
func TestTextTransportsPreserveLongContentAndControls(t *testing.T) {
	body := Conversation("case", "Case for <@123>.", strings.Repeat("🦆", 1100), "Next steps.", "Case #12", true)
	body.Components = []discordgo.MessageComponent{Row(Button("case:void:v1:case", "Void case", discordgo.SecondaryButton, false))}
	prepared := body.ForApplication("819019613371236432")
	if utf16Len(prepared.Content) > contentLimit || !strings.Contains(prepared.Content, "<:quack_case:") ||
		len(prepared.Files) != 1 || len(prepared.Components) != 1 {
		t.Fatalf("invalid message: %+v", prepared)
	}
	full, err := io.ReadAll(prepared.Files[0].Reader)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(full), strings.Repeat("🦆", 1100)) || !strings.Contains(string(full), "Case #12") {
		t.Fatal("long content lost")
	}
	data := prepared.responseData()
	if len(data.Files) != 1 || data.Flags&discordgo.MessageFlagsEphemeral == 0 ||
		data.AllowedMentions == nil || len(data.AllowedMentions.Parse) != 0 {
		t.Fatal("initial response lost attachments or privacy")
	}
	sent := body.ForApplication("968198214450831370").sendParams()
	if len(sent.Files) != 1 || sent.AllowedMentions == nil || len(sent.AllowedMentions.Parse) != 0 ||
		sent.Flags&discordgo.MessageFlagsSuppressEmbeds == 0 {
		t.Fatal("channel send lost safe text presentation")
	}
	edit := EditMessage(Signal("success", "Done.", true)).ForApplication("819019613371236432").webhookEdit()
	if edit.Embeds == nil || len(*edit.Embeds) != 0 || edit.Attachments == nil || len(*edit.Attachments) != 0 {
		t.Fatal("short edit did not clear previous embeds and files")
	}
}

// TestOwnEmbedsStayVisible keeps link-preview suppression from hiding an
// embed Quack sent on purpose.
func TestOwnEmbedsStayVisible(t *testing.T) {
	m := Message{Embeds: []*discordgo.MessageEmbed{{Title: "Card"}}}
	if m.sendParams().Flags&discordgo.MessageFlagsSuppressEmbeds != 0 || m.webhookParams().Flags&discordgo.MessageFlagsSuppressEmbeds != 0 {
		t.Fatal("message embeds were suppressed")
	}
}

// TestTextLimitHandlesAnUnbrokenParagraph never cuts an emoji in half.
func TestTextLimitHandlesAnUnbrokenParagraph(t *testing.T) {
	m := Content(strings.Repeat("🦆", 1001), false).ForApplication("unknown")
	if len(m.Files) != 1 || m.Content != "The full message is attached." {
		t.Fatal(m.Content)
	}
	m = Content(strings.Repeat("🦆", 1000), false).ForApplication("unknown")
	if len(m.Files) != 0 {
		t.Fatal("a message at the limit was attached anyway")
	}
}

// TestPrepareResponseLeavesFormsUntouched keeps message rendering away from
// modals, autocomplete choices, and deferred acknowledgements.
func TestPrepareResponseLeavesFormsUntouched(t *testing.T) {
	modal := Modal("Tell us more", "case:context:v1:1", nil)
	if PrepareResponse(modal, "819019613371236432") != modal {
		t.Fatal("modal was replaced")
	}
	response := Error("Try again.")
	prepared := PrepareResponse(response, "819019613371236432")
	if !strings.Contains(prepared.Data.Content, "<:quack_error:") || prepared.Data.Flags&discordgo.MessageFlagsEphemeral == 0 ||
		!strings.Contains(response.Data.Content, "{{quack:") {
		t.Fatal("response resolution mutated the source or lost visibility")
	}
}

// TestTextPagesPreservesLongUnicodeRecords checks Discord's UTF-16 budget
// without losing whitespace, splitting characters, or dropping text.
func TestTextPagesPreservesLongUnicodeRecords(t *testing.T) {
	for _, source := range []string{"", strings.Repeat("🦆", 3000), strings.Repeat("message with spaces\n", 300), strings.Repeat("x", 5000)} {
		pages := TextPages(source, 1600)
		if strings.Join(pages, "") != source {
			t.Fatal("pagination changed the record")
		}
		for _, page := range pages {
			if !utf8.ValidString(page) || len(utf16.Encode([]rune(page))) > 1600 {
				t.Fatal("page exceeds the budget or contains invalid Unicode")
			}
		}
	}
}

// TestTextPagesKeepsEvidenceLinksClickable moves a link with spaces in its
// label to the next page instead of splitting it.
func TestTextPagesKeepsEvidenceLinksClickable(t *testing.T) {
	link := "[Screenshot of the message](https://example.com/evidence.png)"
	source := strings.Repeat("x", 1580) + "\n" + link
	pages := TextPages(source, 1600)
	if len(pages) != 2 || pages[1] != link || strings.Join(pages, "") != source {
		t.Fatalf("evidence link was split: %q", pages)
	}
}

// TestSpacerSitsAboveButtonRows checks the invisible spacer line is added
// once, only when a message has buttons, for the bot that owns the emoji.
func TestSpacerSitsAboveButtonRows(t *testing.T) {
	const beta = "819019613371236432"
	row := []discordgo.MessageComponent{Row(Button("case:view:v1:1", "View", discordgo.SecondaryButton, false))}
	tests := []struct {
		name       string
		message    Message
		wantSuffix bool
	}{
		{name: "buttons", message: Message{Content: "Case #1", Components: row}, wantSuffix: true},
		{name: "no buttons", message: Message{Content: "Case #1"}},
		{name: "buttons without text", message: Message{Components: row}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			once := test.message.ForApplication(beta)
			twice := once.ForApplication(beta)
			spacer := "\n<:spacer:1556028157168193646>"
			if got := strings.HasSuffix(once.Content, spacer); got != test.wantSuffix {
				t.Fatalf("content = %q, want spacer suffix %v", once.Content, test.wantSuffix)
			}
			if strings.Count(twice.Content, "spacer") > 1 {
				t.Fatalf("spacer added twice: %q", twice.Content)
			}
		})
	}
}
