package logging_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	logmodule "github.com/quackdiscord/bot/internal/modules/logging"
)

func TestPoolDeliversEventsAndBulkDeletes(t *testing.T) {
	service, client, _ := setup(t)
	pool := logmodule.NewPool(service)
	pool.Start(context.Background())
	if !pool.Submit(logmodule.Event{GuildID: "guild-a", Type: logmodule.MemberJoin, ActorDiscordUserID: "member"}) {
		t.Fatal("join was dropped")
	}
	if !pool.Submit(logmodule.Event{GuildID: "guild-a", Type: logmodule.MessageBulkDelete, MessageIDs: []string{"one", "two"}}) {
		t.Fatal("bulk delete was dropped")
	}
	if err := pool.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if pool.Submit(logmodule.Event{GuildID: "guild-a", Type: logmodule.MemberJoin}) {
		t.Fatal("submit after stop succeeded")
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.payloads) != 2 || !strings.Contains(strings.Join(client.payloads, ""), "2 messages were deleted.") {
		t.Fatalf("payloads = %q, want a join and a two-message bulk delete", client.payloads)
	}
}

// TestPoolDoesNotReportSkipsAsErrors checks that events for a guild with
// logging off, or with no channel for the event type, are not logged as
// delivery failures.
func TestPoolDoesNotReportSkipsAsErrors(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	service, _, _ := setup(t)
	pool := logmodule.NewPool(service)
	pool.Start(context.Background())
	pool.Submit(logmodule.Event{GuildID: "guild-off", Type: logmodule.MemberJoin})
	pool.Submit(logmodule.Event{GuildID: "guild-a", Type: logmodule.MemberLeave})
	if err := pool.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if logs.Len() != 0 {
		t.Fatalf("skipped events were logged as failures:\n%s", logs.String())
	}
}
