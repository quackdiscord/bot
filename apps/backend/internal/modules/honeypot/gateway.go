package honeypot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/modules"
)

// RegisterGateway subscribes the honeypot to new messages and to deletions
// of its trap channel or guild.
func (m *Module) RegisterGateway(session *discordgo.Session) {
	session.AddHandler(m.onMessageCreate)
	session.AddHandler(m.onChannelDelete)
	session.AddHandler(m.onGuildDelete)
}

// Intents returns the gateway intents the honeypot needs: guild messages,
// once any guild has it on. It never needs message content.
func (m *Module) Intents(ctx context.Context) (discordgo.Intent, error) {
	enabled, err := m.registry.AnyEnabled(ctx, modules.Honeypots)
	if err != nil || !enabled {
		return 0, err
	}
	return discordgo.IntentGuilds | discordgo.IntentGuildMessages, nil
}

// onMessageCreate projects a message in a guild with the honeypot on and
// queues it. The author's roles and permissions are fetched fresh rather
// than trusted from the event.
func (m *Module) onMessageCreate(session *discordgo.Session, event *discordgo.MessageCreate) {
	if event.Message == nil || event.GuildID == "" || event.Author == nil {
		return
	}
	ctx := context.Background()
	guildID, err := m.guilds.InternalID(ctx, event.GuildID)
	if err != nil {
		return
	}
	configuration, err := m.registry.Configuration(ctx, guildID, modules.Honeypots)
	if err != nil || configuration == nil || !configuration.Enabled {
		return
	}
	channel, err := session.Channel(event.ChannelID)
	if err != nil || channel.GuildID != event.GuildID {
		return
	}
	guild, err := session.Guild(event.GuildID)
	if err != nil {
		return
	}
	member, err := session.GuildMember(event.GuildID, event.Author.ID)
	if err != nil {
		return
	}
	message, err := projectMessage(guildID, event, guild, channel, member, botID(session))
	if err != nil {
		return
	}
	// A full queue sheds the message: honeypot traffic must never back up
	// the gateway.
	m.pool.Submit(message)
}

// onChannelDelete turns the honeypot off if its trap channel was deleted.
func (m *Module) onChannelDelete(_ *discordgo.Session, event *discordgo.ChannelDelete) {
	if event.Channel == nil {
		return
	}
	ctx := context.Background()
	guildID, err := m.guilds.InternalID(ctx, event.GuildID)
	if err != nil {
		return
	}
	_ = m.service.HandleDeletedChannel(ctx, guildID, event.ID)
}

// onGuildDelete turns the honeypot off when Quack leaves the guild, keeping
// its settings for a repair after a rejoin. An outage is not a departure.
func (m *Module) onGuildDelete(_ *discordgo.Session, event *discordgo.GuildDelete) {
	if event.Guild == nil || event.Unavailable {
		return
	}
	ctx := context.Background()
	guildID, err := m.guilds.InternalIDAny(ctx, event.ID)
	if err != nil {
		return
	}
	configuration, err := m.registry.Configuration(ctx, guildID, modules.Honeypots)
	if err != nil || configuration == nil || !configuration.Enabled {
		return
	}
	var settings Settings
	if json.Unmarshal([]byte(configuration.ConfigJSON), &settings) != nil || settings.ChannelDiscordID == "" {
		return
	}
	_ = m.service.HandleDeletedChannel(ctx, guildID, settings.ChannelDiscordID)
}

// channelValidator is the ChannelValidator over live Discord state.
type channelValidator struct {
	session *discordgo.Session
	guilds  *modules.Guilds
}

