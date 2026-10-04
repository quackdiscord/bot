package honeypot_test

import (
	"context"
	"errors"
	"testing"

	"github.com/quackdiscord/bot/internal/modules/honeypot"
)

func TestPendingClaimCannotBeCompletedTwice(t *testing.T) {
	fixture := setup(t)
	store := honeypot.NewStore(fixture.db)
	ctx := context.Background()
	trigger, claimed, err := store.Claim(ctx, message("manual"), "template", honeypot.OutcomePending)
	if err != nil || !claimed {
		t.Fatalf("Claim = %v, %v; want a new claim", claimed, err)
	}
	if err := store.Complete(ctx, trigger.ID, honeypot.OutcomeCreated, "case", ""); err != nil {
		t.Fatal(err)
	}
	err = store.Complete(ctx, trigger.ID, honeypot.OutcomeFailed, "", "late")
	if !errors.Is(err, honeypot.ErrDuplicate) {
		t.Fatalf("second Complete: got %v, want ErrDuplicate", err)
	}
	stats, err := store.Statistics(ctx, "guild-a")
	if err != nil || stats.Created != 1 || stats.Failed != 0 {
		t.Fatalf("Statistics = %+v, %v; want 1 created and 0 failed", stats, err)
	}
}
