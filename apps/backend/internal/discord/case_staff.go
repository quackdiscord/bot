package discord

import (
	"context"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

// casePageSize is how many cases or failures one page shows.
const casePageSize = 10

// staffCommand handles the /case subcommands other than add: browsing cases
// and recovering failed actions. Results replace the public placeholder;
// errors go only to the moderator.
func (c *cases) staffCommand(i *discordgo.InteractionCreate, data discordgo.ApplicationCommandInteractionData) Result {
	var selected *discordgo.ApplicationCommandInteractionDataOption
	for _, option := range data.Options {
		if option != nil {
			selected = option
			break
		}
	}
	if selected == nil {
		return Immediate(Error("Choose a case operation."))
	}
	return AsyncPublic(func(ctx context.Context, responder Responder) error {
		staff, err := c.staff(ctx, i)
		if err != nil {
			_, err := responder.EditOriginal(ErrorEdit(caseErrorMessage(err)))
			return err
		}
		response, err := c.runStaffCommand(ctx, staff, selected)
		if err != nil {
			_, err := responder.EditOriginal(ErrorEdit(caseErrorMessage(err)))
			return err
		}
		_, err = Publish(responder, response)
		return err
	})
}

// runStaffCommand performs the selected subcommand and renders its result.
// The irreversible ones, void and reverse, require the confirm option.
func (c *cases) runStaffCommand(
	ctx context.Context, staff *quack.GuildStaffContext, selected *discordgo.ApplicationCommandInteractionDataOption,
) (Message, error) {
	option := func(name string) string { return optionString(selected.GetOption(name)) }
	confirmed := func() bool {
		confirm := selected.GetOption("confirm")
		return confirm != nil && confirm.BoolValue()
	}
	switch selected.Name {
	case "view":
		detail, err := c.services.Cases.Get(ctx, staff, option("case"))
		if err != nil {
			return Message{}, err
		}
		return caseDetailMessage(detail), nil
	case "list":
		list, err := c.services.Cases.List(ctx, staff, quack.CaseListInput{Limit: strconv.Itoa(casePageSize)})
		if err != nil {
			return Message{}, err
		}
		return caseListMessage(list, 1, ""), nil
	case "user":
		targetID := option("user")
		profile, err := c.services.Cases.UserHistory(ctx, staff, targetID, quack.CaseListInput{Limit: strconv.Itoa(casePageSize)})
		if err != nil {
			return Message{}, err
		}
		return caseProfileMessage(profile, 1, targetID), nil
	case "failures":
		failed, err := c.services.Actions.ListFailures(ctx, staff, casePageSize, 0)
		if err != nil {
			return Message{}, err
		}
		return failedActionMessage(failed, 1), nil
	case "retry":
		if _, err := c.services.Actions.Retry(ctx, staff, option("execution")); err != nil {
			return Message{}, err
		}
		return Signal("retry", "Retry queued. Quack will check its permissions before trying again.", false), nil
	case "dismiss":
		if _, err := c.services.Actions.Dismiss(ctx, staff, option("execution")); err != nil {
			return Message{}, err
		}
		return Signal("review", "Failure dismissed. The attempt history is still on the case.", false), nil
	case "void":
		if !confirmed() {
			return Message{}, quack.ErrCaseValidation
		}
		voided, err := c.services.Cases.Void(ctx, staff, option("case"), option("reason"))
		if err != nil {
			return Message{}, err
		}
		return caseVoidedMessage(voided), nil
	case "reverse":
		if !confirmed() {
			return Message{}, quack.ErrCaseValidation
		}
		action := quack.ActionType(option("action"))
		if _, err := c.services.Actions.Reverse(ctx, staff, option("case"), option("execution"), action); err != nil {
			return Message{}, err
		}
		return Signal("retry", "Reversal queued. The original action stays in the case history.", false), nil
	default:
		return Message{}, quack.ErrCaseValidation
	}
}

// pageCases handles the Prev and Next buttons of a case list. The payload is
// "page|target", with an empty target for the guild-wide list.
func (c *cases) pageCases(delta int, user bool) Handler {
	return func(_ context.Context, i *discordgo.InteractionCreate) Result {
		id, err := DecodeCustomID(i.MessageComponentData().CustomID)
		if err != nil {
			return Immediate(Error("That case page is no longer available."))
		}
		parts := strings.SplitN(id.Payload, "|", 2)
		current, _ := strconv.Atoi(parts[0])
		page := max(current+delta, 1)
		targetID := ""
		if len(parts) == 2 {
			targetID = parts[1]
		}
		return Async(DeferUpdate(), func(ctx context.Context, responder Responder) error {
			staff, err := c.staff(ctx, i)
			if err != nil {
				return err
			}
			input := quack.CaseListInput{
				Limit:  strconv.Itoa(casePageSize),
				Offset: strconv.Itoa((page - 1) * casePageSize),
			}
			var message Message
			if user {
				profile, err := c.services.Cases.UserHistory(ctx, staff, targetID, input)
				if err != nil {
					return err
				}
				message = caseProfileMessage(profile, page, targetID)
			} else {
				list, err := c.services.Cases.List(ctx, staff, input)
				if err != nil {
					return err
				}
				message = caseListMessage(list, page, targetID)
			}
			_, err = responder.EditOriginal(EditMessage(message))
			return err
		})
	}
}

// pageFailures handles the Prev and Next buttons of the failed-action queue.
func (c *cases) pageFailures(delta int) Handler {
	return func(_ context.Context, i *discordgo.InteractionCreate) Result {
		id, err := DecodeCustomID(i.MessageComponentData().CustomID)
		if err != nil {
			return Immediate(Error("That failure page is no longer available."))
		}
		current, _ := strconv.Atoi(id.Payload)
		page := max(current+delta, 1)
		return Async(DeferUpdate(), func(ctx context.Context, responder Responder) error {
			staff, err := c.staff(ctx, i)
			if err != nil {
				return err
			}
			failed, err := c.services.Actions.ListFailures(ctx, staff, casePageSize, (page-1)*casePageSize)
			if err != nil {
				return err
			}
			_, err = responder.EditOriginal(EditMessage(failedActionMessage(failed, page)))
			return err
		})
	}
}

// actionControl handles the Retry and Dismiss buttons, whose payload is the
// execution ID, and then refreshes the failed-action queue in place.
func (c *cases) actionControl(operation string) Handler {
	return func(_ context.Context, i *discordgo.InteractionCreate) Result {
		id, err := DecodeCustomID(i.MessageComponentData().CustomID)
		if err != nil {
			return Immediate(Error("That action control is invalid."))
		}
		return Async(DeferUpdate(), func(ctx context.Context, responder Responder) error {
			staff, err := c.staff(ctx, i)
			if err != nil {
				return err
			}
			if operation == "retry" {
				_, err = c.services.Actions.Retry(ctx, staff, id.Payload)
			} else {
				_, err = c.services.Actions.Dismiss(ctx, staff, id.Payload)
			}
			if err != nil {
				return err
			}
			failed, err := c.services.Actions.ListFailures(ctx, staff, casePageSize, 0)
			if err != nil {
				return err
			}
			_, err = responder.EditOriginal(EditMessage(failedActionMessage(failed, 1)))
			return err
		})
	}
}

// voidButton asks for the correction reason before voiding the case in the
// payload.
func (c *cases) voidButton(_ context.Context, i *discordgo.InteractionCreate) Result {
	id, err := DecodeCustomID(i.MessageComponentData().CustomID)
	if err != nil || strings.TrimSpace(id.Payload) == "" {
		return Immediate(Error("That case control is invalid."))
	}
	customID := MustCustomID(CustomID{Namespace: "case", Action: "void_submit", Version: "v1", Payload: id.Payload})
	reason := discordgo.TextInput{
		CustomID: "reason", Label: "Why are you voiding this case?", Style: discordgo.TextInputParagraph,
		Required: true, MinLength: 3, MaxLength: 500,
	}
	return Immediate(Modal("Void case", customID, []discordgo.MessageComponent{Row(reason)}))
}

// voidModal voids the case once the moderator has given a reason, and posts
// the correction publicly. Errors go only to the moderator.
func (c *cases) voidModal(_ context.Context, i *discordgo.InteractionCreate) Result {
	data := i.ModalSubmitData()
	id, err := DecodeCustomID(data.CustomID)
	if err != nil {
		return Immediate(Error("That case control is invalid."))
	}
	reason := ModalValue(data, "reason")
	return AsyncPublic(func(ctx context.Context, responder Responder) error {
		staff, err := c.staff(ctx, i)
		if err != nil {
			return err
		}
		voided, err := c.services.Cases.Void(ctx, staff, id.Payload, reason)
		if err != nil {
			_, err := responder.EditOriginal(ErrorEdit(caseErrorMessage(err)))
			return err
		}
		_, err = Publish(responder, caseVoidedMessage(voided))
		return err
	})
}

// reverseButton asks the moderator to type REVERSE before reversing. The
// payload is "case|execution|reversal action".
func (c *cases) reverseButton(_ context.Context, i *discordgo.InteractionCreate) Result {
	id, err := DecodeCustomID(i.MessageComponentData().CustomID)
	if err != nil || len(strings.Split(id.Payload, "|")) != 3 {
		return Immediate(Error("That reversal control is invalid."))
	}
	customID := MustCustomID(CustomID{Namespace: "case", Action: "reverse_submit", Version: "v1", Payload: id.Payload})
	confirm := discordgo.TextInput{
		CustomID: "confirm", Label: "Type REVERSE to confirm", Style: discordgo.TextInputShort,
		Required: true, MinLength: 7, MaxLength: 7,
	}
	return Immediate(Modal("Confirm reversal", customID, []discordgo.MessageComponent{Row(confirm)}))
}

// reverseModal queues the reversal once the moderator has typed REVERSE.
// Permission and hierarchy are checked again when the reversal is queued.
func (c *cases) reverseModal(_ context.Context, i *discordgo.InteractionCreate) Result {
	data := i.ModalSubmitData()
	id, err := DecodeCustomID(data.CustomID)
	parts := strings.Split(id.Payload, "|")
	if err != nil || len(parts) != 3 || ModalValue(data, "confirm") != "REVERSE" {
		return Immediate(Error("Reversal confirmation did not match."))
	}
	return AsyncPublic(func(ctx context.Context, responder Responder) error {
		staff, err := c.staff(ctx, i)
		if err != nil {
			return err
		}
		if _, err := c.services.Actions.Reverse(ctx, staff, parts[0], parts[1], quack.ActionType(parts[2])); err != nil {
			_, err := responder.EditOriginal(ErrorEdit(caseErrorMessage(err)))
			return err
		}
		_, err = Publish(responder, Conversation("retry", "The reversal is queued.", "", "The original action stays in the case history.", "", false))
		return err
	})
}
