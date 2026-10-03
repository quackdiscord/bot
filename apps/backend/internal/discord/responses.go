package discord

import (
	"context"
	"errors"

	"github.com/bwmarrin/discordgo"
)

// Public answers with a message everyone in the channel can see.
func Public(m Message) *discordgo.InteractionResponse {
	m.Ephemeral = false
	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: m.responseData(),
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

// Update immediately replaces the message a component belongs to.
func Update(m Message) *discordgo.InteractionResponse {
	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: m.responseData(),
	}
}

// DeferPublic acknowledges with a public "thinking" state that the task
// later replaces with Publish.
func DeferPublic() *discordgo.InteractionResponse {
	return &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredChannelMessageWithSource}
}

// DeferEphemeral acknowledges with a private "thinking" state that the task
// later edits.
func DeferEphemeral() *discordgo.InteractionResponse {
	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
	}
}

// DeferUpdate acknowledges a component without changing its message yet;
// the task then edits that message with EditOriginal.
func DeferUpdate() *discordgo.InteractionResponse {
	return &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredMessageUpdate}
}

// Modal opens a form whose submission is routed by customID. A modal must
// be the first response, so a handler that needs one cannot defer first.
func Modal(title, customID string, components []discordgo.MessageComponent) *discordgo.InteractionResponse {
	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{Title: title, CustomID: customID, Components: components},
	}
}

// Autocomplete answers an autocomplete request with up to 25 choices.
func Autocomplete(choices []*discordgo.ApplicationCommandOptionChoice) *discordgo.InteractionResponse {
	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionApplicationCommandAutocompleteResult,
		Data: &discordgo.InteractionResponseData{Choices: choices},
	}
}

// Error answers privately with content behind the error icon.
func Error(content string) *discordgo.InteractionResponse {
	return Ephemeral(Signal("error", content, true))
}

// ErrorEdit replaces a deferred response with content behind the error
// icon. It is marked PrivateError, so under AsyncPublic it reaches only the
// invoking user.
func ErrorEdit(content string) Edit {
	edit := EditMessage(Signal("error", content, false))
	edit.PrivateError = true
	return edit
}

// Publish replaces a public deferred response with message in place, which
// keeps Discord's "used /case" attribution on the result. Discord fixes a
// response's visibility when it is acknowledged, so the handler must have
// deferred with DeferPublic, usually through AsyncPublic.
func Publish(responder Responder, message Message) (*discordgo.Message, error) {
	return responder.EditOriginal(EditMessage(message))
}

// AsyncPublic defers publicly and runs task. A successful result is
// published in place; an error edit (ErrorEdit) deletes the public
// placeholder and goes to the invoking user in a private followup, so
// failure details never land in the channel.
func AsyncPublic(task Task) Result {
	return Async(DeferPublic(), func(ctx context.Context, responder Responder) error {
		return task(ctx, publicResponder{Responder: responder})
	})
}

// publicResponder reroutes only error edits; everything else goes to the
// public response as usual.
type publicResponder struct{ Responder }

// EditOriginal sends a PrivateError edit as a private followup after
// deleting the public placeholder.
func (r publicResponder) EditOriginal(edit Edit) (*discordgo.Message, error) {
	if !edit.PrivateError {
		return r.Responder.EditOriginal(edit)
	}
	if err := r.Responder.DeleteOriginal(); err != nil {
		return nil, err
	}
	message := Message{Ephemeral: true, Files: edit.Files, AllowedMentions: edit.AllowedMentions}
	if edit.Content != nil {
		message.Content = *edit.Content
	}
	if edit.Embeds != nil {
		message.Embeds = *edit.Embeds
	}
	if edit.Components != nil {
		message.Components = *edit.Components
	}
	return r.Responder.Followup(message)
}

// UserError is an error whose message is written for the person who
// invoked a command and can be shown to them unchanged, such as "Quack
// needs Manage Channels permission to create #appeals." Every other error
// is internal and must be mapped to copy by the caller.
type UserError struct {
	Message string
}

// Error returns the user-facing copy.
func (e *UserError) Error() string { return e.Message }

// UserMessage returns the copy of the UserError in err's chain, if there is
// one.
func UserMessage(err error) (string, bool) {
	if user, ok := errors.AsType[*UserError](err); ok {
		return user.Message, true
	}
	return "", false
}
