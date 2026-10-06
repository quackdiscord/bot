package discord

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

const (
	caseCommandName        = "case"
	messageCaseCommandName = "Add case"
	userCaseCommandName    = "Add case for member"

	// choiceLimit is the most choices Discord accepts in an autocomplete
	// answer or a select menu.
	choiceLimit = 25
)

// errNotInGuild rejects /case outside a server. Discord should never send
// one, since the command disables DMs.
var errNotInGuild = errors.New("case commands must be used in a server")

// commands returns Quack's application commands, freshly built so callers
// can modify them without affecting anyone else's copy.
func commands() []*discordgo.ApplicationCommand {
	return []*discordgo.ApplicationCommand{
		caseCommand(), messageCaseCommand(), userCaseCommand(), setupCommand(),
		templateCommand(), appealsCommand(), helpCommand(),
	}
}

// renamedCommands maps the old names of renamed commands to their current
// names. Sync deletes the old registration once the new one is in place,
// even when pruning is off, so moderators never see both.
var renamedCommands = map[string]string{
	"Create moderation case": messageCaseCommandName,
	"Create case for member": userCaseCommandName,
}

// caseCommand defines /case: add creates a case from a template, evidence
// adds to one, and the other subcommands browse cases and recover failed
// actions.
func caseCommand() *discordgo.ApplicationCommand {
	text := func(name, description string, required bool) *discordgo.ApplicationCommandOption {
		return &discordgo.ApplicationCommandOption{
			Type:        discordgo.ApplicationCommandOptionString,
			Name:        name,
			Description: description,
			Required:    required,
		}
	}
	file := func(description string) *discordgo.ApplicationCommandOption {
		return &discordgo.ApplicationCommandOption{
			Type:        discordgo.ApplicationCommandOptionAttachment,
			Name:        "file",
			Description: description,
		}
	}
	user := func(description string) *discordgo.ApplicationCommandOption {
		return &discordgo.ApplicationCommandOption{
			Type:        discordgo.ApplicationCommandOptionUser,
			Name:        "user",
			Description: description,
			Required:    true,
		}
	}
	confirm := func() *discordgo.ApplicationCommandOption {
		return &discordgo.ApplicationCommandOption{
			Type:        discordgo.ApplicationCommandOptionBoolean,
			Name:        "confirm",
			Description: "Confirm this irreversible control.",
			Required:    true,
		}
	}
	subcommand := func(name, description string, options ...*discordgo.ApplicationCommandOption) *discordgo.ApplicationCommandOption {
		return &discordgo.ApplicationCommandOption{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        name,
			Description: description,
			Options:     options,
		}
	}
	template := text("template", "Case template to apply.", true)
	template.Autocomplete = true
	reversal := text("action", "Reversal action.", true)
	reversal.Choices = []*discordgo.ApplicationCommandOptionChoice{
		{Name: "Remove timeout", Value: string(quack.ActionRemoveTimeout)},
		{Name: "Unban", Value: string(quack.ActionUnbanUser)},
	}
	return moderatorCommand(&discordgo.ApplicationCommand{
		Name:        caseCommandName,
		Description: "Create and manage moderation cases.",
		Options: []*discordgo.ApplicationCommandOption{
			subcommand("add", "Create a moderation case from a template.",
				template,
				user("User to moderate."),
				text("message_link", "Discord message link to capture as evidence.", false),
				file("Screenshot or file to save as evidence."),
			),
			subcommand("evidence", "View a case's evidence, or add a file or message link.",
				text("case", "Case number or ID", true),
				file("Screenshot or file to save."),
				text("message_link", "Discord message to preserve.", false),
			),
			subcommand("view", "View case details.", text("case", "Case number or ID.", true)),
			subcommand("list", "List recent guild cases."),
			subcommand("user", "View a member's case history.", user("Member to review.")),
			subcommand("failures", "Review failed Discord actions."),
			subcommand("retry", "Retry the same failed action.", text("execution", "Failed execution ID.", true)),
			subcommand("dismiss", "Dismiss a failure from active review.", text("execution", "Failed execution ID.", true)),
			subcommand("void", "Void an incorrect case.",
				text("case", "Case number or ID.", true),
				text("reason", "Required correction reason.", true),
				confirm(),
			),
			subcommand("reverse", "Remove a timeout or ban.",
				text("case", "Case ID.", true),
				text("execution", "Original execution ID.", true),
				reversal,
				confirm(),
			),
		},
	})
}

