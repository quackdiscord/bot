package quack_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
)

type fakeAnnouncementStore struct {
	targets  []quack.LaunchAnnouncementTarget
	claimed  map[string]bool
	released []string
}

func (s *fakeAnnouncementStore) ListLaunchAnnouncementTargets(_ context.Context, limit int) ([]quack.LaunchAnnouncementTarget, error) {
	var out []quack.LaunchAnnouncementTarget
	for _, target := range s.targets {
		if !s.claimed[target.GuildID] && len(out) < limit {
			out = append(out, target)
		}
	}
	return out, nil
}

func (s *fakeAnnouncementStore) ClaimLaunchAnnouncement(_ context.Context, guildID string, _ time.Time) (bool, error) {
	if s.claimed[guildID] {
		return false, nil
	}
	s.claimed[guildID] = true
	return true, nil
}

func (s *fakeAnnouncementStore) ReleaseLaunchAnnouncement(_ context.Context, guildID string) error {
	delete(s.claimed, guildID)
	s.released = append(s.released, guildID)
	return nil
}

type fakeAnnouncementSender struct {
	sent   []string
	fail   map[string]bool
	cancel context.CancelFunc
}

func (s *fakeAnnouncementSender) SendLaunchAnnouncement(_ context.Context, target quack.LaunchAnnouncementTarget) error {
	s.sent = append(s.sent, target.GuildID)
	if s.cancel != nil {
		s.cancel()
	}
	if s.fail[target.GuildID] {
		return errors.New("forbidden")
	}
	return nil
}

func announcementTargets(ids ...string) []quack.LaunchAnnouncementTarget {
	targets := make([]quack.LaunchAnnouncementTarget, len(ids))
	for i, id := range ids {
		targets[i] = quack.LaunchAnnouncementTarget{GuildID: id, DiscordGuildID: "d-" + id}
	}
	return targets
}

func TestLaunchAnnouncerSendsEachGuildOnceInBatches(t *testing.T) {
	ids := []string{"g1", "g2", "g3", "g4", "g5", "g6", "g7"}
	store := &fakeAnnouncementStore{targets: announcementTargets(ids...), claimed: map[string]bool{}}
	sender := &fakeAnnouncementSender{}
	announcer := quack.NewLaunchAnnouncer(store, sender)

	if err := announcer.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 5 {
		t.Fatalf("first poll sent %d, want a batch of 5", len(sender.sent))
	}
	for range 3 {
		if err := announcer.PollOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Equal(sender.sent, ids) {
		t.Fatalf("sent %v, want each guild once: %v", sender.sent, ids)
	}
}

func TestLaunchAnnouncerDoesNotRetryAFailedSend(t *testing.T) {
	store := &fakeAnnouncementStore{targets: announcementTargets("g1", "g2"), claimed: map[string]bool{}}
	sender := &fakeAnnouncementSender{fail: map[string]bool{"g1": true}}
	announcer := quack.NewLaunchAnnouncer(store, sender)

	if err := announcer.PollOnce(context.Background()); err == nil {
		t.Fatal("PollOnce hid the failed send")
	}
	if err := announcer.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(sender.sent, []string{"g1", "g2"}) || len(store.released) != 0 {
		t.Fatalf("sent %v, released %v; want one try each and no release", sender.sent, store.released)
	}
}

func TestLaunchAnnouncerStopsBetweenGuildsWhenCanceled(t *testing.T) {
	store := &fakeAnnouncementStore{targets: announcementTargets("g1", "g2"), claimed: map[string]bool{}}
	ctx, cancel := context.WithCancel(context.Background())
	sender := &fakeAnnouncementSender{cancel: cancel}
	if err := quack.NewLaunchAnnouncer(store, sender).PollOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(sender.sent, []string{"g1"}) || store.claimed["g2"] {
		t.Fatalf("sent %v, claimed %v; want g1 only and g2 left for later", sender.sent, store.claimed)
	}
}
