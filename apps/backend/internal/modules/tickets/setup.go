package tickets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discord"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/quack"
)

// Setup handles /setup tickets: it picks or creates the entry and staff
// queue channels, checks Quack's permissions in both, switches tickets on,
// and posts or updates the entry panel. The router has already confirmed
// Manage Server. One setup runs per guild at a time.
func (m *Module) Setup(ctx context.Context, req discord.SetupRequest) (discord.Message, error) {
	actor := modules.ActorFor(req.Guild)
	discordGuildID := req.Guild.Guild.DiscordGuildID
	release, err := m.setupLocks.acquire(ctx, actor.GuildID)
	if err != nil {
		return discord.Message{}, &discord.UserError{Message: "Ticket setup is busy. Try again shortly."}
	}
	defer release()
	settings, _, err := m.service.Settings(ctx, actor)
	if errors.Is(err, ErrPermissionDenied) {
		return discord.Message{}, &discord.UserError{Message: "You need Manage Server permission to set up tickets."}
	}
	if err != nil {
		return discord.Message{}, &discord.UserError{Message: "Could not load ticket settings. Try again."}
	}
	session := m.channels.session
	entryID, err := discord.SetupChannel(ctx, session, discordGuildID,
		req.String("entry"), settings.EntryChannelDiscordID, "support", discord.SetupTicketEntry, nil)
	if err != nil {
		return discord.Message{}, err
	}
	queueID, err := discord.SetupChannel(ctx, session, discordGuildID,
		req.String("queue"), settings.QueueChannelDiscordID, "ticket-log", discord.SetupStaffChannel,
		req.Guild.StaffRoles.ModeratorRoleIDs)
	if err != nil {
		return discord.Message{}, err
	}
	if entryID == queueID {
		return discord.Message{}, &discord.UserError{Message: "Choose separate entry and staff queue channels."}
	}
	entry, err := session.Channel(entryID, rest(ctx)...)
	if err != nil || entry.GuildID != discordGuildID || entry.Type != discordgo.ChannelTypeGuildText {
		return discord.Message{}, &discord.UserError{Message: "The entry must be a text channel in this server that Quack can access."}
	}
	// The request already carries the guild's staff roles, the same ones a
	// new queue channel was just created for.
	if err := m.channels.bot().ValidateStaffChannelForRoles(ctx, discordGuildID, queueID, req.Guild.StaffRoles); err != nil {
		return discord.Message{}, &discord.UserError{Message: "Quack needs to view, send, read history and attach files in the queue channel."}
	}
	if err := m.channels.checkBotPermissions(ctx, discordGuildID, entryID, queueID); err != nil {
		return discord.Message{}, err
	}
	settings.EntryChannelDiscordID, settings.QueueChannelDiscordID = entryID, queueID
	if _, err := m.service.UpdateSettings(ctx, actor, true, settings); err != nil {
		return discord.Message{}, &discord.UserError{Message: "Could not save ticket settings. Try again."}
	}
	panelID, err := m.channels.publishEntryPanel(ctx, settings)
	if err != nil {
		return discord.Message{}, &discord.UserError{Message: "Ticket channels are saved, but Quack could not update the opening buttons. Check access to the old and new entry channels, then run setup again."}
	}
	if err := m.service.RecordEntryPanel(ctx, actor, entryID, panelID); err != nil {
		return discord.Message{}, &discord.UserError{Message: "The opening button was posted, but Quack could not save its message reference. Check the existing panel before running setup again."}
	}
	return discord.Signal("ticket", fmt.Sprintf(
		"Tickets are ready in <#%s>. Staff notifications and transcripts will go to <#%s>.", entryID, queueID), false), nil
}

// checkEnablement checks a guild's saved ticket settings the way Setup
// does, before the settings API switches tickets on: separate channels, a
// text entry channel in the guild, a staff-only queue, and Quack's
// permissions in both. It writes and posts nothing.
func (m *Module) checkEnablement(ctx context.Context, guild *quack.Guild, configJSON string) error {
	settings := Defaults()
	if err := json.Unmarshal([]byte(configJSON), &settings); err != nil {
		return fmt.Errorf("decode ticket settings: %w", err)
	}
	if err := validateSettings(settings, true); err != nil {
		return err
	}
	if settings.EntryChannelDiscordID == settings.QueueChannelDiscordID {
		return errors.New("ticket entry and staff queue channels must be separate")
	}
	entry, err := m.channels.session.Channel(settings.EntryChannelDiscordID, rest(ctx)...)
	if err != nil || entry.GuildID != guild.DiscordGuildID || entry.Type != discordgo.ChannelTypeGuildText {
		return errors.New("ticket entry must be an accessible text channel in this server")
	}
	if err := m.channels.bot().ValidateStaffChannel(ctx, guild.DiscordGuildID, settings.QueueChannelDiscordID); err != nil {
		return fmt.Errorf("ticket staff queue: %w", err)
	}
	return m.channels.checkBotPermissions(ctx, guild.DiscordGuildID, settings.EntryChannelDiscordID, settings.QueueChannelDiscordID)
}

