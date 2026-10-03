package discord

import (
	"errors"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestResponseVisibility(t *testing.T) {
	if r := Ephemeral(Content("private", false)); r.Data.Flags&discordgo.MessageFlagsEphemeral == 0 {
		t.Error("Ephemeral response is not private")
	}
	r := DeferEphemeral()
	if r.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource || r.Data.Flags&discordgo.MessageFlagsEphemeral == 0 {
		t.Errorf("DeferEphemeral = %+v", r)
	}
	if r := DeferUpdate(); r.Type != discordgo.InteractionResponseDeferredMessageUpdate {
		t.Errorf("DeferUpdate = %+v", r)
	}
	if data := Content("x", false).responseData(); data.AllowedMentions == nil || len(data.AllowedMentions.Parse) != 0 {
		t.Errorf("mentions are not suppressed by default: %+v", data.AllowedMentions)
	}
}

func TestErrorResponsesUseEmbeds(t *testing.T) {
	response := Error("Nope")
	if response.Data.Content != "" || len(response.Data.Embeds) != 1 || response.Data.Embeds[0].Color != colorError {
		t.Fatalf("expected private error embed, got %+v", response.Data)
	}
	edit := ErrorEdit("Nope").webhookEdit()
	if *edit.Content != "" || len(*edit.Embeds) != 1 {
		t.Fatalf("expected embed error edit, got %+v", edit)
	}
}

func TestEmbedTruncatesByRunes(t *testing.T) {
	long := func(n int) string { return strings.Repeat("é", n+10) }
	embed := newEmbed(long(embedTitleLimit), long(embedDescriptionLimit), colorMain).
		field(long(embedFieldNameLimit), long(embedFieldValueLimit), false).
		field("", "", true).
		footer(long(embedFooterLimit)).
		build()
	checks := map[string][2]int{
		"title":       {len([]rune(embed.Title)), embedTitleLimit},
		"description": {len([]rune(embed.Description)), embedDescriptionLimit},
		"field name":  {len([]rune(embed.Fields[0].Name)), embedFieldNameLimit},
		"field value": {len([]rune(embed.Fields[0].Value)), embedFieldValueLimit},
		"footer":      {len([]rune(embed.Footer.Text)), embedFooterLimit},
	}
	for name, got := range checks {
		if got[0] != got[1] {
			t.Errorf("%s has %d runes, want %d", name, got[0], got[1])
		}
	}
	if embed.Fields[1].Name != "\u200b" || embed.Fields[1].Value != "\u200b" {
		t.Errorf("blank field not filled: %+v", embed.Fields[1])
	}
}

// deferredResponder models Discord treating the first followup to a
// deferred response as an edit of that response.
type deferredResponder struct {
	Responder
	completed                     bool
	originalDeleted               bool
	published                     *discordgo.Message
	editErr, followErr, deleteErr error
}

func (r *deferredResponder) EditOriginal(Edit) (*discordgo.Message, error) {
	if r.editErr != nil {
		return nil, r.editErr
	}
	r.completed = true
	return &discordgo.Message{ID: "original"}, nil
}

func (r *deferredResponder) Followup(message Message) (*discordgo.Message, error) {
	if r.followErr != nil {
		return nil, r.followErr
	}
	r.published = &discordgo.Message{ID: "result", Flags: message.webhookParams().Flags}
	if !r.completed {
		r.published.ID = "original"
		r.published.Flags = discordgo.MessageFlagsEphemeral
	}
	return r.published, nil
}

func (r *deferredResponder) DeleteOriginal() error {
	if r.deleteErr != nil {
		return r.deleteErr
	}
	r.originalDeleted = true
	if r.published != nil && r.published.ID == "original" {
		r.published = nil
	}
	return nil
}

func TestPublishSurvivesPrivateAcknowledgementCleanup(t *testing.T) {
	failure := errors.New("transport failed")
	for _, stage := range []string{"success", "edit", "followup", "cleanup"} {
		t.Run(stage, func(t *testing.T) {
			r := &deferredResponder{}
			switch stage {
			case "edit":
				r.editErr = failure
			case "followup":
				r.followErr = failure
			case "cleanup":
				r.deleteErr = failure
			}
			result, err := Publish(r, Content("Case created", true))
			if stage == "edit" || stage == "followup" {
				if !errors.Is(err, failure) || r.originalDeleted || r.published != nil {
					t.Fatalf("lost acknowledgement on failure: %+v, %v", r, err)
				}
				return
			}
			if err != nil || result == nil || r.published == nil || result.ID == "original" || result.Flags&discordgo.MessageFlagsEphemeral != 0 {
				t.Fatalf("result did not persist publicly: %+v, %v", r, err)
			}
			if stage == "success" && !r.originalDeleted {
				t.Fatal("private acknowledgement not cleaned up")
			}
		})
	}
}
