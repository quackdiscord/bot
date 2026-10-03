package logging

import (
	"context"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/modules"
)

// systemActor attributes repairs Quack makes on its own.
const systemActor = "quack-system"

// RegisterGateway subscribes logging to the gateway events it logs, and to
// channel deletions so routes to a deleted channel are removed.
func (m *Module) RegisterGateway(session *discordgo.Session) {
	session.AddHandler(m.onMessageCreate)
	session.AddHandler(m.onMessageUpdate)
	session.AddHandler(m.onMessageDelete)
	session.AddHandler(m.onMessageDeleteBulk)
	session.AddHandler(m.onMemberAdd)
	session.AddHandler(m.onMemberRemove)
	session.AddHandler(m.onBanAdd)
	session.AddHandler(m.onBanRemove)
	session.AddHandler(m.onGuildUpdate)
	session.AddHandler(m.onChannelCreate)
	session.AddHandler(m.onChannelUpdate)
	session.AddHandler(m.onChannelDelete)
}

// guildID resolves an event's guild, dropping events from guilds Quack
// does not know.
func (m *Module) guildID(discordGuildID string) (string, bool) {
	id, err := m.guilds.InternalID(context.Background(), discordGuildID)
	return id, err == nil
}

// submit queues an event; a full queue drops it.
func (m *Module) submit(event Event) {
	m.pool.Submit(event)
}

// onMessageCreate caches a human message so a later edit or delete can show
// it. Caching is skipped for guilds without logging on.
func (m *Module) onMessageCreate(_ *discordgo.Session, event *discordgo.MessageCreate) {
	if event.Message == nil || event.GuildID == "" || (event.Author != nil && event.Author.Bot) {
		return
	}
	if guildID, ok := m.guildID(event.GuildID); ok {
		_ = m.service.CacheMessage(context.Background(), cachedMessage(guildID, event.Message))
	}
}

// onMessageUpdate logs an edit and refreshes the cached copy.
func (m *Module) onMessageUpdate(_ *discordgo.Session, event *discordgo.MessageUpdate) {
	if event.Message == nil || event.GuildID == "" {
		return
	}
	guildID, ok := m.guildID(event.GuildID)
	if !ok {
		return
	}
	before := ""
	if event.BeforeUpdate != nil {
		before = event.BeforeUpdate.Content
	}
	m.submit(messageEvent(guildID, MessageEdit, event.Message, before, event.Content))
	_ = m.service.CacheMessage(context.Background(), cachedMessage(guildID, event.Message))
}

// onMessageDelete logs a deletion; delivery fills in the cached content.
func (m *Module) onMessageDelete(_ *discordgo.Session, event *discordgo.MessageDelete) {
	if event.Message == nil || event.GuildID == "" {
		return
	}
	if guildID, ok := m.guildID(event.GuildID); ok {
		m.submit(messageEvent(guildID, MessageDelete, event.Message, "", ""))
	}
}

// onMessageDeleteBulk logs a bulk deletion.
func (m *Module) onMessageDeleteBulk(_ *discordgo.Session, event *discordgo.MessageDeleteBulk) {
	if guildID, ok := m.guildID(event.GuildID); ok {
		m.submit(Event{
			GuildID: guildID, ChannelDiscordID: event.ChannelID, Type: MessageBulkDelete,
			MessageIDs: append([]string(nil), event.Messages...),
		})
	}
}

// onMemberAdd logs a join.
func (m *Module) onMemberAdd(_ *discordgo.Session, event *discordgo.GuildMemberAdd) {
	m.memberEvent(event.Member, MemberJoin)
}

// onMemberRemove logs a leave.
func (m *Module) onMemberRemove(_ *discordgo.Session, event *discordgo.GuildMemberRemove) {
	m.memberEvent(event.Member, MemberLeave)
}

func (m *Module) memberEvent(member *discordgo.Member, eventType EventType) {
	if member == nil {
		return
	}
	if guildID, ok := m.guildID(member.GuildID); ok {
		m.submit(Event{GuildID: guildID, Type: eventType, ActorDiscordUserID: userID(member.User)})
	}
}

