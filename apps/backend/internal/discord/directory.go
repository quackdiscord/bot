package discord

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

// The dashboard's display lookups are cached so that a page naming dozens
// of users costs Discord a handful of requests at most. Users change names
// and avatars rarely and channel pickers tolerate a short delay, so the
// staleness is acceptable for display data.
const (
	directoryUserTTL       = 10 * time.Minute
	directoryUserLimit     = 5000
	directoryChannelTTL    = 30 * time.Second
	directoryChannelLimit  = 1000
	directoryLookupWorkers = 8
)

// DirectoryUser is how the dashboard shows a Discord user. It mirrors
// api.DirectoryUser; the composition root converts between the two.
type DirectoryUser struct {
	ID         string
	Username   string
	GlobalName string
	// Nick is the guild nickname, or "" for none or a non-member.
	Nick string
	// DisplayName is Nick, else GlobalName, else Username.
	DisplayName string
	// AvatarURL is the guild avatar, else the user avatar, else Discord's
	// default avatar. It is never empty.
	AvatarURL string
	Bot       bool
	InGuild   bool
}

// DirectoryChannel is one guild channel for the dashboard's channel
// pickers. Type is one of the directoryChannel* names.
type DirectoryChannel struct {
	ID       string
	Name     string
	Type     string
	ParentID string
	Position int
}

// The channel kinds the directory lists, as the dashboard names them.
const (
	directoryChannelText         = "text"
	directoryChannelAnnouncement = "announcement"
	directoryChannelForum        = "forum"
	directoryChannelVoice        = "voice"
	directoryChannelStage        = "stage"
	directoryChannelCategory     = "category"
)

// directoryEntry is a cached user lookup. found is false for a user
// Discord does not know, so repeated lookups of a deleted account stay
// cheap too.
type directoryEntry struct {
	user  DirectoryUser
	found bool
}

// SearchMembers returns up to limit current members of the guild whose
// username or nickname starts with query. Searches are not cached, but the
// members they find are, for later LookupUsers calls.
func (b *Bot) SearchMembers(ctx context.Context, guildID, query string, limit int) ([]DirectoryUser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	members, err := b.Session.GuildMembersSearch(guildID, query, limit, rest(ctx)...)
	if err != nil {
		return nil, classify("directory_member_search", err, false)
	}
	now := time.Now()
	users := make([]DirectoryUser, 0, len(members))
	for _, member := range members {
		if member == nil || member.User == nil {
			continue
		}
		user := memberDirectoryUser(guildID, member)
		b.directoryUsers.put(directoryUserKey(guildID, user.ID), directoryEntry{user: user, found: true},
			now, directoryUserTTL, directoryUserLimit)
		users = append(users, user)
	}
	return users, nil
}

// LookupUsers describes each user in userIDs, in order: as a guild member
// when they are one, else as a plain Discord user. Users Discord does not
// know are left out. Uncached users are fetched concurrently, a few at a
// time; the first failure cancels the rest and is returned.
func (b *Bot) LookupUsers(ctx context.Context, guildID string, userIDs []string) ([]DirectoryUser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	now := time.Now()
	entries := make([]directoryEntry, len(userIDs))
	var missing []int
	for i, id := range userIDs {
		entry, ok := b.directoryUsers.get(directoryUserKey(guildID, id), now)
		if ok {
			entries[i] = entry
		} else {
			missing = append(missing, i)
		}
	}
	if len(missing) > 0 {
		if err := b.fetchDirectoryUsers(ctx, guildID, userIDs, missing, entries); err != nil {
			return nil, err
		}
	}
	users := make([]DirectoryUser, 0, len(entries))
	for _, entry := range entries {
		if entry.found {
			users = append(users, entry.user)
		}
	}
	return users, nil
}

// fetchDirectoryUsers fills entries[i] for each index in missing from
// Discord and caches the results.
func (b *Bot) fetchDirectoryUsers(ctx context.Context, guildID string, userIDs []string, missing []int, entries []directoryEntry) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
	)
	slots := make(chan struct{}, directoryLookupWorkers)
	for _, i := range missing {
		wg.Go(func() {
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-slots }()
			entry, err := b.fetchDirectoryUser(ctx, guildID, userIDs[i])
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
					cancel()
				}
				mu.Unlock()
				return
			}
			entries[i] = entry
			b.directoryUsers.put(directoryUserKey(guildID, userIDs[i]), entry,
				time.Now(), directoryUserTTL, directoryUserLimit)
		})
	}
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	// Canceled before any lookup failed: the caller's context ended.
	return context.Cause(ctx)
}

