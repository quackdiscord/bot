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

const (
	templateCommandName = "template"
	templateNamespace   = "template"

	// maxTimeoutMinutes is Discord's longest timeout, 28 days.
	maxTimeoutMinutes = 40320
	// maxTriggerCase bounds the case count a level can start at.
	maxTriggerCase = 1000000
)

// errTemplateConflict is the reply when someone else saved the template
// first, which TemplateService reports through ExpectedVersion.
const errTemplateConflict = "Someone changed this template while you were editing. Run the command again to use the latest settings."

// templateCommand defines /template, which lets server managers write the
// rules moderators apply with /case add: create opens a short form, level
// sets the outcome for a repeat count, and the rest view and maintain a
// rule. Creating cases stays with /case.
func templateCommand() *discordgo.ApplicationCommand {
	options := append(templateManagementOptions(), templateLevelOption(), &discordgo.ApplicationCommandOption{
		Type:        discordgo.ApplicationCommandOptionSubCommand,
		Name:        "create",
		Description: "Create a rule with a default outcome",
		Options: []*discordgo.ApplicationCommandOption{
			outcomeOption("Default outcome; warning if omitted", false),
			minutesOption("Timeout length in minutes (required for a timeout)"),
		},
	})
	// Like moderatorCommand, no default member permission: rules managers
	// hold a configured role rather than Manage Server.
	return moderatorCommand(&discordgo.ApplicationCommand{
		Name:        templateCommandName,
		Description: "Create and manage moderation rules",
		Options:     options,
	})
}

// rulesPermissionMessage tells someone who cannot manage templates what they
// need to do what, such as "edit rules".
func rulesPermissionMessage(what string) string {
	return "You need Manage Server or a rules manager role to " + what + "."
}

// outcomeOption is the choice of what a level does.
func outcomeOption(description string, required bool) *discordgo.ApplicationCommandOption {
	return &discordgo.ApplicationCommandOption{
		Type:        discordgo.ApplicationCommandOptionString,
		Name:        "outcome",
		Description: description,
		Required:    required,
		Choices: []*discordgo.ApplicationCommandOptionChoice{
			{Name: "Warning", Value: "warning"},
			{Name: "Timeout", Value: "timeout"},
			{Name: "Kick", Value: "kick"},
			{Name: "Ban", Value: "ban"},
		},
	}
}

// minutesOption is a timeout's length.
func minutesOption(description string) *discordgo.ApplicationCommandOption {
	return &discordgo.ApplicationCommandOption{
		Type:        discordgo.ApplicationCommandOptionInteger,
		Name:        "minutes",
		Description: description,
		MinValue:    floatPointer(1),
		MaxValue:    maxTimeoutMinutes,
	}
}

// templateOption is the autocompleted rule a subcommand works on.
func templateOption(description string) *discordgo.ApplicationCommandOption {
	return &discordgo.ApplicationCommandOption{
		Type:         discordgo.ApplicationCommandOptionString,
		Name:         "template",
		Description:  description,
		Required:     true,
		Autocomplete: true,
	}
}

// templateLevelOption defines /template level.
func templateLevelOption() *discordgo.ApplicationCommandOption {
	return &discordgo.ApplicationCommandOption{
		Type:        discordgo.ApplicationCommandOptionSubCommand,
		Name:        "level",
		Description: "Choose the punishment after repeated rule breaks",
		Options: []*discordgo.ApplicationCommandOption{
			templateOption("Rule to edit"),
			{
				Type:        discordgo.ApplicationCommandOptionInteger,
				Name:        "case",
				Description: "How many times? 1 is the first case, 3 is the third",
				Required:    true,
				MinValue:    floatPointer(1),
				MaxValue:    maxTriggerCase,
			},
			outcomeOption("What should happen?", true),
			minutesOption("Timeout length in minutes"),
			{
				Type:        discordgo.ApplicationCommandOptionBoolean,
				Name:        "notify",
				Description: "Send the member a DM at this level; defaults to on for new levels",
			},
		},
	}
}

