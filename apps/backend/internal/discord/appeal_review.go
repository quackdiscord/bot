package discord

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

// maxQueuePage bounds page numbers read from custom IDs.
const maxQueuePage = 1000000

// appealsCommand defines /appeals, the pending appeal queue. It finds every
// pending appeal even if its queue post never reached the channel.
func appealsCommand() *discordgo.ApplicationCommand {
	return moderatorCommand(&discordgo.ApplicationCommand{
		Name:        appealsCommandName,
		Description: "Review pending appeals",
	})
}

// liveStaff resolves who is acting from live Discord state rather than the
// permissions the interaction carries.
func liveStaff(ctx context.Context, services *quack.Services, i *discordgo.InteractionCreate) (*quack.GuildStaffContext, error) {
	userID, name := interactionMember(i)
	return services.Guilds.ResolveDiscordStaffContext(ctx, quack.DiscordStaffContextInput{
		DiscordGuildID: i.GuildID,
		DiscordUserID:  userID,
		DisplayName:    name,
	})
}

// inGuild reports whether i came from a member in a server.
func inGuild(i *discordgo.InteractionCreate) bool {
	return i.GuildID != "" && i.Member != nil && i.Member.User != nil
}

// ephemeralSource reports whether the component's message is visible only
// to the clicker.
func ephemeralSource(i *discordgo.InteractionCreate) bool {
	return i.Message != nil && i.Message.Flags&discordgo.MessageFlagsEphemeral != 0
}

// updateError reports a failed component update. A private message is
// replaced, so a page left open by a moderator who lost access stops
// showing the appeal; a shared message is left alone and the error goes
// privately to the clicker.
func updateError(i *discordgo.InteractionCreate, responder Responder, text string) error {
	if ephemeralSource(i) {
		_, err := responder.EditOriginal(ErrorEdit(text))
		return err
	}
	_, err := responder.Followup(Signal("error", text, true))
	return err
}

// decide handles Accept and Reject on a queue post. When the guild requires
// a decision reason the button opens a form instead, and since a form must
// be the first response the setting is read before anything else.
func (a appeals) decide(action string) Handler {
	return func(ctx context.Context, i *discordgo.InteractionCreate) Result {
		if !inGuild(i) {
			return Immediate(Error("Open this appeal in the server’s review queue to decide it."))
		}
		id, err := DecodeCustomID(i.MessageComponentData().CustomID)
		if err != nil {
			return Immediate(Error("That appeal button is broken. Open /appeals to try again."))
		}
		required, err := a.services.Appeals.ReviewReasonRequired(ctx, i.GuildID)
		if err != nil {
			return Immediate(Error("I couldn’t check the server’s appeal settings. Try again in a moment."))
		}
		if required {
			title := "Accept appeal"
			if action == appealRejectAction {
				title = "Reject appeal"
			}
			return Immediate(Modal(title, appealCustomID(action+"_reason", id.Payload), []discordgo.MessageComponent{
				Row(discordgo.TextInput{
					CustomID:    "reason",
					Label:       "Reason sent to the member",
					Style:       discordgo.TextInputParagraph,
					Required:    true,
					MinLength:   1,
					MaxLength:   2000,
					Placeholder: "Explain this decision. The member receives this reason.",
				}),
			}))
		}
		reason := "This case has been voided."
		if action == appealRejectAction {
			reason = "Appeal rejected."
		}
		return Async(DeferUpdate(), a.decision(i, action, id.Payload, reason))
	}
}

// decideWithReason completes a decision from the reason form. Permissions
// are checked again on submit, since they may have changed while the form
// was open.
func (a appeals) decideWithReason(action string) Handler {
	return func(_ context.Context, i *discordgo.InteractionCreate) Result {
		if !inGuild(i) {
			return Immediate(Error("Open this appeal in the server’s review queue to decide it."))
		}
		data := i.ModalSubmitData()
		id, err := DecodeCustomID(data.CustomID)
		if err != nil {
			return Immediate(Error("That appeal form is broken. Open /appeals to try again."))
		}
		reason := ModalValue(data, "reason")
		if strings.TrimSpace(reason) == "" {
			return Immediate(Error("Write a reason for this decision. The member receives it."))
		}
		return Async(DeferUpdate(), a.decision(i, action, id.Payload, reason))
	}
}

