package logging_test

import (
	"context"
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
	if len(client.payloads) != 2 || !strings.Contains(strings.Join(client.payloads, ""), `"message_count":"2"`) {
		t.Fatalf("payloads = %q, want a join and a two-message bulk delete", client.payloads)
	}
}