// floatPointer returns a pointer to value, for Discord's optional minimums.
func floatPointer(value float64) *float64 { return &value }

// templates handles /template and its create form.
type templates struct {
	services *quack.Services
	// dashboard links each result to the rule's dashboard page.
	dashboard quack.DashboardLinks
}

// withRuleLink adds an "Open rule" button for the rule templateID in the
// interaction's guild.
func (t templates) withRuleLink(message Message, i *discordgo.InteractionCreate, templateID string) Message {
	return withLink(message, t.dashboard.Staff(i.GuildID, "rules", templateID), dashboardRuleLabel)
}

// register installs /template and its create form on r.
func (t templates) register(r *Router) {
	r.commands[templateCommandName] = t.command
	r.HandleModal(templateNamespace, "create", t.createSubmit)
}

// command routes /template to its subcommand. create opens its form at
// once, without network calls; the submit checks permissions.
func (t templates) command(ctx context.Context, i *discordgo.InteractionCreate) Result {
	if i.GuildID == "" {
		return Immediate(Error("Create templates in your server."))
	}
	if i.Type == discordgo.InteractionApplicationCommandAutocomplete {
		return Immediate(t.autocomplete(ctx, i))
	}
	data := i.ApplicationCommandData()
	if level := data.GetOption("level"); level != nil {
		return t.level(i, level)
	}
	for _, option := range data.Options {
		switch option.Name {
		case "view", "edit", "remove-level", "archive", "restore":
			return t.manage(i, option)
		}
	}
	create := data.GetOption("create")
	if create == nil {
		return Immediate(Error("Choose a template operation."))
	}
	outcome := "warning"
	if option := create.GetOption("outcome"); option != nil {
		outcome = option.StringValue()
	}
	var minutes int64
	if option := create.GetOption("minutes"); option != nil {
		minutes = option.IntValue()
	}
	if !validOutcome(outcome, minutes) {
		return Immediate(Error("For a timeout, set minutes between 1 and 40320. Other outcomes do not use minutes."))
	}
	id := MustCustomID(CustomID{Namespace: templateNamespace, Action: "create", Version: "v1", Payload: fmt.Sprintf("%s|%d", outcome, minutes)})
	return Immediate(Modal("Create a rule", id, []discordgo.MessageComponent{
		Row(discordgo.TextInput{
			CustomID:    "name",
			Label:       "Rule name",
			Placeholder: "NSFW chatting",
			Style:       discordgo.TextInputShort,
			Required:    true,
			MaxLength:   100,
		}),
		Row(discordgo.TextInput{
			CustomID:    "reason",
			Label:       "Reason shown to the member",
			Placeholder: "Keep explicit content out of chat.",
			Style:       discordgo.TextInputParagraph,
			Required:    true,
			MaxLength:   1000,
		}),
	}))
}

// validOutcome rejects forged or mismatched outcome and minutes options.
func validOutcome(outcome string, minutes int64) bool {
	switch outcome {
	case "timeout":
		return minutes >= 1 && minutes <= maxTimeoutMinutes
	case "warning", "kick", "ban":
		return minutes == 0
	default:
		return false
	}
}

// outcomeActions is the level action for outcome; a warning has none.
func outcomeActions(outcome string, minutes int64) []quack.TemplateActionInput {
	var action quack.ActionType
	switch outcome {
	case "timeout":
		action = quack.ActionTimeoutUser
	case "kick":
		action = quack.ActionKickUser
	case "ban":
		action = quack.ActionBanUser
	default:
		return nil
	}
	return []quack.TemplateActionInput{{ActionType: action, TimeoutDurationSeconds: int(minutes * 60)}}
}

