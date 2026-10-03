package discord

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

const setupCommandName = "setup"

// SetupRequest is one module's /setup subcommand, from a member Quack has
// just confirmed holds Manage Server.
type SetupRequest struct {
	// Guild is the caller's live staff context.
	Guild *quack.GuildStaffContext
	// UserID is the caller's Discord user ID.
	UserID string
	// Options are the subcommand's options.
	Options []*discordgo.ApplicationCommandInteractionDataOption
}

// String returns the named option's text, or "" when it was not given.
func (r SetupRequest) String(name string) string {
	for _, option := range r.Options {
		if option != nil && option.Name == name {
			return optionString(option)
		}
	}
	return ""
}

// SetupHandler configures a module from its /setup subcommand and returns
// the public confirmation. It runs in a deferred task. A *UserError's copy
// is shown to the caller privately; any other error gets generic copy.
type SetupHandler func(ctx context.Context, request SetupRequest) (Message, error)

// HandleSetup routes the /setup subcommand named subcommand to handler.
// Subcommands with an enabled option switch the module on or off through
// the settings service instead, without calling handler. Like
// HandleComponent, it panics on a duplicate or empty route.
func (r *Router) HandleSetup(subcommand string, handler SetupHandler) {
	switch {
	case handler == nil || subcommand == "":
		panic("discord: invalid setup route " + subcommand)
	case r.setups[subcommand] != nil:
		panic("discord: duplicate setup route " + subcommand)
	}
	r.setups[subcommand] = handler
}

// setupModules names the modules /setup can switch on and off, with the
// display name the confirmation uses.
var setupModules = map[string]string{
	"tickets":  "Tickets",
	"honeypot": "Honeypot",
	"logging":  "Logging",
}

