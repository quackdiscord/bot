package discord

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

// templateManagementOptions defines the /template subcommands for existing
// rules. Archiving keeps a rule's history; restoring brings the same rule
// back.
func templateManagementOptions() []*discordgo.ApplicationCommandOption {
	specs := []struct{ name, description string }{
		{"view", "Show a rule and its escalation outcomes"},
		{"edit", "Edit a rule"},
		{"remove-level", "Remove a punishment step from a rule"},
		{"archive", "Stop using a rule for new cases; keep its history"},
		{"restore", "Make an archived rule available again"},
	}
	options := make([]*discordgo.ApplicationCommandOption, 0, len(specs))
	for _, spec := range specs {
		option := &discordgo.ApplicationCommandOption{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        spec.name,
			Description: spec.description,
			Options:     []*discordgo.ApplicationCommandOption{templateOption("Rule to manage")},
		}
		switch spec.name {
		case "edit":
			option.Options = append(option.Options,
				&discordgo.ApplicationCommandOption{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "name",
					Description: "Rule name",
					MaxLength:   100,
				},
				&discordgo.ApplicationCommandOption{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "reason",
					Description: "Reason shown to the member",
					MaxLength:   1000,
				},
				&discordgo.ApplicationCommandOption{
					Type:        discordgo.ApplicationCommandOptionBoolean,
					Name:        "appeals",
					Description: "Allow members to appeal new cases under this rule",
				},
				&discordgo.ApplicationCommandOption{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "decay-days",
					Description: "How long cases should be counted for. 0=forever",
					MinValue:    floatPointer(0),
					MaxValue:    quack.MaxCaseDecayDays,
				},
			)
		case "remove-level":
			option.Options = append(option.Options, &discordgo.ApplicationCommandOption{
				Type:        discordgo.ApplicationCommandOptionInteger,
				Name:        "case",
				Description: "Which step? Use its case count from /template view, e.g. 3",
				Required:    true,
				MinValue:    floatPointer(2),
				MaxValue:    maxTriggerCase,
			})
		}
		options = append(options, option)
	}
	return options
}

// manage runs view, edit, remove-level, archive, and restore. Edits start
// from the version that was read, so a concurrent change is reported
// instead of overwritten, and they never alter existing cases.
func (t templates) manage(i *discordgo.InteractionCreate, option *discordgo.ApplicationCommandInteractionDataOption) Result {
	ref := option.GetOption("template")
	if ref == nil {
		return Immediate(Error("Choose a rule first."))
	}
	return AsyncPublic(func(ctx context.Context, responder Responder) error {
		fail := func(text string) error {
			_, err := responder.EditOriginal(ErrorEdit(text))
			return err
		}
		staff, err := liveStaff(ctx, t.services, i)
		if err != nil || !staff.Can(quack.PermissionActionCaseTemplateWrite) {
			return fail(staffDenied(err, rulesPermissionMessage("manage rules"), rulesPermissionMessage("manage rules")))
		}
		all, err := t.services.Templates.List(ctx, staff)
		if err != nil {
			return fail("I couldn’t load the rules. Try again in a moment.")
		}
		value := strings.TrimSpace(ref.StringValue())
		index := slices.IndexFunc(all, func(template quack.TemplateResponse) bool {
			return template.ID == value || strings.EqualFold(template.Slug, value)
		})
		if index < 0 {
			return fail("I can’t find that rule. Choose it from the command’s suggestions.")
		}
		selected := all[index]
		input := selected.EditInput()
		var message string
		switch option.Name {
		case "view":
			_, err := Publish(responder, t.withRuleLink(templatePolicyMessage(selected), i, selected.ID))
			return err
		case "archive":
			_, err = t.services.Templates.Archive(ctx, staff, selected.ID)
			message = fmt.Sprintf("**%s** is archived. Its history is kept, and it cannot be used for new cases.", PlainText(selected.Name))
		case "restore":
			_, err = t.services.Templates.Restore(ctx, staff, selected.ID)
			message = fmt.Sprintf("**%s** is available for new cases again.", PlainText(selected.Name))
		case "edit":
			changed := false
			if value := option.GetOption("name"); value != nil {
				input.Name, changed = strings.TrimSpace(value.StringValue()), true
			}
			if value := option.GetOption("reason"); value != nil {
				input.ReasonTemplate, changed = strings.TrimSpace(value.StringValue()), true
			}
			if value := option.GetOption("appeals"); value != nil {
				input.Appealable, changed = value.BoolValue(), true
			}
			if value := option.GetOption("decay-days"); value != nil {
				input.CaseDecayDays, changed = int(value.IntValue()), true
			}
			if !changed {
				return fail("Set a name, reason, appeals choice or decay window to change.")
			}
			if input.Name == "" || input.ReasonTemplate == "" {
				return fail("The rule needs a name and a member reason.")
			}
			var updated *quack.TemplateResponse
			if updated, err = t.services.Templates.Update(ctx, staff, selected.ID, input); err == nil {
				message = fmt.Sprintf("**%s** updated. These settings apply to new cases.", PlainText(updated.Name))
			}
		case "remove-level":
			value := option.GetOption("case")
			if value == nil || value.IntValue() < 2 || value.IntValue() > maxTriggerCase {
				return fail("Choose a step from case 2 onward. To change the first step, use `/template level`.")
			}
			count := int(value.IntValue())
			index := slices.IndexFunc(input.Levels, func(level quack.TemplateLevelInput) bool {
				return !level.IsDefault && level.TriggerCaseCount == count
			})
			if index < 0 {
				return fail("There is no step at that count. Check `/template view` for this rule’s steps.")
			}
			input.Levels = slices.Delete(input.Levels, index, index+1)
			_, err = t.services.Templates.Update(ctx, staff, selected.ID, input)
			message = fmt.Sprintf("Removed the **%d-case** step. Check `/template view` for the updated rule.", count)
		default:
			return fail("Choose a template operation.")
		}
		if errors.Is(err, quack.ErrTemplateConflict) {
			return fail(errTemplateConflict)
		}
		if err != nil {
			return fail("I couldn’t save that change. Check the options and try again.")
		}
		_, err = Publish(responder, t.withRuleLink(Signal("settings", message, true), i, selected.ID))
		return err
	})
}

