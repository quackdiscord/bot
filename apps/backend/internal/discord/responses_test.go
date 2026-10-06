package discord

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestResponseVisibility(t *testing.T) {
	if r := Public(Content("visible", true)); r.Data.Flags&discordgo.MessageFlagsEphemeral != 0 {
		t.Error("Public response is private")
	}
	if r := Ephemeral(Content("private", false)); r.Data.Flags&discordgo.MessageFlagsEphemeral == 0 {
		t.Error("Ephemeral response is not private")
	}
	if r := DeferPublic(); r.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource || r.Data != nil {
		t.Errorf("DeferPublic = %+v", r)
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

// TestErrorResponsesUseConversationAndClearOldEmbeds keeps errors private
// text behind the error icon, and makes an error edit drop old embeds.
func TestErrorResponsesUseConversationAndClearOldEmbeds(t *testing.T) {
	response := Error("Nope")
	if response.Data.Content != "{{quack:error}} Nope" || len(response.Data.Embeds) != 0 ||
		response.Data.Flags&discordgo.MessageFlagsEphemeral == 0 {
		t.Fatalf("expected private conversational error: %+v", response.Data)
	}
	edit := ErrorEdit("Nope")
	body := edit.webhookEdit()
	if !edit.PrivateError || *body.Content != "{{quack:error}} Nope" || body.Embeds == nil || len(*body.Embeds) != 0 {
		t.Fatalf("expected private text error edit clearing embeds: %+v", body)
	}
}

func TestUserMessageFindsWrappedCopy(t *testing.T) {
	err := fmt.Errorf("create channel: %w", &UserError{Message: "Quack needs Manage Channels."})
	if got, ok := UserMessage(err); !ok || got != "Quack needs Manage Channels." {
		t.Fatalf("UserMessage = %q, %v", got, ok)
	}
	if _, ok := UserMessage(errors.New("internal")); ok {
		t.Fatal("internal error treated as user copy")
	}
}

// recordingResponder records a task's calls. Methods it does not override
// panic through the nil embedded interface, catching unexpected messages.
type recordingResponder struct {
	Responder
	calls    []string
	edit     Edit
	followup Message
	editErr  error
}

func (r *recordingResponder) EditOriginal(edit Edit) (*discordgo.Message, error) {
	r.calls = append(r.calls, "edit")
	r.edit = edit
	if r.editErr != nil {
		return nil, r.editErr
	}
	return &discordgo.Message{ID: "original"}, nil
}

func (r *recordingResponder) DeleteOriginal() error {
	r.calls = append(r.calls, "delete")
	return nil
}

func (r *recordingResponder) Followup(message Message) (*discordgo.Message, error) {
	r.calls = append(r.calls, "followup")
	r.followup = message
	return &discordgo.Message{ID: "private"}, nil
}

// TestPublishEditsTheDeferredOriginal keeps Discord's command attribution:
// the result replaces the placeholder and nothing else is posted.
func TestPublishEditsTheDeferredOriginal(t *testing.T) {
	for _, failure := range []error{nil, errors.New("transport failed")} {
		responder := &recordingResponder{editErr: failure}
		result, err := Publish(responder, Content("Case added.", false))
		if !reflect.DeepEqual(responder.calls, []string{"edit"}) || *responder.edit.Content != "Case added." {
			t.Fatalf("unexpected calls: %+v", responder)
		}
		if failure != nil {
			if !errors.Is(err, failure) || result != nil {
				t.Fatalf("edit failure was lost: %v", err)
			}
		} else if err != nil || result.ID != "original" {
			t.Fatalf("response identity changed: %+v %v", result, err)
		}
	}
}

// TestAsyncPublicKeepsOneAttributedSuccess publishes a success in place
// without deleting the response or adding a second message.
func TestAsyncPublicKeepsOneAttributedSuccess(t *testing.T) {
	result := AsyncPublic(func(_ context.Context, r Responder) error {
		_, err := Publish(r, Content("Saved.", false))
		return err
	})
	if result.Response.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource || result.Response.Data != nil {
		t.Fatal("success did not start publicly")
	}
	responder := &recordingResponder{}
	if err := result.Task(context.Background(), responder); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(responder.calls, []string{"edit"}) || *responder.edit.Content != "Saved." {
		t.Fatalf("success created extra responses: %+v", responder)
	}
}

// TestAsyncPublicRemovesPlaceholderAndKeepsErrorPrivate never edits error
// details into the public response and sends one private reply.
func TestAsyncPublicRemovesPlaceholderAndKeepsErrorPrivate(t *testing.T) {
	result := AsyncPublic(func(_ context.Context, r Responder) error {
		_, err := r.EditOriginal(ErrorEdit("You need Manage Server permission."))
		return err
	})
	responder := &recordingResponder{}
	if err := result.Task(context.Background(), responder); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(responder.calls, []string{"delete", "followup"}) || !responder.followup.Ephemeral ||
		!strings.Contains(responder.followup.Content, "Manage Server") {
		t.Fatalf("error became public or duplicated: %+v", responder)
	}
}