// createSubmit creates the rule from the form with one notifying default
// level, ready for /case add straight away.
func (t templates) createSubmit(_ context.Context, i *discordgo.InteractionCreate) Result {
	if i.GuildID == "" {
		return Immediate(Error("Create templates in your server."))
	}
	data := i.ModalSubmitData()
	id, err := DecodeCustomID(data.CustomID)
	outcome, minutesText, found := strings.Cut(id.Payload, "|")
	if err != nil || !found {
		return Immediate(Error("That template form is unavailable."))
	}
	minutes, err := strconv.ParseInt(minutesText, 10, 64)
	if err != nil || !validOutcome(outcome, minutes) {
		return Immediate(Error("That template outcome is invalid."))
	}
	name, reason := strings.TrimSpace(ModalValue(data, "name")), strings.TrimSpace(ModalValue(data, "reason"))
	if name == "" || reason == "" {
		return Immediate(Error("Enter a rule name and a reason."))
	}
	return AsyncPublic(func(ctx context.Context, responder Responder) error {
		staff, err := liveStaff(ctx, t.services, i)
		if err != nil || !staff.Can(quack.PermissionActionCaseTemplateWrite) {
			_, err = responder.EditOriginal(ErrorEdit(staffDenied(err, rulesPermissionMessage("create rules"), rulesPermissionMessage("create rules"))))
			return err
		}
		created, err := t.services.Templates.Create(ctx, staff, quack.TemplateInput{
			Slug:           "rule-" + i.ID,
			Name:           name,
			ReasonTemplate: reason,
			Appealable:     true,
			Levels: []quack.TemplateLevelInput{{
				Name:       "Default",
				Position:   1,
				IsDefault:  true,
				NotifyUser: true,
				Actions:    outcomeActions(outcome, minutes),
			}},
		})
		if err != nil {
			_, err = responder.EditOriginal(ErrorEdit("Could not create the template. Check the form and try again."))
			return err
		}
		label := outcome
		if outcome == "timeout" {
			label = fmt.Sprintf("%d-minute timeout", minutes)
		}
		_, err = Publish(responder, t.withRuleLink(Signal("settings", fmt.Sprintf(
			"**%s** is ready. First case: **%s**.\nUse `/case add` when someone breaks this rule.",
			PlainText(created.Name), label,
		), true), i, created.ID))
		return err
	})
}

// autocomplete suggests rules for the subcommand being typed: archived ones
// only for restore, both kinds for view and edit, and active ones otherwise.
// It answers with nothing, and audits nothing, to anyone who cannot manage
// templates; see quietStaff.
func (t templates) autocomplete(ctx context.Context, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	options := i.ApplicationCommandData().Options
	if len(options) != 1 || options[0].GetOption("template") == nil {
		return Autocomplete(nil)
	}
	subcommand := options[0]
	staff, err := quietStaff(ctx, t.services, i)
	if err != nil || !staff.Can(quack.PermissionActionCaseTemplateWrite) {
		return Autocomplete(nil)
	}
	all, err := t.services.Templates.List(ctx, staff)
	if err != nil {
		return Autocomplete(nil)
	}
	query := strings.ToLower(subcommand.GetOption("template").StringValue())
	choices := []*discordgo.ApplicationCommandOptionChoice{}
	for _, template := range all {
		archived := template.ArchivedAt != nil
		switch subcommand.Name {
		case "restore":
			if !archived {
				continue
			}
		case "view", "edit":
		default:
			if archived {
				continue
			}
		}
		if !strings.Contains(strings.ToLower(template.Name+" "+template.Slug), query) {
			continue
		}
		choices = append(choices, &discordgo.ApplicationCommandOptionChoice{Name: templateLabel(template), Value: template.ID})
		if len(choices) == choiceLimit {
			break
		}
	}
	return Autocomplete(choices)
}

