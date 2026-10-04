package logging

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// budgetMessage is a message with 100 bytes of text.
func budgetMessage(guild, id string) CachedMessage {
	return CachedMessage{GuildID: guild, MessageDiscordID: id, ChannelDiscordID: "channel",
		AuthorDiscordUserID: "author", Content: strings.Repeat("x", 100)}
}

// assertCacheAccounting re-totals every entry and checks the cache's own
// count and budget.
func assertCacheAccounting(t *testing.T, c *MessageCache) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	var total int64
	entries := 0
	for _, g := range c.guilds {
		if len(g.messages) == 0 {
			t.Fatal("empty guild index retained")
		}
		for _, entry := range g.messages {
			total += messageBytes(entry.message)
			entries++
		}
	}
	if total != c.size || total > c.budget || entries != c.global.Len() {
		t.Fatalf("total=%d size=%d budget=%d entries=%d global=%d", total, c.size, c.budget, entries, c.global.Len())
	}
}

// TestAttachmentURLsCountTowardBudget covers long signed URLs on a message
// without text.
func TestAttachmentURLsCountTowardBudget(t *testing.T) {
	c := NewMessageCache(10)
	c.budget = 1024
	c.Put(CachedMessage{GuildID: "guild", MessageDiscordID: "message", Attachments: []AttachmentMetadata{
		{Filename: "proof.png", URL: "https://cdn.discordapp.com/" + strings.Repeat("x", 2048)},
	}})
	if _, ok := c.Get("guild", "message"); ok {
		t.Fatal("oversized attachment URL bypassed the budget")
	}
	assertCacheAccounting(t, c)
}

// TestCacheGlobalFIFOUsesAggregateBytes checks that the budget evicts the
// oldest message across guilds, and a replacement or read keeps its age.
func TestCacheGlobalFIFOUsesAggregateBytes(t *testing.T) {
	c := NewMessageCache(10)
	a, b, d := budgetMessage("a", "1"), budgetMessage("b", "2"), budgetMessage("d", "3")
	c.budget = messageBytes(a) + messageBytes(b)
	c.Put(a)
	c.Put(b)
	a.Content = strings.Repeat("y", 100)
	if previous, ok := c.Replace(a); !ok || previous.Content == a.Content {
		t.Fatal("replacement lost the old copy")
	}
	c.Get("a", "1")
	c.Put(d)
	if _, ok := c.Get("a", "1"); ok {
		t.Fatal("replacement refreshed the message's age")
	}
	if c.Len("b") != 1 || c.Len("d") != 1 {
		t.Fatal("evicted a newer guild")
	}
	assertCacheAccounting(t, c)
}

// TestCacheReplacementDeleteAndOversizeAccounting covers growth, shrinking,
// and an oversized replacement, which drops stale text but nothing else.
func TestCacheReplacementDeleteAndOversizeAccounting(t *testing.T) {
	c := NewMessageCache(10)
	c.budget = 4096
	a, b := budgetMessage("a", "1"), budgetMessage("b", "2")
	c.Put(a)
	c.Put(b)
	a.Content = strings.Repeat("large", 200)
	c.Replace(a)
	assertCacheAccounting(t, c)
	a.Content, a.AuthorDiscordUserID, a.ChannelDiscordID = "small", "", ""
	c.Replace(a)
	if saved, _ := c.Get("a", "1"); saved.AuthorDiscordUserID != "author" || saved.ChannelDiscordID != "channel" {
		t.Fatal("partial replacement lost the author or channel")
	}
	assertCacheAccounting(t, c)
	a.Content = strings.Repeat("x", 5000)
	if old, ok := c.Replace(a); !ok || old.Content != "small" {
		t.Fatal("oversized replacement lost the previous copy")
	}
	if _, ok := c.Get("a", "1"); ok {
		t.Fatal("stale text survived an oversized replacement")
	}
	a.MessageDiscordID = "new"
	c.Put(a)
	if c.Len("b") != 1 {
		t.Fatal("oversized insert evicted unrelated messages")
	}
	if _, ok := c.Delete("b", "2"); !ok {
		t.Fatal("delete lost the cached message")
	}
	assertCacheAccounting(t, c)
	if c.size != 0 || len(c.guilds) != 0 {
		t.Fatal("delete kept bytes or an empty guild")
	}
}

// TestCacheGuildLimitsProtectOtherGuilds checks a guild's own limit
// evicts only its messages.
func TestCacheGuildLimitsProtectOtherGuilds(t *testing.T) {
	c := NewMessageCache(10)
	c.SetGuildLimit("a", 2)
	c.Put(budgetMessage("b", "first"))
	for i := range 20 {
		c.Put(budgetMessage("a", fmt.Sprint(i)))
	}
	if c.Len("a") != 2 || c.Len("b") != 1 {
		t.Fatal("guild limits leaked across guilds")
	}
	c.SetGuildLimit("a", 1)
	if _, ok := c.Get("a", "19"); !ok {
		t.Fatal("lowering the limit evicted the newest message")
	}
	if c.Len("b") != 1 {
		t.Fatal("lowering a limit affected another guild")
	}
	assertCacheAccounting(t, c)
}

// TestCacheCopiesAndConcurrentBudget checks callers cannot change cached
// slices, and accounting holds under concurrent writers.
func TestCacheCopiesAndConcurrentBudget(t *testing.T) {
	c := NewMessageCache(5)
	c.budget = 8192
	m := budgetMessage("a", "1")
	m.Attachments = []AttachmentMetadata{{Filename: "original"}}
	m.EmbedTypes = []string{"rich"}
	c.Put(m)
	m.Attachments[0].Filename, m.EmbedTypes[0] = "changed", "changed"
	saved, _ := c.Get("a", "1")
	if saved.Attachments[0].Filename != "original" || saved.EmbedTypes[0] != "rich" {
		t.Fatal("input changed cached slices")
	}
	saved.Attachments[0].Filename = "changed"
	if again, _ := c.Get("a", "1"); again.Attachments[0].Filename != "original" {
		t.Fatal("output changed cached slices")
	}
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			guild := fmt.Sprint(i)
			for j := range 40 {
				c.Put(budgetMessage(guild, fmt.Sprint(j)))
				c.Get(guild, fmt.Sprint(j))
				if j%3 == 0 {
					c.Delete(guild, fmt.Sprint(j))
				}
			}
		})
	}
	wg.Wait()
	assertCacheAccounting(t, c)
}
