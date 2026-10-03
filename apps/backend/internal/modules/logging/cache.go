package logging

import (
	"container/list"
	"slices"
	"sync"
	"time"
)

// defaultCacheBytes bounds what the cache holds across all guilds, as an
// estimate of retained data rather than exact heap use.
const defaultCacheBytes int64 = 64 << 20

// CachedMessage is what the cache keeps of a recent message, so an edit or
// delete can show what it was.
type CachedMessage struct {
	GuildID, ChannelDiscordID, MessageDiscordID, AuthorDiscordUserID, Content string
	Attachments                                                               []AttachmentMetadata
	EmbedTypes                                                                []string
	CachedAt                                                                  time.Time
}

// MessageCache holds recent messages per guild. Each guild keeps at most
// its own limit, and all guilds together at most a shared byte budget;
// past either, the oldest messages go first. A replaced message keeps its
// age. It is safe for concurrent use.
type MessageCache struct {
	mu           sync.Mutex
	guilds       map[string]*guildCache
	limits       map[string]int
	global       list.List // of *cacheEntry, oldest first
	defaultLimit int
	budget, size int64
}

// guildCache is one guild's messages in arrival order, indexed by ID.
type guildCache struct {
	order    list.List // of *cacheEntry, oldest first
	messages map[string]*cacheEntry
}

// cacheEntry is one message, linked into both its guild's queue and the
// global one so a removal updates both and the byte count once.
type cacheEntry struct {
	message           CachedMessage
	bytes             int64
	inGuild, inGlobal *list.Element
}

// NewMessageCache returns a cache whose guilds hold defaultLimit messages
// until SetGuildLimit says otherwise. A non-positive limit means 1000.
func NewMessageCache(defaultLimit int) *MessageCache {
	if defaultLimit < 1 {
		defaultLimit = 1000
	}
	return &MessageCache{
		guilds:       map[string]*guildCache{},
		limits:       map[string]int{},
		defaultLimit: defaultLimit,
		budget:       defaultCacheBytes,
	}
}

// SetGuildLimit sets a guild's limit, evicting any excess now.
func (c *MessageCache) SetGuildLimit(guildID string, limit int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.limits[guildID] = max(limit, 1)
	c.evict(guildID)
}

// Put caches a message, replacing an older copy in place.
func (c *MessageCache) Put(message CachedMessage) { c.Replace(message) }

// Replace caches message and returns the copy it replaced, if any. A
// replacement without an author or channel keeps the old one's, since
// Discord's partial updates leave them out. A message too large for the
// whole budget is not cached, and drops its older copy rather than leave
// stale text behind.
func (c *MessageCache) Replace(message CachedMessage) (CachedMessage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	g := c.guilds[message.GuildID]
	var entry *cacheEntry
	if g != nil {
		entry = g.messages[message.MessageDiscordID]
	}
	var previous CachedMessage
	found := entry != nil
	if found {
		previous = cloneMessage(entry.message)
		if message.AuthorDiscordUserID == "" {
			message.AuthorDiscordUserID = previous.AuthorDiscordUserID
		}
		if message.ChannelDiscordID == "" {
			message.ChannelDiscordID = previous.ChannelDiscordID
		}
	}
	size := messageBytes(message)
	if size > c.budget {
		if found {
			c.remove(entry)
		}
		return previous, found
	}
	message = cloneMessage(message)
	switch {
	case found:
		c.size -= entry.bytes
		entry.message, entry.bytes = message, size
	default:
		if g == nil {
			g = &guildCache{messages: map[string]*cacheEntry{}}
			c.guilds[message.GuildID] = g
		}
		entry = &cacheEntry{message: message, bytes: size}
		entry.inGuild = g.order.PushBack(entry)
		entry.inGlobal = c.global.PushBack(entry)
		g.messages[message.MessageDiscordID] = entry
	}
	c.size += size
	c.evict(message.GuildID)
	for c.size > c.budget {
		c.remove(c.global.Front().Value.(*cacheEntry))
	}
	return previous, found
}

// Get returns a copy of a cached message.
func (c *MessageCache) Get(guildID, messageID string) (CachedMessage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if g := c.guilds[guildID]; g != nil {
		if entry := g.messages[messageID]; entry != nil {
			return cloneMessage(entry.message), true
		}
	}
	return CachedMessage{}, false
}

// Delete removes a message and returns what was cached.
func (c *MessageCache) Delete(guildID, messageID string) (CachedMessage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if g := c.guilds[guildID]; g != nil {
		if entry := g.messages[messageID]; entry != nil {
			c.remove(entry)
			return cloneMessage(entry.message), true
		}
	}
	return CachedMessage{}, false
}

// Len returns how many messages a guild has cached.
func (c *MessageCache) Len(guildID string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if g := c.guilds[guildID]; g != nil {
		return len(g.messages)
	}
	return 0
}

// remove unlinks entry from both queues. The caller holds the mutex.
func (c *MessageCache) remove(entry *cacheEntry) {
	guildID := entry.message.GuildID
	g := c.guilds[guildID]
	delete(g.messages, entry.message.MessageDiscordID)
	g.order.Remove(entry.inGuild)
	c.global.Remove(entry.inGlobal)
	c.size -= entry.bytes
	if len(g.messages) == 0 {
		delete(c.guilds, guildID)
	}
}

// evict removes a guild's oldest messages past its limit. The caller holds
// the mutex.
func (c *MessageCache) evict(guildID string) {
	g := c.guilds[guildID]
	if g == nil {
		return
	}
	limit := c.limits[guildID]
	if limit == 0 {
		limit = c.defaultLimit
	}
	for g.order.Len() > limit {
		c.remove(g.order.Front().Value.(*cacheEntry))
	}
}

// messageBytes estimates what caching m retains, generously counting the
// indexes around it so many small messages cannot slip past the budget.
func messageBytes(m CachedMessage) int64 {
	total := 640 + 2*len(m.GuildID) + len(m.ChannelDiscordID) + 2*len(m.MessageDiscordID) +
		len(m.AuthorDiscordUserID) + len(m.Content)
	for _, a := range m.Attachments {
		total += 96 + len(a.DiscordID) + len(a.Filename) + len(a.ContentType) + len(a.URL)
	}
	for _, embed := range m.EmbedTypes {
		total += 16 + len(embed)
	}
	return int64(total)
}

// cloneMessage copies the slices, so callers cannot change cached data.
func cloneMessage(m CachedMessage) CachedMessage {
	m.Attachments = slices.Clone(m.Attachments)
	m.EmbedTypes = slices.Clone(m.EmbedTypes)
	return m
}
