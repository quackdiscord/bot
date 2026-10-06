package tickets_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/tickets"
	"gorm.io/gorm"
)

func countJournal(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var count int64
	if err := db.Table("ticket_message_journal").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	return count
}

// TestJournalKeepsDeletedOriginalTextAcrossRestart covers duplicate
// gateway events, edits, ordering, and thread and guild isolation.
func TestJournalKeepsDeletedOriginalTextAcrossRestart(t *testing.T) {
	db, service, _ := setup(t)
	ctx := context.Background()
	client := &discordFake{}
	actor := modules.Actor{GuildID: "guild-a", DiscordUserID: "member"}
	ticket, err := tickets.NewDiscordAdapter(service, client).Open(ctx, actor)
	if err != nil {
		t.Fatal(err)
	}
	first := tickets.TranscriptMessage{MessageID: "100", AuthorID: "member", AuthorName: "Member", Body: "original deleted text", SentAt: time.Now().UTC()}
	second := first
	second.MessageID, second.Body = "101", "original surviving text"
	edited := second
	edited.Body = "edited text"
	for _, message := range []tickets.TranscriptMessage{first, second, first, edited} {
		if err := service.RecordMessage(ctx, actor.GuildID, ticket.ThreadDiscordChannelID, message); err != nil {
			t.Fatal(err)
		}
	}
	if err := service.RecordMessage(ctx, "other-guild", ticket.ThreadDiscordChannelID, first); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordMessage(ctx, actor.GuildID, "unrelated-channel", first); err != nil {
		t.Fatal(err)
	}
	if count := countJournal(t, db); count != 2 {
		t.Fatalf("journal entries = %d, want 2", count)
	}
	// A restarted process finds the open ticket again. The deleted message
	// is gone from the final history and the survivor was edited.
	restarted := tickets.NewService(modules.NewRegistry(db), tickets.NewStore(db), nil)
	client.messages = []tickets.TranscriptMessage{edited, edited}
	if _, err := tickets.NewDiscordAdapter(restarted, client).Close(ctx, actor, ticket.ID); err != nil {
		t.Fatal(err)
	}
	content := transcriptOf(t, restarted, actor, ticket.ID)
	if strings.Count(content, first.Body) != 1 || strings.Count(content, second.Body) != 1 ||
		strings.Contains(content, "edited text") || strings.Index(content, first.Body) > strings.Index(content, second.Body) {
		t.Fatalf("transcript lost the original text:\n%s", content)
	}
	if client.deleteAttempts != 1 || client.transcriptPublishes != 1 {
		t.Fatal("close did not finish")
	}
}