// fetchDirectoryUser looks one user up as a guild member, falling back to
// the plain user when Discord says they are not a member.
func (b *Bot) fetchDirectoryUser(ctx context.Context, guildID, userID string) (directoryEntry, error) {
	member, err := b.Session.GuildMember(guildID, userID, rest(ctx)...)
	if err == nil && member != nil && member.User != nil {
		return directoryEntry{user: memberDirectoryUser(guildID, member), found: true}, nil
	}
	if err != nil && statusCode(err) != http.StatusNotFound {
		return directoryEntry{}, classify("directory_member", err, false)
	}
	user, err := b.Session.User(userID, rest(ctx)...)
	if err != nil {
		if statusCode(err) == http.StatusNotFound {
			return directoryEntry{}, nil
		}
		return directoryEntry{}, classify("directory_user", err, false)
	}
	if user == nil {
		return directoryEntry{}, nil
	}
	return directoryEntry{user: plainDirectoryUser(user), found: true}, nil
}

// Channels lists the guild's text, announcement, forum, voice, stage, and
// category channels in Discord's sidebar order. Lists are cached per guild
// for directoryChannelTTL.
func (b *Bot) Channels(ctx context.Context, guildID string) ([]DirectoryChannel, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if channels, ok := b.directoryChannels.get(guildID, time.Now()); ok {
		return channels, nil
	}
	channels, err := b.Session.GuildChannels(guildID, rest(ctx)...)
	if err != nil {
		return nil, classify("directory_channels", err, false)
	}
	ordered := sidebarOrder(channels)
	b.directoryChannels.put(guildID, ordered, time.Now(), directoryChannelTTL, directoryChannelLimit)
	return ordered, nil
}

// directoryUserKey keys the user cache by guild, since membership,
// nicknames, and guild avatars differ per guild.
func directoryUserKey(guildID, userID string) string {
	return guildID + ":" + userID
}

// memberDirectoryUser describes a guild member. member.User must be set.
func memberDirectoryUser(guildID string, member *discordgo.Member) DirectoryUser {
	user := plainDirectoryUser(member.User)
	user.Nick = strings.TrimSpace(member.Nick)
	user.DisplayName = displayName(member)
	user.InGuild = true
	if member.Avatar != "" {
		user.AvatarURL = fmt.Sprintf("https://cdn.discordapp.com/guilds/%s/users/%s/avatars/%s.%s",
			guildID, user.ID, member.Avatar, imageExtension(member.Avatar))
	}
	return user
}

// plainDirectoryUser describes a user without any guild details.
func plainDirectoryUser(user *discordgo.User) DirectoryUser {
	return DirectoryUser{
		ID:          user.ID,
		Username:    user.Username,
		GlobalName:  user.GlobalName,
		DisplayName: displayName(&discordgo.Member{User: user}),
		AvatarURL:   userAvatarURL(user.ID, user.Avatar),
		Bot:         user.Bot,
	}
}

// userAvatarURL returns the user's avatar, or Discord's default avatar when
// they have none.
func userAvatarURL(userID, avatarHash string) string {
	if avatarHash == "" {
		return fmt.Sprintf("https://cdn.discordapp.com/embed/avatars/%d.png", defaultAvatarIndex(userID))
	}
	return fmt.Sprintf("https://cdn.discordapp.com/avatars/%s/%s.%s", userID, avatarHash, imageExtension(avatarHash))
}

// defaultAvatarIndex picks one of Discord's six default avatars from the
// user ID, as Discord does for users without a discriminator.
func defaultAvatarIndex(userID string) int {
	id, _ := strconv.ParseUint(userID, 10, 64)
	return int((id >> 22) % 6)
}

// imageExtension is gif for animated ("a_") image hashes and png otherwise.
func imageExtension(hash string) string {
	if strings.HasPrefix(hash, "a_") {
		return "gif"
	}
	return "png"
}

