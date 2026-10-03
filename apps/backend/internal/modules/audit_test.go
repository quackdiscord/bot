package modules_test

import (
	"context"
	"testing"

	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/testutil"
)

func TestAuditLogWritesCoreEntriesWithSource(t *testing.T) {
	ctx := context.Background()
	store := testutil.NewSQLiteStore(t)
	guild, err := store.UpsertGuild(ctx, quack.UpsertGuildParams{
		DiscordGuildID: "discord-guild", Name: "Guild", OwnerDiscordUserID: "owner",
	})
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
	invalid := modules.AuditEvent{GuildID: guild.ID, Action: "ticket.open", Result: "maybe"}
	if err := audit.RecordModuleAudit(ctx, invalid); err == nil {
		t.Fatal("accepted an invalid result")
	}
	entries, err := store.ListAuditLogEntries(ctx, guild.ID)
	if err != nil || len(entries) != 3 {
		t.Fatalf("entries = %+v, %v; want 3", entries, err)
	}
	want := []quack.AuditSource{quack.AuditSourceAPI, quack.AuditSourceDiscord, quack.AuditSourceHoneypot}
	for i, entry := range entries {
		if entry.Source != want[i] {
			t.Errorf("%s: source %s, want %s", entry.Action, entry.Source, want[i])
		}
	}
}