// level sets the outcome from the count-th case onward, replacing a level
// already at that count and keeping every other level and rule setting.
// Case 1 is the default level. Existing cases keep their snapshot.
func (t templates) level(i *discordgo.InteractionCreate, option *discordgo.ApplicationCommandInteractionDataOption) Result {
	ref, countOption, outcomeOption := option.GetOption("template"), option.GetOption("case"), option.GetOption("outcome")
	if ref == nil || countOption == nil || outcomeOption == nil {
		return Immediate(Error("Choose a template, case number and outcome."))
	}
	count, outcome := countOption.IntValue(), outcomeOption.StringValue()
	var minutes int64
	if value := option.GetOption("minutes"); value != nil {
		minutes = value.IntValue()
	}
	if count < 1 || count > maxTriggerCase || !validOutcome(outcome, minutes) {
		return Immediate(Error("Check the case number and timeout minutes."))
	}
	notify := option.GetOption("notify")
	return AsyncPublic(func(ctx context.Context, responder Responder) error {
		fail := func(text string) error {
			_, err := responder.EditOriginal(ErrorEdit(text))
			return err
		}
		staff, err := liveStaff(ctx, t.services, i)
		if err != nil || !staff.Can(quack.PermissionActionCaseTemplateWrite) {
			return fail(staffDenied(err, rulesPermissionMessage("edit rules"), rulesPermissionMessage("edit rules")))
		}
		template, err := t.activeTemplate(ctx, staff, ref.StringValue())
		if err != nil || template == nil {
			return fail("That active template is unavailable.")
		}
		policy := template.EditInput()
		level := quack.TemplateLevelInput{
			Name:             fmt.Sprintf("Case %d onward", count),
			TriggerCaseCount: int(count),
			NotifyUser:       true,
			Actions:          outcomeActions(outcome, minutes),
		}
		if count == 1 {
			level.IsDefault, level.TriggerCaseCount, level.Name = true, 0, "Default"
		}
		same := func(existing quack.TemplateLevelInput) bool {
			return existing.IsDefault == level.IsDefault && (level.IsDefault || existing.TriggerCaseCount == level.TriggerCaseCount)
		}
		replaced := false
		for index, existing := range policy.Levels {
			if same(existing) {
				level.Name, level.Position, level.NotifyUser = existing.Name, existing.Position, existing.NotifyUser
				policy.Levels[index] = level
				replaced = true
				break
			}
		}
		if !replaced {
			level.Position = len(policy.Levels) + 1
			policy.Levels = append(policy.Levels, level)
		}
		if notify != nil {
			for index := range policy.Levels {
				if same(policy.Levels[index]) {
					policy.Levels[index].NotifyUser = notify.BoolValue()
					break
				}
			}
		}
		_, err = t.services.Templates.Update(ctx, staff, template.ID, policy)
		if errors.Is(err, quack.ErrTemplateConflict) {
			return fail(errTemplateConflict)
		}
		if err != nil {
			return fail("Could not save that level. Check the outcome and try again.")
		}
		text := fmt.Sprintf("**%s:** **%s** after **%d** cases.", PlainText(template.Name), outcome, count)
		if outcome == "timeout" {
			unit := "minutes"
			if minutes == 1 {
				unit = "minute"
			}
			text += fmt.Sprintf(" Timeout: %d %s.", minutes, unit)
		}
		if notify != nil {
			if notify.BoolValue() {
				text += " I’ll DM the member."
			} else {
				text += " The member will not be notified."
			}
		}
		_, err = Publish(responder, t.withRuleLink(Signal("settings", text, true), i, template.ID))
		return err
	})
}

// activeTemplate finds an active template by ID or slug, or returns nil.
func (t templates) activeTemplate(ctx context.Context, staff *quack.GuildStaffContext, value string) (*quack.TemplateResponse, error) {
	value = strings.TrimSpace(value)
	active, err := t.services.Templates.ListActive(ctx, staff)
	if err != nil {
		return nil, err
	}
	for _, template := range active {
		if template.ID == value || strings.EqualFold(template.Slug, value) {
			return &template, nil
		}
	}
	return nil, nil
}