// directoryChannelType names a channel's kind for the directory, or ""
// for kinds it leaves out, such as threads.
func directoryChannelType(t discordgo.ChannelType) string {
	switch t {
	case discordgo.ChannelTypeGuildText:
		return directoryChannelText
	case discordgo.ChannelTypeGuildNews:
		return directoryChannelAnnouncement
	case discordgo.ChannelTypeGuildForum:
		return directoryChannelForum
	case discordgo.ChannelTypeGuildVoice:
		return directoryChannelVoice
	case discordgo.ChannelTypeGuildStageVoice:
		return directoryChannelStage
	case discordgo.ChannelTypeGuildCategory:
		return directoryChannelCategory
	}
	return ""
}

// sidebarOrder keeps the channel kinds the directory lists and orders them
// as Discord's sidebar does: uncategorized channels first, then each
// category by position followed by its channels. Within a group, text-like
// channels come before voice and stage channels, each by position. Ties
// break by ID, oldest first. A channel whose category is missing counts as
// uncategorized.
func sidebarOrder(channels []*discordgo.Channel) []DirectoryChannel {
	var categories []DirectoryChannel
	grouped := map[string][]DirectoryChannel{}
	isCategory := map[string]bool{}
	for _, channel := range channels {
		if channel != nil && channel.Type == discordgo.ChannelTypeGuildCategory {
			isCategory[channel.ID] = true
		}
	}
	for _, channel := range channels {
		if channel == nil {
			continue
		}
		kind := directoryChannelType(channel.Type)
		if kind == "" {
			continue
		}
		entry := DirectoryChannel{
			ID: channel.ID, Name: channel.Name, Type: kind,
			ParentID: channel.ParentID, Position: channel.Position,
		}
		if kind == directoryChannelCategory {
			entry.ParentID = ""
			categories = append(categories, entry)
			continue
		}
		group := ""
		if isCategory[channel.ParentID] {
			group = channel.ParentID
		}
		grouped[group] = append(grouped[group], entry)
	}
	slices.SortFunc(categories, compareChannels)
	ordered := make([]DirectoryChannel, 0, len(channels))
	ordered = append(ordered, sortedGroup(grouped[""])...)
	for _, category := range categories {
		ordered = append(ordered, category)
		ordered = append(ordered, sortedGroup(grouped[category.ID])...)
	}
	return ordered
}

// sortedGroup orders the channels under one category heading.
func sortedGroup(group []DirectoryChannel) []DirectoryChannel {
	slices.SortFunc(group, func(a, b DirectoryChannel) int {
		return cmp.Or(cmp.Compare(voiceRank(a), voiceRank(b)), compareChannels(a, b))
	})
	return group
}

// voiceRank sorts voice and stage channels below text-like ones.
func voiceRank(c DirectoryChannel) int {
	if c.Type == directoryChannelVoice || c.Type == directoryChannelStage {
		return 1
	}
	return 0
}

// compareChannels orders by position, then by snowflake age.
func compareChannels(a, b DirectoryChannel) int {
	return cmp.Or(
		cmp.Compare(a.Position, b.Position),
		cmp.Compare(len(a.ID), len(b.ID)),
		strings.Compare(a.ID, b.ID),
	)
}

// ttlCache is a small, bounded, thread-safe map whose entries expire. Its
// zero value is ready to use. When full, it drops expired entries and then,
// if still full, arbitrary ones; it is a load shield, not an LRU.
type ttlCache[V any] struct {
	mu      sync.Mutex
	entries map[string]ttlEntry[V]
}

type ttlEntry[V any] struct {
	value   V
	expires time.Time
}

// get returns the live value for key at now.
func (c *ttlCache[V]) get(key string, now time.Time) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok || !now.Before(entry.expires) {
		var zero V
		return zero, false
	}
	return entry.value, true
}

// put stores value under key until now+ttl, keeping at most limit entries.
func (c *ttlCache[V]) put(key string, value V, now time.Time, ttl time.Duration, limit int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]ttlEntry[V])
	}
	if _, ok := c.entries[key]; !ok && len(c.entries) >= limit {
		for k, entry := range c.entries {
			if !now.Before(entry.expires) {
				delete(c.entries, k)
			}
		}
		for k := range c.entries {
			if len(c.entries) < limit {
				break
			}
			delete(c.entries, k)
		}
	}
	c.entries[key] = ttlEntry[V]{value: value, expires: now.Add(ttl)}
}