// TestJournalFailureBlocksDeletionUntilFlushed keeps a failed journal write
// from being skipped by the close-time history capture.
func TestJournalFailureBlocksDeletionUntilFlushed(t *testing.T) {
	db, service, _ := setup(t)
	ctx := context.Background()
	client := &discordFake{messages: []tickets.TranscriptMessage{}}
	adapter := tickets.NewDiscordAdapter(service, client)
	actor := modules.Actor{GuildID: "guild-a", DiscordUserID: "member"}
	ticket, err := adapter.Open(ctx, actor)
	if err != nil {
		t.Fatal(err)
	}
	fail := true
	if err := db.Callback().Create().Before("gorm:create").Register("fail_journal", func(tx *gorm.DB) {
		if fail && tx.Statement.Table == "ticket_message_journal" {
			_ = tx.AddError(errors.New("journal storage unavailable"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	original := tickets.TranscriptMessage{MessageID: "deleted", AuthorID: "member", Body: "buffered original", SentAt: time.Now().UTC()}
	if err := service.RecordMessage(ctx, actor.GuildID, ticket.ThreadDiscordChannelID, original); err == nil {
		t.Fatal("write failure hidden")
	}
	if _, err := adapter.Close(ctx, actor, ticket.ID); err == nil || client.deleteAttempts != 0 || client.transcriptPublishes != 0 {
		t.Fatalf("close skipped the failed write: %v", err)
	}
	fail = false
	if _, err := adapter.Close(ctx, actor, ticket.ID); err != nil {
		t.Fatal(err)
	}
	if content := transcriptOf(t, service, actor, ticket.ID); !strings.Contains(content, original.Body) {
		t.Fatalf("retry lost the original: %q", content)
	}
}

// TestJournalOverflowBlocksClose refuses to close when the retry buffer
// overflowed instead of saving a transcript that looks complete.
func TestJournalOverflowBlocksClose(t *testing.T) {
	db, service, _ := setup(t)
	ctx := context.Background()
	client := &discordFake{}
	adapter := tickets.NewDiscordAdapter(service, client)
	actor := modules.Actor{GuildID: "guild-a", DiscordUserID: "member"}
	ticket, err := adapter.Open(ctx, actor)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Callback().Create().Before("gorm:create").Register("full_journal", func(tx *gorm.DB) {
		if tx.Statement.Table == "ticket_message_journal" {
			_ = tx.AddError(errors.New("unavailable"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	for i := range 1001 {
		message := tickets.TranscriptMessage{MessageID: fmt.Sprint(i), AuthorID: "member", Body: "original", SentAt: time.Now()}
		err = service.RecordMessage(ctx, actor.GuildID, ticket.ThreadDiscordChannelID, message)
	}
	if !errors.Is(err, tickets.ErrJournalIncomplete) {
		t.Fatalf("overflow: %v", err)
	}
	if err := db.Callback().Create().Remove("full_journal"); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Close(ctx, actor, ticket.ID); !errors.Is(err, tickets.ErrJournalIncomplete) || client.deleteAttempts != 0 {
		t.Fatalf("overflow allowed the delete: %v", err)
	}
}

// TestJournalCloseWaitsForWriteInFlight has a write in progress when the
// close starts; the close waits and includes it.
func TestJournalCloseWaitsForWriteInFlight(t *testing.T) {
	db, service, _ := setup(t)
	ctx := context.Background()
	client := &discordFake{messages: []tickets.TranscriptMessage{}}
	adapter := tickets.NewDiscordAdapter(service, client)
	actor := modules.Actor{GuildID: "guild-a", DiscordUserID: "member"}
	ticket, err := adapter.Open(ctx, actor)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	if err := db.Callback().Create().Before("gorm:create").Register("blocked_journal", func(tx *gorm.DB) {
		if tx.Statement.Table == "ticket_message_journal" {
			close(entered)
			<-release
		}
	}); err != nil {
		t.Fatal(err)
	}
	written := make(chan error, 1)
	go func() {
		message := tickets.TranscriptMessage{MessageID: "received", AuthorID: "member", Body: "received before close", SentAt: time.Now().UTC()}
		written <- service.RecordMessage(ctx, actor.GuildID, ticket.ThreadDiscordChannelID, message)
	}()
	<-entered
	closed := make(chan error, 1)
	go func() {
		_, err := adapter.Close(ctx, actor, ticket.ID)
		closed <- err
	}()
	select {
	case err := <-closed:
		t.Fatalf("close passed a write in flight: %v", err)
	case <-time.After(10 * time.Millisecond):
	}
	close(release)
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if content := transcriptOf(t, service, actor, ticket.ID); !strings.Contains(content, "received before close") {
		t.Fatalf("write lost: %q", content)
	}
}

// TestJournalSharesTranscriptRetention purges journaled text with the
// transcript, not before and not never.
func TestJournalSharesTranscriptRetention(t *testing.T) {
	db, service, _ := setup(t)
	ctx := context.Background()
	actor := modules.Actor{GuildID: "guild-a", DiscordUserID: "member"}
	ticket, err := tickets.NewDiscordAdapter(service, &discordFake{}).Open(ctx, actor)
	if err != nil {
		t.Fatal(err)
	}
	message := tickets.TranscriptMessage{MessageID: "original", AuthorID: "member", Body: "text", SentAt: time.Now()}
	if err := service.RecordMessage(ctx, actor.GuildID, ticket.ThreadDiscordChannelID, message); err != nil {
		t.Fatal(err)
	}
	if _, err := service.PurgeExpiredTranscripts(ctx); err != nil {
		t.Fatal(err)
	}
	if count := countJournal(t, db); count != 1 {
		t.Fatalf("open ticket's journal purged: %d", count)
	}
	if _, err := service.ResolveWithHistory(ctx, actor, ticket.ID, nil); err != nil {
		t.Fatal(err)
	}
	var entry struct{ ExpiresAt *time.Time }
	if err := db.Table("ticket_message_journal").Select("expires_at").Take(&entry).Error; err != nil || entry.ExpiresAt == nil {
		t.Fatalf("journal has no expiry: %v", err)
	}
	past := time.Now().Add(-time.Hour)
	if err := db.Table("ticket_message_journal").Where("1 = 1").Update("expires_at", past).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Table("ticket_transcripts").Where("1 = 1").Update("expires_at", past).Error; err != nil {
		t.Fatal(err)
	}
	if purged, err := service.PurgeExpiredTranscripts(ctx); err != nil || purged != 1 {
		t.Fatalf("purged = %d, %v", purged, err)
	}
	if count := countJournal(t, db); count != 0 {
		t.Fatalf("expired journal kept: %d", count)
	}
}

// openingJournalFake posts and deletes a message while the owner is being
// invited.
type openingJournalFake struct {
	*discordFake
	service *tickets.Service
}

func (f *openingJournalFake) EnsureAccess(ctx context.Context, guild, thread, owner string) error {
	if known, ok := f.service.KnownMessageThread(thread); !ok || known != guild {
		return errors.New("owner invited before the journal knew the thread")
	}
	message := tickets.TranscriptMessage{MessageID: "early", AuthorID: owner, Body: "deleted during invitation", SentAt: time.Now().UTC()}
	if err := f.service.RecordMessage(ctx, guild, thread, message); err != nil {
		return err
	}
	return f.discordFake.EnsureAccess(ctx, guild, thread, owner)
}

// TestJournalKeepsMessagesFromOpening covers the gap between inviting the
// owner and the rest of the access sync.
func TestJournalKeepsMessagesFromOpening(t *testing.T) {
	_, service, _ := setup(t)
	ctx := context.Background()
	client := &openingJournalFake{discordFake: &discordFake{permissionError: errors.New("sync failed after invitation")}, service: service}
	adapter := tickets.NewDiscordAdapter(service, client)
	actor := modules.Actor{GuildID: "guild-a", DiscordUserID: "member"}
	ticket, err := adapter.Open(ctx, actor)
	if err == nil || ticket == nil || ticket.LogMessageDiscordID == "" {
		t.Fatalf("open = %+v, %v; want a queued ticket with an error", ticket, err)
	}
	client.permissionError = nil
	if _, err := adapter.Close(ctx, actor, ticket.ID); err != nil {
		t.Fatal(err)
	}
	if content := transcriptOf(t, service, actor, ticket.ID); !strings.Contains(content, "deleted during invitation") {
		t.Fatalf("transcript = %q", content)
	}
}

func TestFormatTranscriptOrdersAndKeepsAttachmentContext(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	got := tickets.FormatTranscript([]tickets.TranscriptMessage{
		{MessageID: "20", AuthorID: "2", AuthorName: "b", Body: "second", SentAt: at},
		{MessageID: "9", AuthorID: "1", AuthorName: "a", Body: "first", SentAt: at,
			Attachments: []tickets.TranscriptAttachment{{Name: "a.png", Size: 3, URL: "https://cdn/a.png"}}},
		{MessageID: "9", AuthorID: "1", AuthorName: "a", Body: "duplicate", SentAt: at},
		{MessageID: "1", Body: "no author", SentAt: at.Add(time.Second)},
	})
	want := "[2026-01-02T03:04:05Z] a (1): first\n" +
		"  attachment: a.png (3 bytes)\n" +
		"  original attachment URL (may expire): https://cdn/a.png\n" +
		"[2026-01-02T03:04:05Z] b (2): second\n" +
		"[2026-01-02T03:04:06Z] unknown: no author\n"
	if got != want {
		t.Fatalf("transcript =\n%s\nwant\n%s", got, want)
	}
}
