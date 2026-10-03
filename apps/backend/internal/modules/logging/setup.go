package logging

import (
	"context"
	"fmt"

	"github.com/quackdiscord/bot/internal/discord"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/quack"
)

// RegisterSetup routes /setup logging to Setup.
func (m *Module) RegisterSetup(router *discord.Router) {
	router.HandleSetup("logging", m.Setup)
}

// Setup handles /setup logging: it sends every event to one staff-only
// channel, the one given, the one already configured, or a new
// #discord-log, and switches logging on. The router has already confirmed
// the caller holds Manage Server.
func (m *Module) Setup(ctx context.Context, req discord.SetupRequest) (discord.Message, error) {
	discordGuildID := req.Guild.Guild.DiscordGuildID
	if err := m.client.requireAuditLog(ctx, discordGuildID); err != nil {
		return discord.Message{}, &discord.UserError{Message: "Quack needs View Audit Log permission to log bans by other moderators without duplicating its own actions."}
	}
	actor := modules.ActorFor(req.Guild)
	settings, _, _, err := m.service.Settings(ctx, actor)
	if err != nil {
		return discord.Message{}, &discord.UserError{Message: "Could not load logging settings. Try again."}
	}
	channelID, err := discord.SetupChannel(ctx, m.client.bot.Session, discordGuildID,
		req.String("channel"), settings.Channels[MessageEdit], "discord-log", discord.SetupStaffChannel)
	if err != nil {
		return discord.Message{}, err
	}
	if _, err := m.service.UpdateSettings(ctx, actor, true, settings.RouteAllTo(channelID)); err != nil {
		return discord.Message{}, &discord.UserError{Message: "Could not enable logging. Choose a text channel where Quack can View Channel, Send Messages and Attach Files."}
	}
	return discord.Signal("settings", fmt.Sprintf("Discord logs will go to <#%s>.", channelID), false), nil
}

// checkEnablement is the registry's modules.EnablementCheck for logging.
// It checks saved settings the way Setup does: valid with logging on, View
// Audit Log for attributing bans, and every destination staff-only and
// writable.
func (m *Module) checkEnablement(ctx context.Context, guild *quack.Guild, configJSON string) error {
	settings, err := decodeEnabledSettings(configJSON)
	if err != nil {
		return err
	}
	if err := m.client.requireAuditLog(ctx, guild.DiscordGuildID); err != nil {
		return err
	}
	for _, channelID := range destinations(settings.Channels) {
		if err := m.client.ValidateStaffOnlyChannel(ctx, guild.ID, channelID); err != nil {
			return err
		}
	}
	return nil
}
