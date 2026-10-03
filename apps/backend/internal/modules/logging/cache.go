package logging

import (
	"container/list"
	"slices"
	"sync"
	"time"
)

// CachedMessage is what the cache keeps of a recent message, so an edit or
// delete can show what it was.
type CachedMessage struct {
	GuildID, ChannelDiscordID, MessageDiscordID, AuthorDiscordUserID, Content string
	Attachments                                                               []AttachmentMetadata
	EmbedTypes                                                                []string
	CachedAt                                                                  time.Time
}

// MessageCache holds recent messages per guild, evicting the oldest past
// each guild's limit. It is safe for concurrent use.
type MessageCache struct {
	mu           sync.Mutex
	guilds       map[string]*guildCache
	limits       map[string]int
	defaultLimit int
}

// guildCache is one guild's messages in arrival order, indexed by ID.
type guildCache struct {
	order    list.List
	messages map[string]*list.Element
}

// NewMessageCache returns a cache whose guilds hold defaultLimit messages
// until SetGuildLimit says otherwise. A non-positive limit means 1000.
func NewMessageCache(defaultLimit int) *MessageCache {
	if defaultLimit < 1 {
		defaultLimit = 1000
	}
	return &MessageCache{guilds: map[string]*guildCache{}, limits: map[string]int{}, defaultLimit: defaultLimit}
}

// SetGuildLimit sets a guild's limit, evicting any excess now.
func (c *MessageCache) SetGuildLimit(guildID string, limit int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if limit < 1 {
		limit = 1
	}
	c.limits[guildID] = limit
	c.evict(guildID)
}

// Put caches a message, replacing an older copy in place.
func (c *MessageCache) Put(message CachedMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	g := c.guilds[message.GuildID]
	if g == nil {
		g = &guildCache{messages: make(map[string]*list.Element)}
		c.guilds[message.GuildID] = g
	}
	if element, ok := g.messages[message.MessageDiscordID]; ok {
		element.Value = cloneMessage(message)
	} else {
		g.messages[message.MessageDiscordID] = g.order.PushBack(cloneMessage(message))
	}
	c.evict(message.GuildID)
}

// Get returns a copy of a cached message.
func (c *MessageCache) Get(guildID, messageID string) (CachedMessage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	g := c.guilds[guildID]
	if g == nil {
		return CachedMessage{}, false
	}
	element, ok := g.messages[messageID]
	if !ok {
		return CachedMessage{}, false
	}
	return cloneMessage(element.Value.(CachedMessage)), true
}

// Delete removes a message and returns what was cached.
func (c *MessageCache) Delete(guildID, messageID string) (CachedMessage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	g := c.guilds[guildID]
	if g == nil {
		return CachedMessage{}, false
	}
	element, ok := g.messages[messageID]
	if !ok {
		return CachedMessage{}, false
	}
	delete(g.messages, messageID)
	g.order.Remove(element)
	return cloneMessage(element.Value.(CachedMessage)), true
}

// Len returns how many messages a guild has cached.
func (c *MessageCache) Len(guildID string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.guilds[guildID] == nil {
		return 0
	}
	return len(c.guilds[guildID].messages)
}

// evict removes oldest entries while the caller holds the cache mutex.
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
		oldest := g.order.Front()
		delete(g.messages, oldest.Value.(CachedMessage).MessageDiscordID)
		g.order.Remove(oldest)
	}
}

// cloneMessage copies the slices, so callers cannot change cached data.
func cloneMessage(m CachedMessage) CachedMessage {
	m.Attachments = slices.Clone(m.Attachments)
	m.EmbedTypes = slices.Clone(m.EmbedTypes)
	return m
}