// onBanAdd logs a ban.
func (m *Module) onBanAdd(_ *discordgo.Session, event *discordgo.GuildBanAdd) {
	m.banEvent(event.GuildID, event.User, DiscordBan)
}

// onBanRemove logs an unban.
func (m *Module) onBanRemove(_ *discordgo.Session, event *discordgo.GuildBanRemove) {
	m.banEvent(event.GuildID, event.User, DiscordUnban)
}

func (m *Module) banEvent(discordGuildID string, user *discordgo.User, eventType EventType) {
	if guildID, ok := m.guildID(discordGuildID); ok {
		m.submit(Event{GuildID: guildID, Type: eventType, ActorDiscordUserID: userID(user)})
	}
}

// onGuildUpdate logs a guild rename.
func (m *Module) onGuildUpdate(_ *discordgo.Session, event *discordgo.GuildUpdate) {
	if event.Guild == nil {
		return
	}
	if guildID, ok := m.guildID(event.ID); ok {
		m.submit(Event{GuildID: guildID, Type: GuildChange, Metadata: map[string]string{"name": event.Name}})
	}
}

// onChannelCreate logs a new channel.
func (m *Module) onChannelCreate(_ *discordgo.Session, event *discordgo.ChannelCreate) {
	m.channelEvent(event.Channel, "created")
}

// onChannelUpdate logs a channel change.
func (m *Module) onChannelUpdate(_ *discordgo.Session, event *discordgo.ChannelUpdate) {
	m.channelEvent(event.Channel, "updated")
}

// onChannelDelete logs the deletion and removes every route to the channel.
func (m *Module) onChannelDelete(_ *discordgo.Session, event *discordgo.ChannelDelete) {
	if event.Channel == nil {
		return
	}
	m.channelEvent(event.Channel, "deleted")
	if guildID, ok := m.guildID(event.GuildID); ok {
		actor := modules.Actor{GuildID: guildID, DiscordUserID: systemActor, CanManage: true}
		_, _, _ = m.service.RepairDeletedChannel(context.Background(), actor, event.ID)
	}
}

func (m *Module) channelEvent(channel *discordgo.Channel, operation string) {
	if channel == nil || channel.GuildID == "" {
		return
	}
	if guildID, ok := m.guildID(channel.GuildID); ok {
		m.submit(Event{
			GuildID: guildID, ChannelDiscordID: channel.ID, Type: ChannelChange,
			Metadata: map[string]string{"operation": operation, "name": channel.Name},
		})
	}
}

// cachedMessage copies what the cache may keep of a message.
func cachedMessage(guildID string, message *discordgo.Message) CachedMessage {
	cached := CachedMessage{
		GuildID: guildID, ChannelDiscordID: message.ChannelID,
		MessageDiscordID: message.ID, Content: message.Content,
	}
	for _, attachment := range message.Attachments {
		cached.Attachments = append(cached.Attachments, AttachmentMetadata{
			Filename: attachment.Filename, ContentType: attachment.ContentType, Size: int64(attachment.Size),
		})
	}
	for _, embed := range message.Embeds {
		cached.EmbedTypes = append(cached.EmbedTypes, string(embed.Type))
	}
	return cached
}

// messageEvent builds the event for an edited or deleted message.
func messageEvent(guildID string, eventType EventType, message *discordgo.Message, before, after string) Event {
	cached := cachedMessage(guildID, message)
	return Event{
		GuildID: guildID, ChannelDiscordID: message.ChannelID, MessageDiscordID: message.ID,
		ActorDiscordUserID: userID(message.Author), Type: eventType, Before: before, After: after,
		Attachments: cached.Attachments, EmbedTypes: cached.EmbedTypes,
	}
}

func userID(user *discordgo.User) string {
	if user == nil {
		return ""
	}
	return user.ID
}
