package logging

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discord"
	"github.com/quackdiscord/bot/internal/modules"
)

// TestExternalBanEventSeparatesActorAndTarget skips Quack's own bans,
// which its cases already record, but keeps another moderator's.
func TestExternalBanEventSeparatesActorAndTarget(t *testing.T) {
	for _, kind := range []discordgo.AuditLogAction{discordgo.AuditLogActionMemberBanAdd, discordgo.AuditLogActionMemberBanRemove} {
		entry := &discordgo.GuildAuditLogEntryCreate{GuildID: "guild", AuditLogEntry: &discordgo.AuditLogEntry{
			ID: "audit", TargetID: "member", UserID: "quack", ActionType: &kind, Reason: "rule",
		}}
		if _, ok := externalBanEvent(entry, "quack"); ok {
			t.Fatal("Quack's own action was logged")
		}
		entry.UserID = "moderator"
		event, ok := externalBanEvent(entry, "quack")
		if !ok || event.ActorDiscordUserID != "moderator" || event.Metadata["target_id"] != "member" || event.Metadata["reason"] != "rule" {
			t.Fatalf("external action lost attribution: %+v", event)
		}
		want := map[discordgo.AuditLogAction]EventType{
			discordgo.AuditLogActionMemberBanAdd:    DiscordBan,
			discordgo.AuditLogActionMemberBanRemove: DiscordUnban,
		}[kind]
		if event.Type != want {
			t.Fatalf("event type = %s, want %s", event.Type, want)
		}
	}
	if _, ok := externalBanEvent(nil, "quack"); ok {
		t.Fatal("nil audit entry accepted")
	}
}

// recorder records delivered log text.
type recorder struct {
	mu       sync.Mutex
	payloads []string
}

func (*recorder) ValidateStaffOnlyChannel(context.Context, string, string) error { return nil }

func (r *recorder) SendStaffLog(_ context.Context, _, _ string, message discord.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.payloads = append(r.payloads, message.Content)
	return nil
}

// TestGatewayKeepsBotAuthorsAndDeletionContext runs a create, an edit
// without its author, and a delete through the gateway handlers and the
// pool. Another bot's message is logged; Quack's own is not.
func TestGatewayKeepsBotAuthorsAndDeletionContext(t *testing.T) {
	ctx := context.Background()
	rec := &recorder{}
	service := NewService(newTestRegistry(t), rec, nil)
	actor := modules.Actor{GuildID: testGuild.ID, CanManage: true}
	if _, err := service.UpdateSettings(ctx, actor, true, Defaults().RouteAllTo("logs")); err != nil {
		t.Fatal(err)
	}
	m := newTestModule(service)
	m.pool.Start(ctx)
	session := &discordgo.Session{State: discordgo.NewState()}
	session.State.User = &discordgo.User{ID: "quack"}

	files := []*discordgo.MessageAttachment{{ID: "attachment", Filename: "proof.png"}}
	edited := time.Now()
	m.onMessageCreate(session, &discordgo.MessageCreate{Message: &discordgo.Message{
		ID: "own", GuildID: "guild", ChannelID: "source", Author: &discordgo.User{ID: "quack", Bot: true}, Content: "receipt",
	}})
	m.onMessageUpdate(session, &discordgo.MessageUpdate{Message: &discordgo.Message{
		ID: "own", GuildID: "guild", ChannelID: "source", Author: &discordgo.User{ID: "quack", Bot: true}, Content: "receipt v2", EditedTimestamp: &edited,
	}})
	m.onMessageCreate(session, &discordgo.MessageCreate{Message: &discordgo.Message{
		ID: "message", GuildID: "guild", ChannelID: "source", Author: &discordgo.User{ID: "other-bot", Bot: true}, Content: "before", Attachments: files,
	}})
	// A link preview arriving is an update without an edit timestamp.
	m.onMessageUpdate(session, &discordgo.MessageUpdate{Message: &discordgo.Message{
		ID: "message", GuildID: "guild", ChannelID: "source", Content: "before", Attachments: files,
	}})
	m.onMessageUpdate(session, &discordgo.MessageUpdate{Message: &discordgo.Message{
		ID: "message", GuildID: "guild", ChannelID: "source", Content: "after", EditedTimestamp: &edited, Attachments: files,
	}})
	m.onMessageDelete(session, &discordgo.MessageDelete{Message: &discordgo.Message{ID: "message", GuildID: "guild", ChannelID: "source"}})
	if err := m.Stop(ctx); err != nil {
		t.Fatal(err)
	}

	if len(rec.payloads) != 2 {
		t.Fatalf("logs = %q, want one edit and one delete", rec.payloads)
	}
	// The pool's workers may deliver in either order.
	edit, deletion := rec.payloads[0], rec.payloads[1]
	if strings.Contains(edit, "was deleted") {
		edit, deletion = deletion, edit
	}
	for _, want := range []string{"A message from <@other-bot> was edited in <#source>.", "Before:\n> before", "After:\n> after", "Files after: proof.png"} {
		if !strings.Contains(edit, want) {
			t.Fatalf("edit log lacks %q: %s", want, edit)
		}
	}
	for _, want := range []string{"A message from <@other-bot> was deleted in <#source>.", "> after", "Files: proof.png"} {
		if !strings.Contains(deletion, want) {
			t.Fatalf("delete log lacks %q: %s", want, deletion)
		}
	}
}

