package honeypot

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discord"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/quack"
)

// TemplateService is the core template service the honeypot needs: setup
// creates the default honeypot template, and the warning names its
// punishments. *quack.TemplateService implements it.
type TemplateService interface {
	WarningPolicy
	EnsureHoneypotTemplate(ctx context.Context, guildContext *quack.GuildStaffContext) (*quack.TemplateResponse, error)
}

// RegisterSetup routes /setup honeypot to Setup.
func (m *Module) RegisterSetup(router *discord.Router) {
	router.HandleSetup("honeypot", m.Setup)
}

// Setup handles /setup honeypot: it picks or creates the trap channel and
// the honeypot template, posts or updates the warning, and switches the
// honeypot on. Existing settings, templates, and channels are reused rather
// than replaced. The caller has already confirmed Manage Server. Every
// failure is a *discord.UserError.
func (m *Module) Setup(ctx context.Context, req discord.SetupRequest) (discord.Message, error) {
	fail := func(message string) (discord.Message, error) {
		return discord.Message{}, &discord.UserError{Message: message}
	}
	actor := modules.ActorFor(req.Guild)
	discordGuildID := req.Guild.Guild.DiscordGuildID
	release, err := m.locks.lock(ctx, actor.GuildID)
	if err != nil {
		return fail("Honeypot setup timed out. Try again.")
	}
	defer release()

	settings, status, err := m.service.Settings(ctx, actor)
	if err != nil {
		return fail("Could not load honeypot settings. Try again.")
	}
	if settings.TemplateID == "" {
		template, err := m.templates.EnsureHoneypotTemplate(ctx, req.Guild)
		if err != nil {
			return fail("Could not create the honeypot template. If it is archived, restore it first.")
		}
		settings.TemplateID = template.ID
	}
	if err := m.templateCheck.ValidateHoneypotTemplate(ctx, actor.GuildID, settings.TemplateID); err != nil {
		return fail("The selected honeypot template is unavailable. Restore or repair it before setup.")
	}
	channelID, err := discord.SetupChannel(ctx, m.session, discordGuildID,
		req.String("channel"), settings.ChannelDiscordID, "honeypot", discord.SetupHoneypotChannel)
	if err != nil {
		return discord.Message{}, err
	}
	channel, err := m.session.Channel(channelID, rest(ctx)...)
	if err != nil {
		return fail("Could not access the honeypot channel. Check Quack's permissions.")
	}
	if channel.GuildID != discordGuildID || channel.Type != discordgo.ChannelTypeGuildText {
		return fail("The honeypot must be a text channel in this server.")
	}
	if settings.ChannelDiscordID != channelID {
		// Save the new channel straight away, so a retry reuses it rather
		// than creating another.
		settings.ChannelDiscordID, settings.WarningMessageID = channelID, ""
		if _, _, err := m.service.UpdateSettings(ctx, actor, false, settings); err != nil {
			return fail("Could not save the honeypot channel. Specify it when you retry setup.")
		}
	}
	if err := m.channelCheck.ValidateHoneypotChannel(ctx, actor.GuildID, channel.ID); err != nil {
		return fail("Quack needs View Channel, Send Messages, Read Message History and Manage Messages in the honeypot channel. Update its permissions and run setup again.")
	}
	if warning := req.String("warning"); strings.TrimSpace(warning) != "" {
		settings.WarningText = strings.ReplaceAll(warning, `\n`, "\n")
	}
	text, err := resolveHoneypotWarning(ctx, m.warnings.policy, actor.GuildID, settings)
	if err != nil {
		return fail("I couldn't read the honeypot punishment. Check its template and try setup again.")
	}
	content := honeypotWarningContent(text, status.Statistics.Created)
	posted := false
	if settings.WarningMessageID != "" {
		if posted, err = m.warnings.edit(ctx, channel.ID, settings.WarningMessageID, content); err != nil {
			return fail("Could not update the honeypot warning. Check Quack's channel permissions.")
		}
	}
	if !posted {
		sent, err := m.warnings.sendReplacement(ctx, actor.GuildID, settings, content)
		if errors.Is(err, ErrWarningDeliveryUnknown) {
			return fail("The previous warning send could not be confirmed. Inspect the configured channel before changing setup; another warning was not sent.")
		}
		if err != nil {
			return fail("Could not post the honeypot warning. Check Send Messages permission and run setup again.")
		}
		settings.WarningMessageID = sent.ID
	}
	if _, _, err := m.service.UpdateSettings(ctx, actor, true, settings); err != nil {
		return fail("The warning is posted, but the honeypot could not be enabled. Check the channel and template, then run setup again.")
	}
	return discord.Signal("settings", fmt.Sprintf("Honeypot ready in <#%s>. Staff and Quack can post safely.", channel.ID), false), nil
}

// checkEnablement is the registry's enablement check for the honeypot: the
// saved settings must name a channel and template, and both must pass the
// same live checks /setup runs. It writes and posts nothing.
func (m *Module) checkEnablement(ctx context.Context, guild *quack.Guild, configJSON string) error {
	settings, err := decodeEnabledSettings(configJSON)
	if err != nil {
		return err
	}
	if err := m.channelCheck.ValidateHoneypotChannel(ctx, guild.ID, settings.ChannelDiscordID); err != nil {
		return err
	}
	return m.templateCheck.ValidateHoneypotTemplate(ctx, guild.ID, settings.TemplateID)
}
