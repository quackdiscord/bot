package modules_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/testutil"
)

func TestAuditLogWritesCoreEntriesWithSource(t *testing.T) {
	ctx := context.Background()
	store := testutil.NewSQLiteStore(t)
	guild, err := store.UpsertGuild(ctx, quack.UpsertGuildParams{DiscordGuildID: "discord-guild", Name: "Guild", OwnerDiscordUserID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	audit := modules.NewAuditLog(store)
	for _, record := range []struct {
		ctx    context.Context
		action string
	}{
		{ctx, "ticket.open"},
		{quack.ContextWithAuditSource(ctx, quack.AuditSourceDiscord), "ticket.close"},
		{ctx, "honeypot.trigger.accepted"},
	} {
		if err := audit.RecordModuleAudit(record.ctx, modules.AuditEvent{
			GuildID: guild.ID, ActorDiscordUserID: "actor", Action: record.action,
			ResourceType: "ticket", Result: "success", MetadataJSON: "{}",
		}); err != nil {
			t.Fatalf("record %s: %v", record.action, err)
		}
	}
	if err := audit.RecordModuleAudit(ctx, modules.AuditEvent{GuildID: guild.ID, Action: "ticket.open", Result: "maybe"}); err == nil {
		t.Fatal("accepted an invalid result")
	}
	entries, err := store.ListAuditLogEntries(ctx, guild.ID)
	if err != nil || len(entries) != 3 {
		t.Fatalf("entries = %+v, %v", entries, err)
	}
	want := []quack.AuditSource{quack.AuditSourceAPI, quack.AuditSourceDiscord, quack.AuditSourceHoneypot}
	for i, entry := range entries {
		if entry.Source != want[i] {
			t.Errorf("%s: source %s, want %s", entry.Action, entry.Source, want[i])
		}
	}
}

func TestGuildsResolveOnlyActiveGuilds(t *testing.T) {
	ctx := context.Background()
	store := testutil.NewSQLiteStore(t)
	guild, err := store.UpsertGuild(ctx, quack.UpsertGuildParams{DiscordGuildID: "discord-guild", Name: "Guild", OwnerDiscordUserID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	guilds := modules.NewGuilds(store)
	if id, err := guilds.InternalID(ctx, "discord-guild"); err != nil || id != guild.ID {
		t.Fatalf("InternalID = %q, %v", id, err)
	}
	if id, err := guilds.DiscordID(ctx, guild.ID); err != nil || id != "discord-guild" {
		t.Fatalf("DiscordID = %q, %v", id, err)
	}
	if _, err := store.DeactivateGuild(ctx, "discord-guild", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := guilds.InternalID(ctx, "discord-guild"); err == nil {
		t.Fatal("resolved a departed guild")
	}
	if id, err := guilds.InternalIDAny(ctx, "discord-guild"); err != nil || id != guild.ID {
		t.Fatalf("InternalIDAny = %q, %v", id, err)
	}
	if _, err := guilds.InternalIDAny(ctx, "unknown"); err == nil {
		t.Fatal("resolved an unknown guild")
	}
}

func TestActorForMapsLivePermissions(t *testing.T) {
	staff := &quack.GuildStaffContext{
		Guild:              &quack.Guild{ULIDModel: quack.ULIDModel{ID: "guild"}},
		ActorDiscordUserID: "actor",
		Permissions:        map[quack.PermissionAction]bool{quack.PermissionActionGuildSettingsWrite: true},
	}
	if actor := modules.ActorFor(staff); !actor.CanManage || actor.CanModerate || actor.GuildID != "guild" || actor.DiscordUserID != "actor" {
		t.Fatalf("manager actor = %+v", actor)
	}
	staff.Permissions[quack.PermissionActionTicketResolve] = true
	if actor := modules.ActorFor(staff); !actor.CanModerate {
		t.Fatalf("moderator actor = %+v", actor)
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	if _, err := modules.RequestActor(request); err == nil {
		t.Fatal("resolved an actor without a guild context")
	}
	request = request.WithContext(quack.ContextWithStaff(request.Context(), staff))
	if actor, err := modules.RequestActor(request); err != nil || actor.GuildID != "guild" {
		t.Fatalf("RequestActor = %+v, %v", actor, err)
	}
}

func TestPoolSubmitIsSafeDuringStop(t *testing.T) {
	var handled atomic.Int64
	pool := modules.NewPool("test", 128, 2, func(context.Context, string) { handled.Add(1) })
	pool.Start(context.Background())
	var submitters sync.WaitGroup
	var accepted atomic.Int64
	for range 64 {
		submitters.Add(1)
		go func() {
			defer submitters.Done()
			if pool.Submit("job") {
				accepted.Add(1)
			}
		}()
	}
	if err := pool.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	submitters.Wait()
	if err := pool.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if handled.Load() != accepted.Load() {
		t.Fatalf("handled %d of %d accepted jobs", handled.Load(), accepted.Load())
	}
}

func TestPoolStopIsBoundedByContext(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	started := make(chan struct{})
	pool := modules.NewPool("test", 1, 1, func(ctx context.Context, _ int) {
		close(started)
		select {
		case <-ctx.Done():
		case <-release:
		}
	})
	pool.Start(context.Background())
	pool.Submit(1)
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := pool.Stop(ctx); err == nil {
		t.Fatal("Stop waited past its deadline")
	}
}