// decision records one decision with live permissions and shows the result
// on the queue message. The appeal's transaction settles races between
// moderators; a failure goes privately to the clicker and leaves the shared
// message as it was.
func (a appeals) decision(i *discordgo.InteractionCreate, action, appealID, reason string) Task {
	return func(ctx context.Context, responder Responder) error {
		fail := func(text string) error {
			_, err := responder.Followup(Signal("error", text, true))
			return err
		}
		staff, err := liveStaff(ctx, a.services, i)
		if err != nil {
			return fail("I couldn’t check your Discord permissions. Try again in a moment.")
		}
		var decided *quack.AppealResponse
		if action == appealAcceptAction {
			decided, err = a.services.Appeals.Accept(ctx, staff, appealID, reason)
		} else {
			decided, err = a.services.Appeals.Reject(ctx, staff, appealID, reason)
		}
		switch {
		case errors.Is(err, quack.ErrAppealConflict):
			return fail("This appeal has already been decided or its case was voided.")
		case errors.Is(err, quack.ErrAppealPermissionDenied):
			return fail("You need Moderate Members permission to review appeals.")
		case errors.Is(err, quack.ErrAppealNotFound):
			return fail("I couldn’t find that appeal in this server. Open /appeals to see pending appeals.")
		case errors.Is(err, quack.ErrAppealValidation):
			return fail("Write a reason between 1 and 2,000 characters.")
		case err != nil:
			return fail("I couldn’t save your decision. Please try again.")
		}
		message := appealStaffPage(decided, 1, i.AppID, a.staffURL(i, decided))
		if ephemeralSource(i) {
			message.Components = append(message.Components, Row(
				Button(appealCustomID(appealPageAction, "1"), "Next pending appeal", discordgo.SecondaryButton, false),
			))
		}
		_, err = Publish(responder, message)
		return err
	}
}

// statementPage turns the page of a long statement. On the shared queue
// post it opens the page as a new message, so one moderator never moves
// another's place; on a private copy it turns that copy. Permissions are
// checked again on every page.
func (a appeals) statementPage(delta int) Handler {
	return func(_ context.Context, i *discordgo.InteractionCreate) Result {
		if !inGuild(i) {
			return Immediate(Error("Open this appeal in your server's review queue."))
		}
		id, err := DecodeCustomID(i.MessageComponentData().CustomID)
		pageText, appealID, found := strings.Cut(id.Payload, "|")
		page, pageErr := strconv.Atoi(pageText)
		if err != nil || !found || appealID == "" || pageErr != nil || page < 1 || page > maxQueuePage {
			return Immediate(Error("I couldn’t open that page. Run /appeals to start again."))
		}
		task := func(ctx context.Context, responder Responder) error {
			staff, err := liveStaff(ctx, a.services, i)
			if err != nil {
				_, err = responder.EditOriginal(ErrorEdit("I couldn’t check your Discord permissions. Try again in a moment."))
				return err
			}
			appeal, err := a.services.Appeals.GetStaff(ctx, staff, appealID)
			if err != nil {
				_, err = responder.EditOriginal(ErrorEdit("I couldn’t open that appeal. Check that you have Moderate Members permission, then try /appeals."))
				return err
			}
			message := appealStaffPage(appeal, page+delta, i.AppID, a.staffURL(i, appeal))
			message.Ephemeral = false
			message.Components = append(message.Components, Row(
				Button(appealCustomID(appealPageAction, "1"), "Pending appeals", discordgo.SecondaryButton, false),
			))
			_, err = responder.EditOriginal(EditMessage(message))
			return err
		}
		if ephemeralSource(i) {
			return Async(DeferUpdate(), task)
		}
		return Async(DeferEphemeral(), task)
	}
}

// command handles /appeals.
func (a appeals) command(_ context.Context, i *discordgo.InteractionCreate) Result {
	if i.GuildID == "" {
		return Immediate(Error("Something went wrong. Try again later."))
	}
	return AsyncPublic(a.queue(i, 1, false))
}

