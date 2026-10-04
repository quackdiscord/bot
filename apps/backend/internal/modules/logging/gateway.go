package logging

import (
	"context"
	"slices"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/modules"
)

// guildID resolves an event's guild. Events from guilds Quack does not
// know, or has left, are dropped.
func (m *Module) guildID(discordGuildID string) (string, bool) {
	id, err := m.guilds.InternalID(context.Background(), discordGuildID)
	return id, err == nil
}

// onMessageCreate caches a message so a later edit or delete can show it.
// Other bots' messages are cached too; Quack's own are not, since its
// receipts are edited as cases progress. Caching is skipped for guilds
// without logging on.
func (m *Module) onMessageCreate(s *discordgo.Session, event *discordgo.MessageCreate) {
	if event.Message == nil || event.GuildID == "" || fromSelf(s, event.Author) {
		return
	}
	if guildID, ok := m.guildID(event.GuildID); ok {
		_ = m.service.CacheMessage(context.Background(), cachedMessage(guildID, event.Message))
	}
}

// onMessageUpdate logs an edit with the message as it was before. Updates
// without an edit timestamp are Discord adding link previews and the like,
// not member edits.
func (m *Module) onMessageUpdate(s *discordgo.Session, event *discordgo.MessageUpdate) {
	if event.Message == nil || event.GuildID == "" || event.EditedTimestamp == nil || fromSelf(s, event.Author) {
		return
	}
	guildID, ok := m.guildID(event.GuildID)
	if !ok {
		return
	}
	var before *CachedMessage
	if event.BeforeUpdate != nil {
		value := cachedMessage(guildID, event.BeforeUpdate)
		before = &value
	}
	edit, err := m.service.PrepareMessageEdit(context.Background(), cachedMessage(guildID, event.Message), before)
	if err == nil && edit != nil {
		m.pool.Submit(*edit)
	}
}

// onMessageDelete logs a deletion; delivery fills in the cached content.
func (m *Module) onMessageDelete(_ *discordgo.Session, event *discordgo.MessageDelete) {
	if event.Message == nil || event.GuildID == "" {
		return
	}
	if guildID, ok := m.guildID(event.GuildID); ok {
		m.pool.Submit(messageEvent(guildID, MessageDelete, event.Message))
	}
}

// onMessageDeleteBulk logs a bulk deletion.
func (m *Module) onMessageDeleteBulk(_ *discordgo.Session, event *discordgo.MessageDeleteBulk) {
	if guildID, ok := m.guildID(event.GuildID); ok {
		m.pool.Submit(Event{
			GuildID: guildID, ChannelDiscordID: event.ChannelID, Type: MessageBulkDelete,
			MessageIDs: slices.Clone(event.Messages),
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

// memberEvent queues a join or leave.
func (m *Module) memberEvent(member *discordgo.Member, eventType EventType) {
	if member == nil {
		return
	}
	if guildID, ok := m.guildID(member.GuildID); ok {
		m.pool.Submit(Event{GuildID: guildID, Type: eventType, ActorDiscordUserID: userID(member.User)})
	}
}

// onAuditLogEntry logs bans and unbans from Discord's audit log, which
// names who did it as well as who it happened to.
func (m *Module) onAuditLogEntry(s *discordgo.Session, entry *discordgo.GuildAuditLogEntryCreate) {
	event, ok := externalBanEvent(entry, selfID(s))
	if !ok {
		return
	}
	if guildID, ok := m.guildID(entry.GuildID); ok {
		event.GuildID = guildID
		m.pool.Submit(event)
	}
}

// externalBanEvent returns the ban or unban event for an audit log entry.
// Quack's own bans are left out: its cases already record them, in the
// audit log and its mirror.
func externalBanEvent(entry *discordgo.GuildAuditLogEntryCreate, botID string) (Event, bool) {
	if entry == nil || entry.AuditLogEntry == nil || entry.ActionType == nil || entry.GuildID == "" ||
		entry.TargetID == "" || entry.UserID == "" || botID == "" || entry.UserID == botID {
		return Event{}, false
	}
	var eventType EventType
	switch *entry.ActionType {
	case discordgo.AuditLogActionMemberBanAdd:
		eventType = DiscordBan
	case discordgo.AuditLogActionMemberBanRemove:
		eventType = DiscordUnban
	default:
		return Event{}, false
	}
	return Event{
		Type:               eventType,
		ActorDiscordUserID: entry.UserID,
		Metadata:           map[string]string{"target_id": entry.TargetID, "reason": entry.Reason},
	}, true
}

// onGuildUpdate logs a guild change.
func (m *Module) onGuildUpdate(_ *discordgo.Session, event *discordgo.GuildUpdate) {
	if event.Guild == nil {
		return
	}
	if guildID, ok := m.guildID(event.ID); ok {
		m.pool.Submit(Event{GuildID: guildID, Type: GuildChange, Metadata: map[string]string{"name": event.Name}})
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
		actor := modules.Actor{GuildID: guildID, DiscordUserID: modules.SystemActorID, CanManage: true}
		_, _, _ = m.service.RepairDeletedChannel(context.Background(), actor, event.ID)
	}
}

// channelEvent queues a channel creation, change, or deletion.
func (m *Module) channelEvent(channel *discordgo.Channel, operation string) {
	if channel == nil || channel.GuildID == "" {
		return
	}
	if guildID, ok := m.guildID(channel.GuildID); ok {
		m.pool.Submit(Event{
			GuildID: guildID, ChannelDiscordID: channel.ID, Type: ChannelChange,
			Metadata: map[string]string{"operation": operation, "name": channel.Name},
		})
	}
}

// cachedMessage copies what the cache may keep of a message.
func cachedMessage(guildID string, message *discordgo.Message) CachedMessage {
	cached := CachedMessage{
		GuildID: guildID, ChannelDiscordID: message.ChannelID, MessageDiscordID: message.ID,
		AuthorDiscordUserID: userID(message.Author), Content: message.Content,
	}
	for _, a := range message.Attachments {
		cached.Attachments = append(cached.Attachments, AttachmentMetadata{
			DiscordID: a.ID, Filename: a.Filename, ContentType: a.ContentType, URL: a.URL, Size: int64(a.Size),
		})
	}
	for _, embed := range message.Embeds {
		cached.EmbedTypes = append(cached.EmbedTypes, string(embed.Type))
	}
	return cached
}

// messageEvent builds the event for a deleted message from what the
// gateway sent; delivery fills in the rest from the cache.
func messageEvent(guildID string, eventType EventType, message *discordgo.Message) Event {
	cached := cachedMessage(guildID, message)
	return Event{
		GuildID: guildID, ChannelDiscordID: message.ChannelID, MessageDiscordID: message.ID,
		ActorDiscordUserID: cached.AuthorDiscordUserID, Type: eventType,
		Attachments: cached.Attachments, EmbedTypes: cached.EmbedTypes,
	}
}

// selfID returns Quack's own user ID from the gateway session, or "" before
// the gateway is ready.
func selfID(s *discordgo.Session) string {
	if s == nil || s.State == nil || s.State.User == nil {
		return ""
	}
	return s.State.User.ID
}

// fromSelf reports whether author is Quack itself.
func fromSelf(s *discordgo.Session, author *discordgo.User) bool {
	id := selfID(s)
	return author != nil && id != "" && author.ID == id
}

// userID returns user's ID, or "" for an event without one.
func userID(user *discordgo.User) string {
	if user == nil {
		return ""
	}
	return user.ID
}
