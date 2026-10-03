package discord

import (
	"context"
	"errors"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

// The appeal routes. Their custom IDs are posted in DMs and the appeal queue
// channel, so they must keep decoding the same way:
//
//   - appeal:submit:v1:<case>, the "Appeal decision" button on a punishment
//     DM and the appeal form it opens
//   - appeal:accept:v1:<appeal> and appeal:reject:v1:<appeal>, the queue
//     decision buttons, and accept_reason and reject_reason, their
//     reason forms
//   - appeal:statement_prev and statement_next, "<page>|<appeal>"
//   - appeal:page:v1:<page>, the /appeals queue
//   - appeal:reverse:v1:<appeal>,<execution>,<action>, the "Confirm ..."
//     reversal button on an accepted appeal
const (
	appealNamespace     = "appeal"
	appealSubmitAction  = "submit"
	appealAcceptAction  = "accept"
	appealRejectAction  = "reject"
	appealPageAction    = "page"
	appealReverseAction = "reverse"

	appealsCommandName = "appeals"
)

// appealCustomID is the custom ID of appeal route action for payload.
func appealCustomID(action, payload string) string {
	return MustCustomID(CustomID{Namespace: appealNamespace, Action: action, Version: "v1", Payload: payload})
}

// appeals handles the member appeal form, the staff queue controls, and
// /appeals.
type appeals struct {
	services *quack.Services
}

// register installs /appeals and the appeal components and forms on r.
func (a appeals) register(r *Router) {
	r.commands[appealsCommandName] = a.command
	r.HandleComponent(appealNamespace, appealSubmitAction, a.openForm)
	r.HandleModal(appealNamespace, appealSubmitAction, a.submit)
	for _, action := range []string{appealAcceptAction, appealRejectAction} {
		r.HandleComponent(appealNamespace, action, a.decide(action))
		r.HandleModal(appealNamespace, action+"_reason", a.decideWithReason(action))
	}
	r.HandleComponent(appealNamespace, "statement_prev", a.statementPage(-1))
	r.HandleComponent(appealNamespace, "statement_next", a.statementPage(1))
	r.HandleComponent(appealNamespace, appealPageAction, a.queuePage)
	r.HandleComponent(appealNamespace, appealReverseAction, appealReversal(a.services))
}

// openForm handles "Appeal decision" on a punishment DM. Eligibility is
// checked before the form opens, so a member never writes a statement that
// cannot be saved; the case ID comes from the button, but the member is
// always the one Discord says clicked it.
func (a appeals) openForm(ctx context.Context, i *discordgo.InteractionCreate) Result {
	memberID := appellant(i)
	if memberID == "" {
		return Immediate(Error("I couldn’t identify your Discord account. Try opening the appeal again."))
	}
	id, err := DecodeCustomID(i.MessageComponentData().CustomID)
	if err != nil {
		return Immediate(Error("That appeal button is broken. Ask a moderator for help."))
	}
	if err := a.services.Appeals.CanSubmit(ctx, id.Payload, memberID); err != nil {
		return Immediate(Error(appealSubmissionError(err)))
	}
	return Immediate(Modal("Appeal this case", appealCustomID(appealSubmitAction, id.Payload), []discordgo.MessageComponent{
		Row(discordgo.TextInput{
			CustomID:    "reason",
			Label:       "What would you like staff to reconsider?",
			Style:       discordgo.TextInputParagraph,
			Required:    true,
			MinLength:   1,
			MaxLength:   quack.AppealStatementMaxLength,
			Placeholder: "Explain what happened or share your apology. You can submit once.",
		}),
	}))
}

// submit saves the member's statement. Submit checks ownership and
// eligibility again, since the case may have changed while the form was
// open. Guild membership is not needed, so a banned member can appeal.
func (a appeals) submit(_ context.Context, i *discordgo.InteractionCreate) Result {
	memberID := appellant(i)
	if memberID == "" {
		return Immediate(Error("I couldn’t identify your Discord account. Open the appeal form again."))
	}
	data := i.ModalSubmitData()
	id, err := DecodeCustomID(data.CustomID)
	if err != nil {
		return Immediate(Error("I couldn’t read this appeal form. Open it again and try once more."))
	}
	statement := ModalValue(data, "reason")
	return Async(DeferEphemeral(), func(ctx context.Context, responder Responder) error {
		_, err := a.services.Appeals.Submit(ctx, id.Payload, memberID, quack.AppealSubmissionInput{Statement: statement})
		if err != nil {
			_, err = responder.EditOriginal(ErrorEdit(appealSubmissionError(err)))
			return err
		}
		_, err = Publish(responder, Signal("appeal", "Your appeal was submitted. We’ll DM you when the moderators decide.", true))
		return err
	})
}

// appellant is the member Discord says sent the interaction: the user in a
// DM, the member in a guild. It never comes from a custom ID.
func appellant(i *discordgo.InteractionCreate) string {
	if i.User != nil {
		return i.User.ID
	}
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User.ID
	}
	return ""
}

// appealSubmissionError tells the member what went wrong without case
// details.
func appealSubmissionError(err error) string {
	switch {
	case errors.Is(err, quack.ErrAppealConflict):
		return "You already submitted an appeal for this case."
	case errors.Is(err, quack.ErrAppealCaseIneligible):
		return "This case cannot be appealed."
	case errors.Is(err, quack.ErrAppealNotFound):
		return "This appeal is not available to you. Open the appeal button in your own case DM."
	case errors.Is(err, quack.ErrAppealValidation):
		return "Write your appeal in 1–4,000 characters."
	default:
		return "Your appeal could not be saved. Please try again."
	}
}