// queuePage handles Previous and Next on the /appeals queue.
func (a appeals) queuePage(_ context.Context, i *discordgo.InteractionCreate) Result {
	id, err := DecodeCustomID(i.MessageComponentData().CustomID)
	page, pageErr := strconv.Atoi(id.Payload)
	if err != nil || pageErr != nil || page < 1 || page > maxQueuePage || i.GuildID == "" {
		return Immediate(Error("I couldn’t open that page. Run /appeals to start again."))
	}
	return Async(DeferUpdate(), a.queue(i, page, true))
}

// queue shows one pending appeal per page, read straight from storage with
// fresh permissions, so it works even when queue posts failed. A page past
// the end, after others decided appeals, falls back to the first.
func (a appeals) queue(i *discordgo.InteractionCreate, page int, update bool) Task {
	return func(ctx context.Context, responder Responder) error {
		fail := func(text string) error {
			if update {
				return updateError(i, responder, text)
			}
			_, err := responder.EditOriginal(ErrorEdit(text))
			return err
		}
		staff, err := liveStaff(ctx, a.services, i)
		if err != nil {
			return fail("I couldn’t check your Discord permissions. Try again in a moment.")
		}
		list, err := a.services.Appeals.ListStaff(ctx, staff, quack.AppealStatusPending, 1, page-1)
		if err != nil {
			return fail("You need Moderate Members permission to review this server's appeals.")
		}
		if len(list.Appeals) == 0 && page > 1 {
			page = 1
			if list, err = a.services.Appeals.ListStaff(ctx, staff, quack.AppealStatusPending, 1, 0); err != nil {
				return err
			}
		}
		message := Signal("appeal", "No appeals are waiting for review.", false)
		if len(list.Appeals) > 0 {
			message = appealStaffPage(&list.Appeals[0], 1, i.AppID, a.staffURL(i, &list.Appeals[0]))
			message.Ephemeral = false
			message.Content += fmt.Sprintf("\nPending appeal %d of %d", page, list.Total)
			message.Components = append(message.Components, Row(
				Button(appealCustomID(appealPageAction, strconv.Itoa(max(1, page-1))), "Previous", discordgo.SecondaryButton, page <= 1),
				Button(appealCustomID(appealPageAction, strconv.Itoa(page+1)), "Next", discordgo.SecondaryButton, int64(page) >= list.Total),
			))
		}
		_, err = Publish(responder, message)
		return err
	}
}

// appealReversal handles the "Confirm ..." button on an accepted appeal.
// Accepting queues reversals itself; this button covers a timeout or ban
// that has none yet, after live permission and hierarchy checks.
func appealReversal(services *quack.Services) Handler {
	return func(_ context.Context, i *discordgo.InteractionCreate) Result {
		if !inGuild(i) {
			return Immediate(Error("Open this appeal in the server’s review queue to remove the punishment."))
		}
		id, err := DecodeCustomID(i.MessageComponentData().CustomID)
		if err != nil {
			return Immediate(Error("That punishment button is broken. Open the case to try again."))
		}
		parts := strings.Split(id.Payload, ",")
		if len(parts) != 3 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return Immediate(Error("That punishment button is broken. Open the case to try again."))
		}
		appealID, executionID, actionType := parts[0], parts[1], quack.ActionType(parts[2])
		if actionType != quack.ActionRemoveTimeout && actionType != quack.ActionUnbanUser {
			return Immediate(Error("Only bans and timeouts can be removed here."))
		}
		return Async(DeferEphemeral(), func(ctx context.Context, responder Responder) error {
			staff, err := liveStaff(ctx, services, i)
			if err != nil {
				_, _ = responder.EditOriginal(ErrorEdit("I couldn’t check your Discord permissions. Try again in a moment."))
				return nil
			}
			appeal, err := services.Appeals.GetStaff(ctx, staff, appealID)
			if err != nil || appeal.Status != quack.AppealStatusAccepted {
				_, _ = responder.EditOriginal(ErrorEdit("Accept the appeal before removing its punishment."))
				return nil
			}
			_, err = services.Actions.ReverseForAppeal(ctx, staff, appeal.CaseID, executionID, actionType, &appeal.ID)
			if err != nil {
				_, _ = responder.EditOriginal(ErrorEdit("I couldn’t queue the punishment removal. Check your moderation permissions and try again."))
				return nil
			}
			_, err = Publish(responder, Signal("retry", "Punishment removal queued. Check the case for the result.", false))
			return err
		})
	}
}