// setupCommand defines /setup, which configures Quack without the
// dashboard. Every subcommand creates a suitable channel when none is
// given.
func setupCommand() *discordgo.ApplicationCommand {
	channel := func(name, description string) *discordgo.ApplicationCommandOption {
		return &discordgo.ApplicationCommandOption{
			Type:         discordgo.ApplicationCommandOptionChannel,
			Name:         name,
			Description:  description,
			ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildText},
		}
	}
	enabled := func() *discordgo.ApplicationCommandOption {
		return &discordgo.ApplicationCommandOption{
			Type:        discordgo.ApplicationCommandOptionBoolean,
			Name:        "enabled",
			Description: "Enable or disable the saved setup; use this option on its own",
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
	existing := "Use an existing channel; otherwise Quack creates one"
	permissions := int64(discordgo.PermissionManageGuild)
	dmAllowed := false
	return &discordgo.ApplicationCommand{
		Name:                     setupCommandName,
		Description:              "Configure Quack for this server",
		DefaultMemberPermissions: &permissions,
		//lint:ignore SA1019 see moderatorCommand.
		DMPermission: &dmAllowed,
		Options: []*discordgo.ApplicationCommandOption{
			subcommand("appeals", "Set up appeal reviews",
				channel("channel", existing),
				&discordgo.ApplicationCommandOption{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "rejoin",
					Description: "Discord invite for accepted appeals; use none to remove it",
					MaxLength:   256,
				},
				&discordgo.ApplicationCommandOption{
					Type:        discordgo.ApplicationCommandOptionBoolean,
					Name:        "require-reason",
					Description: "Require moderators to write a decision reason; the member receives it",
				},
			),
			subcommand("tickets", "Set up private support tickets",
				channel("entry", "Use an existing entry channel; otherwise Quack creates one"),
				channel("queue", "Use an existing queue; otherwise Quack creates one"),
				enabled(),
			),
			subcommand("honeypot", "Create or update the honeypot trap",
				channel("channel", existing),
				&discordgo.ApplicationCommandOption{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "warning",
					Description: "Warning shown in the trap channel",
					MaxLength:   1500,
				},
				enabled(),
			),
			subcommand("logging", "Set up Discord event logs",
				channel("channel", existing),
				enabled(),
			),
			subcommand("audit", "Set up moderation history", channel("channel", existing)),
		},
	}
}

// setup handles /setup. It owns the appeal and audit destinations, which
// are core settings, and the module switches; everything else about a
// module is its SetupHandler's business.
type setup struct {
	session  *discordgo.Session
	services *quack.Services
	modules  map[string]SetupHandler
}

// command dispatches /setup by subcommand. Each runs as a public deferred
// task that re-checks Manage Server from live Discord state first.
func (s *setup) command(_ context.Context, i *discordgo.InteractionCreate) Result {
	if i.GuildID == "" {
		return Immediate(Error("Run setup in your server."))
	}
	options := i.ApplicationCommandData().Options
	if len(options) != 1 || options[0] == nil {
		return Immediate(Error("Choose which feature to set up."))
	}
	command := options[0]
	switch name := command.Name; {
	case name == "appeals":
		return s.run(i, "set up appeals", s.appeals(command))
	case name == "audit":
		return s.run(i, "change the audit channel", s.audit(command))
	case setupModules[name] != "" && command.GetOption("enabled") != nil:
		if len(command.Options) != 1 {
			return Immediate(Error("Use enabled on its own. To change channels or the warning, run setup separately without enabled."))
		}
		enabled, ok := command.Options[0].Value.(bool)
		if !ok {
			return Immediate(Error("Choose true or false for enabled."))
		}
		return s.run(i, "turn features on or off", s.toggle(name, enabled))
	case s.modules[name] != nil:
		handler := s.modules[name]
		return s.run(i, "set up "+name, func(ctx context.Context, staff *quack.GuildStaffContext, userID string) (Message, error) {
			return handler(ctx, SetupRequest{Guild: staff, UserID: userID, Options: command.Options})
		})
	default:
		return Immediate(Error("This setup feature is unavailable."))
	}
}

// setupTask is the work of one subcommand once Manage Server is confirmed.
type setupTask func(ctx context.Context, staff *quack.GuildStaffContext, userID string) (Message, error)

// run resolves the caller's live authority, requires Manage Server, runs
// task, and publishes its confirmation. what completes "You need Manage
// Server permission to ...".
func (s *setup) run(i *discordgo.InteractionCreate, what string, task setupTask) Result {
	userID, name := interactionMember(i)
	return AsyncPublic(func(ctx context.Context, responder Responder) error {
		staff, err := s.services.Guilds.ResolveDiscordStaffContext(ctx, quack.DiscordStaffContextInput{
			DiscordGuildID: i.GuildID,
			DiscordUserID:  userID,
			DisplayName:    name,
		})
		if err != nil {
			_, err = responder.EditOriginal(ErrorEdit("Could not verify your server permissions. Try again."))
			return err
		}
		if !staff.Can(quack.PermissionActionGuildSettingsWrite) {
			_, err = responder.EditOriginal(ErrorEdit(fmt.Sprintf("You need Manage Server permission to %s.", what)))
			return err
		}
		message, err := task(ctx, staff, userID)
		if err != nil {
			text, ok := UserMessage(err)
			if !ok {
				text = "Could not finish setup. Try again."
			}
			_, err = responder.EditOriginal(ErrorEdit(text))
			return err
		}
		_, err = Publish(responder, message)
		return err
	})
}

// appeals points appeal reviews at a staff channel, creating #appeals when
// none is given, and sets or clears the rejoin invite.
func (s *setup) appeals(command *discordgo.ApplicationCommandInteractionDataOption) setupTask {
	specified := optionString(command.GetOption("channel"))
	rejoin := command.GetOption("rejoin")
	var requireReason any
	if option := command.GetOption("require-reason"); option != nil {
		requireReason = option.Value
	}
	return func(ctx context.Context, staff *quack.GuildStaffContext, _ string) (Message, error) {
		settings, err := s.services.Settings.Get(ctx, staff)
		if err != nil {
			return Message{}, &UserError{Message: "Could not load appeal settings. Try again."}
		}
		channelID, err := SetupChannel(ctx, s.session, staff.Guild.DiscordGuildID,
			specified, settings.AppealQueueChannelDiscordID, "appeals", SetupStaffChannel)
		if err != nil {
			return Message{}, err
		}
		input := quack.GuildSettingsInput{AppealQueueChannelDiscordID: &channelID}
		if rejoin != nil {
			value := strings.TrimSpace(optionString(rejoin))
			if strings.EqualFold(value, "none") {
				value = ""
			}
			input.AppealRejoinURL = &value
		}
		if required, ok := requireReason.(bool); ok {
			input.AppealReviewReasonRequired = &required
		}
		saved, err := s.services.Settings.Update(ctx, staff, input)
		if err != nil {
			return Message{}, &UserError{Message: "Could not save appeal settings. Check Manage Server permission, Quack's queue channel permissions, and the HTTPS Discord invite (or none)."}
		}
		confirmation := fmt.Sprintf(
			"Appeal reviews will go to <#%s>. Members can appeal from their case DM; moderators can accept or reject in this channel.",
			channelID)
		if saved.AppealReviewReasonRequired {
			confirmation += " Moderators must write a decision reason; the member receives it."
		} else {
			confirmation += " Decision reasons are optional."
		}
		return Signal("settings", confirmation, false), nil
	}
}

// audit points the audit mirror at a staff channel, creating
// #moderation-log when none is given.
func (s *setup) audit(command *discordgo.ApplicationCommandInteractionDataOption) setupTask {
	specified := optionString(command.GetOption("channel"))
	return func(ctx context.Context, staff *quack.GuildStaffContext, _ string) (Message, error) {
		settings, err := s.services.Settings.Get(ctx, staff)
		if err != nil {
			return Message{}, &UserError{Message: "Could not load audit settings. Try again."}
		}
		channelID, err := SetupChannel(ctx, s.session, staff.Guild.DiscordGuildID,
			specified, settings.AuditMirrorChannelDiscordID, "moderation-log", SetupStaffChannel)
		if err != nil {
			return Message{}, err
		}
		_, err = s.services.Settings.Update(ctx, staff, quack.GuildSettingsInput{AuditMirrorChannelDiscordID: &channelID})
		switch {
		case errors.Is(err, quack.ErrGuildSettingsPermissionDenied):
			return Message{}, &UserError{Message: "You need Manage Server permission to change the audit channel."}
		case errors.Is(err, quack.ErrGuildSettingsValidation):
			return Message{}, &UserError{Message: "Choose a text channel in this server where Quack can view, send, read history and attach files."}
		case err != nil:
			return Message{}, &UserError{Message: "Could not save the audit channel. Try again."}
		}
		return Signal("settings", fmt.Sprintf(
			"Moderation history will go to <#%s>: cases, action outcomes, appeals, tickets and settings changes.",
			channelID), false), nil
	}
}

// toggle switches a module on or off through the settings service, which
// checks the module's saved setup against live Discord before switching it
// on. Switching off keeps the setup.
func (s *setup) toggle(module string, enabled bool) setupTask {
	return func(ctx context.Context, staff *quack.GuildStaffContext, _ string) (Message, error) {
		var input quack.GuildSettingsInput
		switch module {
		case "tickets":
			input.TicketsEnabled = &enabled
		case "honeypot":
			input.HoneypotEnabled = &enabled
		case "logging":
			input.GeneralLoggingEnabled = &enabled
		}
		_, err := s.services.Settings.Update(ctx, staff, input)
		switch {
		case errors.Is(err, quack.ErrGuildSettingsValidation):
			return Message{}, &UserError{Message: fmt.Sprintf(
				"Could not enable %s with the saved setup. Check its channels and Quack's permissions, or run `/setup %s` without enabled to configure it.",
				module, module)}
		case errors.Is(err, quack.ErrGuildSettingsPermissionDenied):
			return Message{}, &UserError{Message: "You need Manage Server permission to turn features on or off."}
		case err != nil:
			return Message{}, &UserError{Message: "Could not save this setting. Try again."}
		}
		text := setupModules[module] + " turned on."
		if !enabled {
			text = fmt.Sprintf("%s turned off. Your channels are saved. Turn it back on with `/setup %s enabled:true`.",
				setupModules[module], module)
		}
		return Signal("settings", text, false), nil
	}
}