// ValidateHoneypotChannel requires that the channel is in the guild and
// that the bot can see it, checked live.
func (v channelValidator) ValidateHoneypotChannel(ctx context.Context, guildID, channelID string) error {
	discordGuildID, err := v.guilds.DiscordID(ctx, strings.TrimSpace(guildID))
	if err != nil {
		return err
	}
	channel, err := v.session.Channel(strings.TrimSpace(channelID),
		discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil || channel == nil || channel.GuildID != discordGuildID {
		return ErrChannelUnavailable
	}
	guild, member, err := botMember(v.session, discordGuildID)
	if err != nil {
		return err
	}
	if channelPermissions(guild, channel, member)&discordgo.PermissionViewChannel == 0 {
		return ErrChannelUnavailable
	}
	return nil
}

// botMember loads the guild and the bot's current membership in it.
func botMember(session *discordgo.Session, discordGuildID string) (*discordgo.Guild, *discordgo.Member, error) {
	id := botID(session)
	if id == "" {
		user, err := session.User("@me")
		if err != nil || user == nil {
			return nil, nil, errors.New("current Discord bot identity is unavailable")
		}
		id = user.ID
	}
	guild, err := session.Guild(discordGuildID)
	if err != nil || guild == nil {
		return nil, nil, errors.New("current Discord guild is unavailable")
	}
	member, err := session.GuildMember(discordGuildID, id)
	if err != nil || member == nil || member.User == nil || member.User.ID != id {
		return nil, nil, errors.New("current Discord bot membership is unavailable")
	}
	return guild, member, nil
}

// botID returns the bot's user ID once the gateway has delivered it.
func botID(session *discordgo.Session) string {
	if session.State != nil && session.State.User != nil {
		return session.State.User.ID
	}
	return ""
}

// projectMessage builds the trap policy's view of a message from the event
// plus freshly loaded guild, channel, and member state.
func projectMessage(guildID string, event *discordgo.MessageCreate, guild *discordgo.Guild, channel *discordgo.Channel, member *discordgo.Member, botID string) (Message, error) {
	if strings.TrimSpace(guildID) == "" || event.Message == nil || event.GuildID == "" ||
		channel == nil || channel.GuildID != event.GuildID ||
		member == nil || member.User == nil || member.User.ID == "" {
		return Message{}, errors.New("honeypot message projection is incomplete")
	}
	if event.Author == nil || event.Author.ID != member.User.ID {
		return Message{}, errors.New("honeypot message author does not match current member")
	}
	permissions := channelPermissions(guild, channel, member)
	staff := int64(discordgo.PermissionModerateMembers | discordgo.PermissionKickMembers |
		discordgo.PermissionBanMembers | discordgo.PermissionManageServer)
	return Message{
		GuildID:              strings.TrimSpace(guildID),
		ChannelDiscordID:     channel.ID,
		MessageDiscordID:     event.ID,
		AuthorDiscordUserID:  member.User.ID,
		MessageURL:           fmt.Sprintf("https://discord.com/channels/%s/%s/%s", event.GuildID, channel.ID, event.ID),
		AuthorRoleDiscordIDs: append([]string(nil), member.Roles...),
		IsBot:                member.User.Bot,
		IsQuack:              member.User.ID == botID,
		IsWebhook:            event.WebhookID != "",
		AuthorCanModerate:    permissions&(discordgo.PermissionAdministrator|staff) != 0,
	}, nil
}

// channelPermissions computes a member's permissions in a channel the way
// Discord does: base role permissions, then @everyone, role, and member
// overwrites in that order. The owner and administrators get everything.
func channelPermissions(guild *discordgo.Guild, channel *discordgo.Channel, member *discordgo.Member) int64 {
	if guild == nil || channel == nil || member == nil || member.User == nil {
		return 0
	}
	permissions := int64(0)
	roles := make(map[string]struct{}, len(member.Roles))
	for _, roleID := range member.Roles {
		roles[roleID] = struct{}{}
	}
	for _, role := range guild.Roles {
		if role == nil {
			continue
		}
		if _, ok := roles[role.ID]; ok || role.ID == guild.ID {
			permissions |= role.Permissions
		}
	}
	if member.User.ID == guild.OwnerID || permissions&discordgo.PermissionAdministrator != 0 {
		return discordgo.PermissionAll
	}
	for _, overwrite := range channel.PermissionOverwrites {
		if overwrite.ID == guild.ID && overwrite.Type == discordgo.PermissionOverwriteTypeRole {
			permissions = permissions&^overwrite.Deny | overwrite.Allow
			break
		}
	}
	roleDeny, roleAllow := int64(0), int64(0)
	for _, overwrite := range channel.PermissionOverwrites {
		if overwrite.Type != discordgo.PermissionOverwriteTypeRole {
			continue
		}
		if _, ok := roles[overwrite.ID]; ok {
			roleDeny |= overwrite.Deny
			roleAllow |= overwrite.Allow
		}
	}
	permissions = permissions&^roleDeny | roleAllow
	for _, overwrite := range channel.PermissionOverwrites {
		if overwrite.ID == member.User.ID && overwrite.Type == discordgo.PermissionOverwriteTypeMember {
			permissions = permissions&^overwrite.Deny | overwrite.Allow
			break
		}
	}
	return permissions
}
