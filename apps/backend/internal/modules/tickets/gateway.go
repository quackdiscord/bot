package tickets

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"time"

	"github.com/bwmarrin/discordgo"
)

// repairPage is how many tickets one thread-repair query loads.
const repairPage = 100

// onMessageCreate journals messages posted in open ticket threads. Threads
// are recognized from memory, so other traffic costs nothing. A message
// without text has nothing to preserve, and journaling it would hide the
// text of a session missing the message content intent.
func (m *Module) onMessageCreate(_ *discordgo.Session, event *discordgo.MessageCreate) {
	if event.Message == nil || event.GuildID == "" || event.Author == nil || event.Content == "" {
		return
	}
	guildID, ok := m.service.KnownMessageThread(event.ChannelID)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := m.service.RecordMessage(ctx, guildID, event.ChannelID, transcriptMessage(event.Message))
	switch {
	case errors.Is(err, ErrJournalCutoff):
		slog.WarnContext(ctx, "Ticket message arrived after its transcript was captured",
			"thread_id", event.ChannelID, "message_id", event.ID)
	case err != nil:
		slog.WarnContext(ctx, "Ticket message not journaled yet; closing will retry",
			"guild_id", guildID, "thread_id", event.ChannelID, "message_id", event.ID, "error", err)
	}
}

// onGuildCreate repairs thread membership whenever a guild becomes
// available, including after a reconnect that may have missed demotions.
func (m *Module) onGuildCreate(_ *discordgo.Session, event *discordgo.GuildCreate) {
	if event.Guild != nil && !event.Unavailable {
		m.repairThreads(event.ID)
	}
}

// onMemberUpdate repairs thread membership after a member's roles change.
func (m *Module) onMemberUpdate(_ *discordgo.Session, event *discordgo.GuildMemberUpdate) {
	if event.Member == nil {
		return
	}
	if event.BeforeUpdate == nil || !slices.Equal(event.Roles, event.BeforeUpdate.Roles) {
		m.repairThreads(event.GuildID)
	}
}

// onRoleUpdate repairs thread membership after a role changes.
func (m *Module) onRoleUpdate(_ *discordgo.Session, event *discordgo.GuildRoleUpdate) {
	if event.GuildRole != nil {
		m.repairThreads(event.GuildID)
	}
}

// onRoleDelete repairs thread membership granted through a deleted role.
func (m *Module) onRoleDelete(_ *discordgo.Session, event *discordgo.GuildRoleDelete) {
	m.repairThreads(event.GuildID)
}

// onChannelDelete turns tickets off if the entry channel was deleted, and
// records a deleted ticket thread on its ticket's timeline.
func (m *Module) onChannelDelete(_ *discordgo.Session, event *discordgo.ChannelDelete) {
	if event.Channel == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	guildID, err := m.guilds.InternalID(ctx, event.GuildID)
	if err != nil {
		return
	}
	if err := m.service.RepairDeletedEntryChannel(ctx, guildID, event.ID); err != nil {
		slog.WarnContext(ctx, "Ticket entry channel repair failed", "guild_id", guildID, "error", err)
	}
	ticketID, err := m.store.ticketIDByChannel(ctx, guildID, event.ID)
	if err != nil {
		return
	}
	if err := m.service.RecordChannelMissing(ctx, guildID, ticketID, event.ID); err != nil {
		slog.WarnContext(ctx, "Ticket channel deletion not recorded",
			"guild_id", guildID, "ticket_id", ticketID, "error", err)
	}
}

// repairThreads queues a thread membership repair for a guild. Bursts of
// events coalesce: one goroutine drains the pending guilds while later
// calls only add to the set, so a reconnect cannot drop any guild's repair.
func (m *Module) repairThreads(discordGuildID string) {
	m.repairMu.Lock()
	m.repairPending[discordGuildID] = struct{}{}
	if m.repairRunning {
		m.repairMu.Unlock()
		return
	}
	m.repairRunning = true
	for len(m.repairPending) > 0 {
		var next string
		for guildID := range m.repairPending {
			next = guildID
			break
		}
		delete(m.repairPending, next)
		m.repairMu.Unlock()
		m.repairGuildThreads(next)
		m.repairMu.Lock()
	}
	m.repairRunning = false
	m.repairMu.Unlock()
}

// repairGuildThreads removes former staff from every open ticket thread in
// a guild, whether or not tickets are still switched on. A minute bounds
// the whole pass; failures are logged and left for the next event.
func (m *Module) repairGuildThreads(discordGuildID string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	guildID, err := m.guilds.InternalID(ctx, discordGuildID)
	if err != nil {
		return
	}
	after := ""
	for {
		page, err := m.store.openThreadsAfter(ctx, guildID, after, repairPage)
		if err != nil {
			slog.ErrorContext(ctx, "Ticket permission repair lookup failed", "guild_id", guildID, "error", err)
			return
		}
		for _, ticket := range page {
			thread, err := m.channels.session.Channel(ticket.ThreadDiscordChannelID, rest(ctx)...)
			if err == nil && thread.GuildID == discordGuildID && thread.Type == discordgo.ChannelTypeGuildPrivateThread {
				err = m.channels.syncThreadMembers(ctx, discordGuildID, thread.ID, ticket.OwnerDiscordUserID)
			}
			if err != nil {
				slog.WarnContext(ctx, "Ticket permission repair incomplete", "guild_id", guildID, "ticket_id", ticket.ID)
			}
			if ctx.Err() != nil {
				return
			}
		}
		if len(page) < repairPage {
			return
		}
		after = page[len(page)-1].ID
	}
}