// newTestModule returns a module over service whose deletion batches wait
// until Stop.
func newTestModule(service *Service) *Module {
	m := &Module{service: service, guilds: modules.NewGuilds(guildStore{}), pool: NewPool(service)}
	m.deletes = newDeleteBatcher(time.Hour, m.submitUncachedDeletes)
	return m
}

// TestUncachedDeletesAreSummarizedPerChannel deletes messages Quack never
// saw, as when a member clears old history: each channel gets one summary,
// a lone deletion stays a plain one, and a message cached before the batch
// is posted keeps its content.
func TestUncachedDeletesAreSummarizedPerChannel(t *testing.T) {
	ctx := context.Background()
	rec := &recorder{}
	service := NewService(newTestRegistry(t), rec, nil)
	actor := modules.Actor{GuildID: testGuild.ID, CanManage: true}
	if _, err := service.UpdateSettings(ctx, actor, true, Defaults().RouteAllTo("logs")); err != nil {
		t.Fatal(err)
	}
	m := newTestModule(service)
	m.pool.Start(ctx)
	session := &discordgo.Session{State: discordgo.NewState()}
	deleteMessage := func(id, channelID string) {
		m.onMessageDelete(session, &discordgo.MessageDelete{Message: &discordgo.Message{ID: id, GuildID: "guild", ChannelID: channelID}})
	}
	deleteMessage("old-1", "chat")
	deleteMessage("old-2", "chat")
	deleteMessage("late", "chat")
	deleteMessage("old-3", "chat")
	deleteMessage("lone", "other")
	// The gateway delivered this message's create after its delete.
	m.onMessageCreate(session, &discordgo.MessageCreate{Message: &discordgo.Message{
		ID: "late", GuildID: "guild", ChannelID: "chat", Author: &discordgo.User{ID: "member"}, Content: "hello",
	}})
	if len(rec.payloads) != 0 {
		t.Fatalf("uncached deletions were posted before their window ended: %q", rec.payloads)
	}
	if err := m.Stop(ctx); err != nil {
		t.Fatal(err)
	}

	all := strings.Join(rec.payloads, "\n---\n")
	if len(rec.payloads) != 3 {
		t.Fatalf("logs = %q, want a summary, a lone deletion, and the late message", rec.payloads)
	}
	for _, want := range []string{
		"3 messages were deleted in <#chat>.",
		"Messages: old-1, old-2, old-3",
		"A message from A member was deleted in <#other>.",
		"A message from <@member> was deleted in <#chat>.",
		"> hello",
	} {
		if !strings.Contains(all, want) {
			t.Fatalf("logs lack %q:\n%s", want, all)
		}
	}
}

// TestDeleteBatcherPostsFullAndExpiredBatches checks a full batch is posted
// at once and a partial one when its window ends, never twice.
func TestDeleteBatcherPostsFullAndExpiredBatches(t *testing.T) {
	var mu sync.Mutex
	var batches [][]string
	posted := make(chan struct{}, 4)
	b := newDeleteBatcher(20*time.Millisecond, func(_, _ string, ids []string) {
		mu.Lock()
		batches = append(batches, ids)
		mu.Unlock()
		posted <- struct{}{}
	})
	for i := range maxUncachedDeletes + 1 {
		b.Add("guild", "chat", strconv.Itoa(i))
	}
	<-posted
	<-posted
	b.Flush()
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(batches) != 2 || len(batches[0]) != maxUncachedDeletes || len(batches[1]) != 1 {
		t.Fatalf("batches = %v, want one full batch then the remainder", batches)
	}
}