// messageCaseCommand defines the "Add case" message action, which starts a
// case against a message's author with the message as evidence.
func messageCaseCommand() *discordgo.ApplicationCommand {
	return moderatorCommand(&discordgo.ApplicationCommand{
		Type: discordgo.MessageApplicationCommand,
		Name: messageCaseCommandName,
	})
}

// userCaseCommand defines the "Add case for member" user action, which
// starts a case against the selected member without evidence; staff can add
// context and evidence afterwards.
func userCaseCommand() *discordgo.ApplicationCommand {
	return moderatorCommand(&discordgo.ApplicationCommand{
		Type: discordgo.UserApplicationCommand,
		Name: userCaseCommandName,
	})
}

// moderatorCommand limits command to guilds. It sets no default member
// permission: moderators may come from configured roles rather than
// Moderate Members, so Discord shows the command to every member and the
// handlers check Quack's live capabilities instead. Server admins can still
// narrow it in Discord's integration settings.
//
// DMPermission is deprecated in favor of Contexts, but it is what the
// registered commands use and it is part of their fingerprint. Switching
// would change every command's hash and re-register them for no change in
// behavior, so it stays until the definitions change for another reason.
func moderatorCommand(command *discordgo.ApplicationCommand) *discordgo.ApplicationCommand {
	dmAllowed := false
	//lint:ignore SA1019 see the comment above.
	command.DMPermission = &dmAllowed
	return command
}

// cases handles the /case command, the "Add case" context menus, and the
// case buttons and modals. Every handler resolves the acting staff member
// once and passes that context down, including into its deferred task.
type cases struct {
	services *quack.Services
	drafts   *draftStore
	// poster publishes standalone channel messages with bot credentials,
	// for results of context-menu flows whose interaction is private.
	poster channelPoster
	// dashboard builds the dashboard links on staff views and receipts.
	dashboard quack.DashboardLinks
	// followEvery and followFor pace how a private receipt, which the
	// publication refresher cannot edit, is kept current while its actions
	// settle (see publishPrivately).
	followEvery, followFor time.Duration
}

// channelPoster sends a message to a channel as the bot. *Bot implements it.
type channelPoster interface {
	Send(ctx context.Context, channelID string, message Message) (*discordgo.Message, error)
}

// newCases returns the case handlers with an empty draft store.
func newCases(services *quack.Services, poster channelPoster, dashboard quack.DashboardLinks) *cases {
	return &cases{
		services: services, drafts: newDraftStore(), poster: poster, dashboard: dashboard,
		followEvery: 2 * time.Second, followFor: 30 * time.Second,
	}
}

// register installs the case commands, components, and modals on r. The
// custom IDs are part of messages already posted in Discord, so they must
// not change.
func (c *cases) register(r *Router) {
	r.commands[caseCommandName] = c.command
	r.commands[messageCaseCommandName] = c.messageCommand
	r.commands[userCaseCommandName] = c.userCommand
	components := map[string]Handler{
		"list_prev":        c.pageCases(-1, false),
		"list_next":        c.pageCases(1, false),
		"user_prev":        c.pageCases(-1, true),
		"user_next":        c.pageCases(1, true),
		"failures_prev":    c.pageFailures(-1),
		"failures_next":    c.pageFailures(1),
		"retry":            c.actionControl(retryControl),
		"dismiss":          c.actionControl(dismissControl),
		"void":             c.voidButton,
		"reverse":          c.reverseButton,
		"template_page":    c.templatePage,
		"message_template": c.messageTemplate,
		"user_template":    c.userTemplate,
		"context_next":     c.contextNext,
		"edit_context":     c.editContextButton,
		"view":             c.viewButton,
		"detail_prev":      c.pageDetail(-1),
		"detail_next":      c.pageDetail(1),
		"evidence":         c.evidenceButton,
		"evidence_prev":    c.pageEvidence(-1),
		"evidence_next":    c.pageEvidence(1),
		"user_detail":      c.historyButton,
	}
	for action, handler := range components {
		r.HandleComponent("case", action, handler)
	}
	r.HandleModal("case", "void_submit", c.voidModal)
	r.HandleModal("case", "reverse_submit", c.reverseModal)
	r.HandleModal("case", "context_submit", c.contextModal)
	r.HandleModal("case", "edit_context_submit", c.editContextModal)
}

