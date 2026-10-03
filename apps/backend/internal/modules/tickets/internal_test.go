package tickets

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/modules"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// testDB returns an in-memory database with the ticket and module tables.
// testutil migrates through the store package, which imports this one.
func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(append(modules.Models(), Models()...)...); err != nil {
		t.Fatal(err)
	}
	return db
}

// TestKeyedLocksCancelAndStayIndependent checks a waiter can give up
// without blocking other keys or leaking entries.
func TestKeyedLocksCancelAndStayIndependent(t *testing.T) {
	var locks keyedLocks
	release, err := locks.acquire(context.Background(), "first")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := locks.acquire(ctx, "first"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait did not cancel: %v", err)
	}
	other, err := locks.acquire(context.Background(), "other")
	if err != nil {
		t.Fatal(err)
	}
	other()
	release()
	if len(locks.entries) != 0 {
		t.Fatal("idle lock kept")
	}
	again, err := locks.acquire(context.Background(), "first")
	if err != nil {
		t.Fatal(err)
	}
	again()
}

// TestOpeningReservationFencesExpiredWorkers proves a lost provisioning
// worker cannot create a ticket after another request has reclaimed the
// member's slot.
func TestOpeningReservationFencesExpiredWorkers(t *testing.T) {
	s := NewStore(testDB(t))
	ctx := context.Background()
	actor := modules.Actor{GuildID: "guild", DiscordUserID: "member"}
	now := time.Now().UTC()

	first, err := s.reserveOpening(ctx, actor, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.reserveOpening(ctx, actor, now); !errors.Is(err, ErrDuplicateOpen) {
		t.Fatalf("concurrent reservation: got %v, want ErrDuplicateOpen", err)
	}
	later := now.Add(openingTTL + time.Second)
	second, err := s.reserveOpening(ctx, actor, later)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.finishOpening(ctx, actor, first, "old-channel", later); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("stale finish: got %v, want ErrInvalidTransition", err)
	}
	if err := s.releaseOpening(ctx, actor, first); err != nil {
		t.Fatal(err)
	}
	if _, err := s.finishOpening(ctx, actor, second, "new-channel", later); err != nil {
		t.Fatalf("stale release freed the current reservation: %v", err)
	}
}

// journalService returns a service whose journal knows two open tickets,
// in threads "one" and "two".
func journalService(t *testing.T) *Service {
	t.Helper()
	db := testDB(t)
	s := NewService(modules.NewRegistry(db), NewStore(db), nil)
	for _, thread := range []string{"one", "two"} {
		actor := modules.Actor{GuildID: "guild", DiscordUserID: thread}
		token, err := s.store.reserveOpening(context.Background(), actor, s.now())
		if err != nil {
			t.Fatal(err)
		}
		ticket, err := s.store.finishOpening(context.Background(), actor, token, thread, s.now())
		if err != nil {
			t.Fatal(err)
		}
		s.rememberJournalThread(ticket)
	}
	return s
}

// TestJournalBuffersBeforeWaitingForTheGate checks a write waiting on a
// closing thread is already visible to the close's flush, and other
// threads do not wait.
func TestJournalBuffersBeforeWaitingForTheGate(t *testing.T) {
	s := journalService(t)
	release, err := s.journal.locks.acquire(context.Background(), "one")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- s.RecordMessage(context.Background(), "guild", "one", TranscriptMessage{MessageID: "1", AuthorID: "member", Body: "queued original"})
	}()
	for deadline := time.Now().Add(time.Second); ; time.Sleep(time.Millisecond) {
		s.journal.mu.Lock()
		n := len(s.journal.pending)
		s.journal.mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			release()
			t.Fatal("write did not buffer before waiting")
		}
	}
	if err := s.RecordMessage(context.Background(), "guild", "two", TranscriptMessage{MessageID: "2", AuthorID: "member", Body: "independent"}); err != nil {
		release()
		t.Fatal(err)
	}
	var ticket ticketRecord
	if err := s.store.db.Where("thread_discord_channel_id = ?", "one").First(&ticket).Error; err != nil {
		release()
		t.Fatal(err)
	}
	// A close holding the gate flushes the waiting write before it runs.
	rows, err := s.flushJournal(context.Background(), &Ticket{ID: ticket.ID, GuildID: "guild", ThreadDiscordChannelID: "one"})
	release()
	if err != nil || len(rows) != 1 || rows[0].Body != "queued original" {
		t.Fatalf("flushed %+v, %v", rows, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(s.journal.threads) != 0 || len(s.journal.locks.entries) != 0 {
		t.Fatal("idle journal gates leaked")
	}
}

// TestJournalIgnoresUnknownThreadsDuringOutage keeps unrelated message
// content out of the retry buffer even when the database is down.
func TestJournalIgnoresUnknownThreadsDuringOutage(t *testing.T) {
	s := journalService(t)
	sqlDB, err := s.store.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	_ = sqlDB.Close()
	for range journalBufferLimit + 100 {
		message := TranscriptMessage{MessageID: "message", AuthorID: "member", Body: "private unrelated text"}
		if err := s.RecordMessage(context.Background(), "guild", "unrelated", message); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.journal.pending) != 0 || len(s.journal.blocked) != 0 || s.journal.allBlocked {
		t.Fatal("unrelated traffic entered the retry buffer")
	}
}

// TestJournalLoadFailureBlocksClosing refuses every close when the open
// threads could not be loaded at startup.
func TestJournalLoadFailureBlocksClosing(t *testing.T) {
	db := testDB(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	_ = sqlDB.Close()
	s := NewService(modules.NewRegistry(db), NewStore(db), nil)
	if _, err := s.flushJournal(context.Background(), &Ticket{ID: "t", GuildID: "g", ThreadDiscordChannelID: "c"}); !errors.Is(err, ErrJournalIncomplete) {
		t.Fatalf("flush = %v, want ErrJournalIncomplete", err)
	}
}