// publishEntryPanel posts the entry panel and returns its message ID. A
// panel already in the entry channel is edited in place; one left in an old
// entry channel is retired first, and a failed retirement stops here so its
// reference is kept for a retry and two live panels never coexist. Only
// Discord saying the old panel is gone allows a fresh post.
func (c channels) publishEntryPanel(ctx context.Context, settings Settings) (string, error) {
	panelMoved := settings.EntryPanelChannelID != settings.EntryChannelDiscordID
	if settings.EntryPanelMessageID != "" && settings.EntryPanelChannelID != "" && panelMoved {
		if err := c.retireEntryPanel(ctx, settings); err != nil {
			return "", err
		}
	}
	panel := entryPanelMessage()
	if settings.EntryPanelMessageID != "" && !panelMoved {
		edit := c.messageEdit(ctx, settings.EntryChannelDiscordID, settings.EntryPanelMessageID, panel)
		sent, err := c.session.ChannelMessageEditComplex(edit, rest(ctx)...)
		switch {
		case err == nil && sent.ID != "":
			return sent.ID, nil
		case err != nil && !isRESTCode(err, discordgo.ErrCodeUnknownMessage):
			return "", err
		}
	}
	sent, err := c.bot().Send(ctx, settings.EntryChannelDiscordID, panel)
	if err != nil {
		return "", err
	}
	if sent.ID == "" {
		return "", errors.New("discord did not return the entry panel message")
	}
	return sent.ID, nil
}

// retireEntryPanel replaces a panel in an old entry channel with a pointer
// to the new one and removes its button. A deleted message or channel is
// already retired; other failures are returned.
func (c channels) retireEntryPanel(ctx context.Context, settings Settings) error {
	content := movedPanelContent(settings.EntryChannelDiscordID)
	components := []discordgo.MessageComponent{}
	_, err := c.session.ChannelMessageEditComplex(&discordgo.MessageEdit{
		ID:              settings.EntryPanelMessageID,
		Channel:         settings.EntryPanelChannelID,
		Content:         &content,
		Components:      &components,
		AllowedMentions: &discordgo.MessageAllowedMentions{},
	}, rest(ctx)...)
	if isRESTCode(err, discordgo.ErrCodeUnknownMessage, discordgo.ErrCodeUnknownChannel) {
		return nil
	}
	return err
}

// permission is a Discord permission bit and the name admins see.
type permission struct {
	bit  int64
	name string
}

// checkBotPermissions checks, from fresh REST state rather than the gateway
// cache, that Quack can open threads in the entry channel and post
// transcripts in the queue. Every error is a *discord.UserError.
func (c channels) checkBotPermissions(ctx context.Context, discordGuildID, entryID, queueID string) error {
	botID, err := c.botID(ctx)
	if err != nil {
		return &discord.UserError{Message: "Could not identify Quack's server account. Try again."}
	}
	guild, err := c.session.Guild(discordGuildID, rest(ctx)...)
	if err != nil {
		return &discord.UserError{Message: "Could not check Quack's current server permissions. Try again."}
	}
	member, err := c.session.GuildMember(discordGuildID, botID, rest(ctx)...)
	if err != nil {
		return &discord.UserError{Message: "Could not check Quack's current server membership. Try again."}
	}
	common := []permission{
		{discordgo.PermissionViewChannel, "View Channel"},
		{discordgo.PermissionSendMessages, "Send Messages"},
		{discordgo.PermissionReadMessageHistory, "Read Message History"},
	}
	for _, destination := range []struct {
		channelID string
		required  []permission
	}{
		{entryID, append(slices.Clone(common),
			permission{discordgo.PermissionCreatePrivateThreads, "Create Private Threads"},
			permission{discordgo.PermissionSendMessagesInThreads, "Send Messages in Threads"},
			permission{discordgo.PermissionManageThreads, "Manage Threads"})},
		{queueID, append(slices.Clone(common), permission{discordgo.PermissionAttachFiles, "Attach Files"})},
	} {
		channel, err := c.session.Channel(destination.channelID, rest(ctx)...)
		if err != nil || channel.GuildID != discordGuildID || channel.Type != discordgo.ChannelTypeGuildText {
			return &discord.UserError{Message: "Ticket destinations must be text channels in this server."}
		}
		// A throwaway state computes permissions from exactly these reads.
		snapshot := *guild
		snapshot.Channels = []*discordgo.Channel{channel}
		snapshot.Members = []*discordgo.Member{member}
		state := discordgo.NewState()
		if err := state.GuildAdd(&snapshot); err != nil {
			return &discord.UserError{Message: "Could not check Quack's channel permissions. Try again."}
		}
		granted, err := state.UserChannelPermissions(botID, channel.ID)
		if err != nil {
			return &discord.UserError{Message: "Could not check Quack's channel permissions. Try again."}
		}
		var missing []string
		for _, required := range destination.required {
			if granted&required.bit == 0 {
				missing = append(missing, required.name)
			}
		}
		if len(missing) > 0 {
			return &discord.UserError{Message: fmt.Sprintf(
				"Quack needs %s in <#%s>. Update the channel permissions and run setup again.",
				strings.Join(missing, ", "), channel.ID)}
		}
	}
	return nil
}