// command handles /case and its template autocomplete.
func (c *cases) command(ctx context.Context, i *discordgo.InteractionCreate) Result {
	if i.Type == discordgo.InteractionApplicationCommandAutocomplete {
		return Immediate(c.autocomplete(ctx, i))
	}
	data := i.ApplicationCommandData()
	if add := data.GetOption("add"); add != nil {
		return c.add(ctx, i, add)
	}
	return c.staffCommand(i, data)
}

// staff resolves who is acting, from live Discord state rather than the
// permissions the interaction carries; see liveStaff.
func (c *cases) staff(ctx context.Context, i *discordgo.InteractionCreate) (*quack.GuildStaffContext, error) {
	return liveStaff(ctx, c.services, i)
}

// template finds an active template by ID or slug. An unknown value comes
// back as the ID with a nil template, so case creation reports it.
func (c *cases) template(ctx context.Context, staff *quack.GuildStaffContext, value string) (string, *quack.TemplateResponse, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil, quack.ErrCaseValidation
	}
	templates, err := c.services.Templates.ListActive(ctx, staff)
	if err != nil {
		return "", nil, err
	}
	for _, template := range templates {
		if template.ID == value || strings.EqualFold(template.Slug, value) {
			return template.ID, &template, nil
		}
	}
	return value, nil, nil
}

// autocomplete suggests active templates matching what the moderator typed.
// It answers with no choices when they cannot create cases, without
// auditing: it runs on every keystroke, and the command itself audits a
// refusal once.
func (c *cases) autocomplete(ctx context.Context, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	staff, err := quietStaff(ctx, c.services, i)
	if err != nil || !staff.Can(quack.PermissionActionCaseCreate) {
		return Autocomplete(nil)
	}
	add := i.ApplicationCommandData().GetOption("add")
	if add == nil {
		return Autocomplete(nil)
	}
	option := add.GetOption("template")
	if option == nil {
		return Autocomplete(nil)
	}
	query := strings.ToLower(strings.TrimSpace(optionString(option)))
	templates, err := c.services.Templates.ListActive(ctx, staff)
	if err != nil {
		slog.Error("failed to list templates for case autocomplete", "error", err)
		return Autocomplete(nil)
	}
	choices := make([]*discordgo.ApplicationCommandOptionChoice, 0, choiceLimit)
	for _, template := range templates {
		search := strings.ToLower(template.Slug + " " + template.Name + " " + template.Description)
		if query != "" && !strings.Contains(search, query) {
			continue
		}
		choices = append(choices, &discordgo.ApplicationCommandOptionChoice{
			Name:  templateLabel(template),
			Value: template.ID,
		})
		if len(choices) == choiceLimit {
			break
		}
	}
	return Autocomplete(choices)
}

// templateLabel is "Name - Description", cut to Discord's 100-character
// choice limit.
func templateLabel(template quack.TemplateResponse) string {
	name := strings.TrimSpace(template.Name)
	if name == "" {
		name = strings.TrimSpace(template.Slug)
	}
	if description := strings.TrimSpace(template.Description); description != "" {
		name = fmt.Sprintf("%s - %s", name, description)
	}
	return Truncate(name, 100)
}

// interactionMember returns the invoking member's ID and display name.
func interactionMember(i *discordgo.InteractionCreate) (string, string) {
	if i.Member == nil || i.Member.User == nil {
		return "", ""
	}
	return i.Member.User.ID, displayName(i.Member)
}

// optionString returns an option's value as text, or "" when the option is
// missing. Non-string values, such as a user option, are formatted.
func optionString(option *discordgo.ApplicationCommandInteractionDataOption) string {
	if option == nil || option.Value == nil {
		return ""
	}
	if value, ok := option.Value.(string); ok {
		return value
	}
	return strings.TrimSpace(fmt.Sprint(option.Value))
}
