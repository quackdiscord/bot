package quack

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// launchAnnouncementBatch is how many guilds one poll announces to. With the
// worker's interval it paces the rollout well under Discord's rate limits.
const launchAnnouncementBatch = 5

// LaunchAnnouncementTarget is a guild that has not been told about Quack v5
// yet, with what the sender needs to find somewhere to post.
type LaunchAnnouncementTarget struct {
	// GuildID is Quack's internal guild ID.
	GuildID            string
	DiscordGuildID     string
	OwnerDiscordUserID string
	// AuditMirrorChannelDiscordID is the guild's staff audit channel, or ""
	// when none is configured.
	AuditMirrorChannelDiscordID string
}

// LaunchAnnouncementStore finds guilds still owed the announcement and
// records that one was sent.
type LaunchAnnouncementStore interface {
	// ListLaunchAnnouncementTargets returns up to limit active guilds that
	// have not been claimed, oldest first.
	ListLaunchAnnouncementTargets(ctx context.Context, limit int) ([]LaunchAnnouncementTarget, error)
	// ClaimLaunchAnnouncement marks the guild announced at now and reports
	// whether this call did it. A guild is claimed at most once, so
	// separate processes never both send.
	ClaimLaunchAnnouncement(ctx context.Context, guildID string, now time.Time) (bool, error)
	// ReleaseLaunchAnnouncement clears a claim whose send never started, so
	// a later poll tries again.
	ReleaseLaunchAnnouncement(ctx context.Context, guildID string) error
}

// LaunchAnnouncementSender posts the announcement for one guild somewhere
// staff will see it, trying fallbacks in its own order.
type LaunchAnnouncementSender interface {
	SendLaunchAnnouncement(ctx context.Context, target LaunchAnnouncementTarget) error
}

// LaunchAnnouncer tells each guild once that Quack v5 has arrived, what
// changed, and where to read more. The worker calls PollOnce on a timer
// while the announcement is enabled, which also reaches guilds that add
// Quack during that time.
//
// Delivery is at most once: a guild is claimed before the send, and a
// failed send is logged by the caller and not retried, because a duplicate
// announcement is worse than a missing one. Only a poll stopped before
// sending releases its claim.
type LaunchAnnouncer struct {
	store  LaunchAnnouncementStore
	sender LaunchAnnouncementSender
	now    func() time.Time
	pollMu sync.Mutex
}

// NewLaunchAnnouncer returns a LaunchAnnouncer that reads store and posts
// through sender.
func NewLaunchAnnouncer(store LaunchAnnouncementStore, sender LaunchAnnouncementSender) *LaunchAnnouncer {
	return &LaunchAnnouncer{store: store, sender: sender, now: func() time.Time { return time.Now().UTC() }}
}

// PollOnce announces to the next batch of guilds. Concurrent calls run one
// at a time. It returns the guilds whose send failed, joined, so the worker
// logs them.
func (a *LaunchAnnouncer) PollOnce(ctx context.Context) error {
	a.pollMu.Lock()
	defer a.pollMu.Unlock()
	targets, err := a.store.ListLaunchAnnouncementTargets(ctx, launchAnnouncementBatch)
	if err != nil {
		return err
	}
	var failures []error
	for _, target := range targets {
		if ctx.Err() != nil {
			break
		}
		claimed, err := a.store.ClaimLaunchAnnouncement(ctx, target.GuildID, a.now())
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if !claimed {
			continue
		}
		if ctx.Err() != nil {
			if err := a.store.ReleaseLaunchAnnouncement(context.WithoutCancel(ctx), target.GuildID); err != nil {
				failures = append(failures, err)
			}
			break
		}
		if err := a.sender.SendLaunchAnnouncement(ctx, target); err != nil {
			failures = append(failures, fmt.Errorf("announce v5 to guild %s: %w", target.DiscordGuildID, err))
		}
	}
	return errors.Join(failures...)
}
