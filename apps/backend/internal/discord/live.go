package discord

import (
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bwmarrin/discordgo"
)

// Authorization reads guilds, roles, and members from the gateway's state
// while the session is live, and asks REST only for what state lacks. The
// gateway keeps that state current: GUILD_UPDATE and GUILD_ROLE_* events
// rewrite the guild and its roles, and with the Guild Members intent, which
// Quack always requests, GUILD_MEMBER_UPDATE and GUILD_MEMBER_REMOVE rewrite
// or drop members. A new session (READY) rebuilds state from scratch, and a
// resumed one replays what it missed, so state never trails Discord by more
// than event delivery.
//
// Large guilds do not send every member up front, so a member missing from
// state is fetched over REST and kept briefly in liveState's member overlay.
// Quack does not add fetched members to discordgo's state: a GUILD_MEMBER_REMOVE
// handled while the fetch was in flight would find nothing to remove, and the
// stale copy would then outlive the member. The overlay is dropped on every
// member event for that user and on READY, and expires after
// liveMemberTTL in case an event raced the fetch.

const (
	// liveMemberTTL bounds how long a member fetched over REST answers
	// authorization. Member events normally evict it much sooner.
	liveMemberTTL = 30 * time.Second
	// liveMemberLimit caps the overlay's size.
	liveMemberLimit = 10_000
)

// liveState tracks whether gateway state can answer authorization, and holds
// the members fetched over REST that state lacks.
type liveState struct {
	// live is set once a session is ready or resumed and cleared on
	// disconnect, while state may be missing events.
	live atomic.Bool

	mu      sync.Mutex
	members map[liveMemberKey]liveMember
	// generations counts member events per guild, so a fetch that raced
	// one is not cached.
	generations map[string]uint64
}

type liveMemberKey struct{ guildID, userID string }

type liveMember struct {
	member  discordgo.Member
	expires time.Time
}

// track follows the gateway: readiness, and member events that invalidate
// fetched members. It runs as a discordgo handler, after discordgo has
// already applied the event to state.
func (l *liveState) track(_ *discordgo.Session, event any) {
	switch event := event.(type) {
	case *discordgo.Ready:
		l.reset()
		l.live.Store(true)
	case *discordgo.Resumed:
		l.live.Store(true)
	case *discordgo.Disconnect:
		l.live.Store(false)
	case *discordgo.GuildMemberAdd:
		l.forget(event.Member)
	case *discordgo.GuildMemberUpdate:
		l.forget(event.Member)
	case *discordgo.GuildMemberRemove:
		l.forget(event.Member)
	case *discordgo.GuildDelete:
		if event.Guild != nil {
			l.forgetGuild(event.ID)
		}
	}
}

// reset drops every fetched member, for a new gateway session.
func (l *liveState) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.members = nil
	for guildID := range l.generations {
		l.generations[guildID]++
	}
}

// forget drops a fetched member after an event about them.
func (l *liveState) forget(member *discordgo.Member) {
	if member == nil || member.User == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.members, liveMemberKey{member.GuildID, member.User.ID})
	l.bump(member.GuildID)
}

// forgetGuild drops every fetched member of a guild Quack left or lost.
func (l *liveState) forgetGuild(guildID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for key := range l.members {
		if key.guildID == guildID {
			delete(l.members, key)
		}
	}
	l.bump(guildID)
}

// bump advances guildID's generation. l.mu must be held.
func (l *liveState) bump(guildID string) {
	if l.generations == nil {
		l.generations = make(map[string]uint64)
	}
	l.generations[guildID]++
}

// generation returns guildID's current generation, taken before a fetch and
// passed back to remember.
func (l *liveState) generation(guildID string) uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.generations[guildID]
}

// remember keeps a fetched member until now+liveMemberTTL, unless a member
// event in the guild arrived since generation was taken.
func (l *liveState) remember(member *discordgo.Member, generation uint64, now time.Time) {
	if member == nil || member.User == nil || !l.live.Load() {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.generations[member.GuildID] != generation {
		return
	}
	key := liveMemberKey{member.GuildID, member.User.ID}
	if l.members == nil {
		l.members = make(map[liveMemberKey]liveMember)
	}
	if _, ok := l.members[key]; !ok && len(l.members) >= liveMemberLimit {
		for k, entry := range l.members {
			if !now.Before(entry.expires) || len(l.members) >= liveMemberLimit {
				delete(l.members, k)
			}
		}
	}
	l.members[key] = liveMember{member: copyMember(member), expires: now.Add(liveMemberTTL)}
}

// fetched returns a member remembered from REST, if still fresh.
func (l *liveState) fetched(guildID, userID string, now time.Time) (*discordgo.Member, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry, ok := l.members[liveMemberKey{guildID, userID}]
	if !ok || !now.Before(entry.expires) {
		return nil, false
	}
	member := copyMember(&entry.member)
	return &member, true
}

// stateGuild returns a copy of the guild, its MFA level, and its roles from
// gateway state, or false when state cannot answer: the session is not
// live, the guild is not there, or it has not loaded yet.
func (b *Bot) stateGuild(guildID string) (*discordgo.Guild, bool) {
	state := b.Session.State
	if !b.live.live.Load() || state == nil {
		return nil, false
	}
	guild, err := state.Guild(guildID)
	if err != nil {
		return nil, false
	}
	state.RLock()
	defer state.RUnlock()
	if guild.Unavailable || guild.OwnerID == "" || len(guild.Roles) == 0 {
		return nil, false
	}
	roles := make([]*discordgo.Role, 0, len(guild.Roles))
	for _, role := range guild.Roles {
		if role != nil {
			copied := *role
			roles = append(roles, &copied)
		}
	}
	return &discordgo.Guild{
		ID:       guild.ID,
		Name:     guild.Name,
		Icon:     guild.Icon,
		OwnerID:  guild.OwnerID,
		MfaLevel: guild.MfaLevel,
		Roles:    roles,
	}, true
}

// liveMember returns a copy of a member from gateway state, or from members
// recently fetched over REST, or false when neither has them. A member state
// holds without a join time came from a presence update rather than a member
// payload, so its roles are unknown and it is not used.
func (b *Bot) liveMember(guildID, userID string) (*discordgo.Member, bool) {
	state := b.Session.State
	if !b.live.live.Load() || state == nil {
		return nil, false
	}
	if member, err := state.Member(guildID, userID); err == nil {
		state.RLock()
		copied := copyMember(member)
		state.RUnlock()
		if copied.User != nil && !copied.JoinedAt.IsZero() {
			return &copied, true
		}
	}
	return b.live.fetched(guildID, userID, time.Now())
}

// copyMember copies the parts of member that authorization reads, so later
// state updates cannot race with the caller.
func copyMember(member *discordgo.Member) discordgo.Member {
	copied := *member
	copied.Roles = slices.Clone(member.Roles)
	if member.User != nil {
		user := *member.User
		copied.User = &user
	}
	return copied
}