// templatePolicyMessage shows a rule the way admins configure it: each step
// by the case count it starts at, its outcome, and whether it DMs, then the
// counting window and whether appeals are on. Internal IDs and worker
// settings are left out.
func templatePolicyMessage(template quack.TemplateResponse) Message {
	status := "Active"
	if template.ArchivedAt != nil {
		status = "Archived — unavailable for new cases"
	}
	appeals := "Off"
	if template.Appealable {
		appeals = "On"
	}
	lines := []string{
		fmt.Sprintf("**%s** · %s", PlainText(template.Name), status),
		PlainText(template.ReasonTemplate),
		"",
		"**When someone breaks this rule**",
	}
	levels := slices.Clone(template.Levels)
	slices.SortStableFunc(levels, func(a, b quack.TemplateLevelResponse) int {
		switch {
		case a.IsDefault != b.IsDefault && a.IsDefault:
			return -1
		case a.IsDefault != b.IsDefault:
			return 1
		}
		return a.TriggerCaseCount - b.TriggerCaseCount
	})
	for _, level := range levels {
		count := level.TriggerCaseCount
		if level.IsDefault {
			count = 1
		}
		outcome := "Warning"
		if len(level.Actions) > 0 {
			action := level.Actions[0]
			outcome = action.ActionType.Label()
			if action.ActionType == quack.ActionTimeoutUser {
				if action.TimeoutDurationSeconds%60 == 0 {
					outcome += fmt.Sprintf(" (%d minutes)", action.TimeoutDurationSeconds/60)
				} else {
					outcome += fmt.Sprintf(" (%d seconds)", action.TimeoutDurationSeconds)
				}
			}
		}
		dm := "DM off"
		if level.NotifyUser {
			dm = "DM on"
		}
		lines = append(lines, fmt.Sprintf("**%d+ times:** %s · %s", count, outcome, dm))
	}
	window := "-# Counting all cases for this rule."
	if template.CaseDecayDays > 0 {
		window = fmt.Sprintf("-# Counting cases from the last **%d days**.", template.CaseDecayDays)
	}
	lines = append(lines, "", window, "-# Appeals: **"+appeals+"**", "Need a hand? `/help topic:rules`")
	return Signal("settings", strings.Join(lines, "\n"), true)
}
