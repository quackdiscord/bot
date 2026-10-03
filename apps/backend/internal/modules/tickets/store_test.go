package tickets

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/modules"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// TestOpeningReservationFencesExpiredWorkers proves a lost provisioning
// worker cannot create a ticket after another request has reclaimed the
// member's slot.
func TestOpeningReservationFencesExpiredWorkers(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	// testutil migrates through the store package, which imports this one.
	if err := db.AutoMigrate(Models()...); err != nil {
		t.Fatal(err)
	}
	s := NewStore(db)
	ctx := context.Background()
	actor := modules.Actor{GuildID: "guild", DiscordUserID: "member"}
	now := time.Now().UTC()

	first, err := s.reserveOpening(ctx, actor, 3, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.reserveOpening(ctx, actor, 3, now); !errors.Is(err, ErrDuplicateOpen) {
		t.Fatalf("concurrent reservation: got %v, want ErrDuplicateOpen", err)
	}
	later := now.Add(openingTTL + time.Second)
	second, err := s.reserveOpening(ctx, actor, 3, later)
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
