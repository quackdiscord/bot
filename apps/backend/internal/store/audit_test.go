package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/store"
)

func TestAuditIsRedactedAndAppendOnly(t *testing.T) {
	ctx := context.Background()
	s, guildID := newTestStore(t)
	entry := quack.AuditLogEntry{GuildID: guildID, Source: quack.AuditSourceAPI, Action: string(quack.AuditActionSettingsUpdate),
		ResourceType: "guild_settings", ResourceID: "settings", Result: quack.AuditResultFailure,
		FailureReason: "token=top-secret", MetadataJSON: `{"authorization":"Bearer secret","safe":"value"}`}
	if err := s.CreateAuditLogEntry(ctx, &entry); err != nil {
		t.Fatal(err)
	}
	if entry.ID == "" {
		t.Fatal("CreateAuditLogEntry did not fill in the ID")
	}
	stored, err := s.ListAuditLogEntries(ctx, guildID)
	if err != nil || len(stored) != 1 {
		t.Fatalf("stored = %+v, %v", stored, err)
	}
	if stored[0].MetadataJSON != `{"authorization":"[REDACTED]","safe":"value"}` || stored[0].FailureReason != "sensitive failure detail redacted" {
		t.Fatalf("stored entry was not redacted: %+v", stored[0])
	}
	if err := s.DB().Table("audit_log_entries").Where("id = ?", entry.ID).Update("result", quack.AuditResultSuccess).Error; !errors.Is(err, store.ErrAuditImmutable) {
		t.Errorf("update = %v, want ErrAuditImmutable", err)
	}
	if err := s.DB().Delete(&quack.AuditLogEntry{}, "id = ?", entry.ID).Error; !errors.Is(err, store.ErrAuditImmutable) {
		t.Errorf("delete = %v, want ErrAuditImmutable", err)
	}
}

func TestListAuditFiltersAndCursor(t *testing.T) {
	ctx := context.Background()
	s, guildID := newTestStore(t)
	otherID := addGuild(t, s, "guild-2")
	entries := []quack.AuditLogEntry{
		{GuildID: guildID, ActorDiscordUserID: "actor-1", Source: quack.AuditSourceAPI, Action: "case.create", ResourceType: "case", ResourceID: "case-1", Result: quack.AuditResultSuccess},
		{GuildID: guildID, ActorDiscordUserID: "actor-2", Source: quack.AuditSourceSystem, Action: "case_action.failed", ResourceType: "case_action_execution",
			ResourceID: "action-1", Result: quack.AuditResultFailure, MetadataJSON: `{"case_id":"case-1","target_discord_user_id":"member-1"}`},
		{GuildID: otherID, ActorDiscordUserID: "actor-1", Source: quack.AuditSourceAPI, Action: "case.create", ResourceType: "case", ResourceID: "case-2", Result: quack.AuditResultSuccess},
	}
	for i := range entries {
		if err := s.CreateAuditLogEntry(ctx, &entries[i]); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	tests := []struct {
		name   string
		params quack.ListAuditLogEntriesParams
		want   []string
	}{
		{"guild newest first", quack.ListAuditLogEntriesParams{GuildID: guildID}, []string{"action-1", "case-1"}},
		{"exact filters", quack.ListAuditLogEntriesParams{GuildID: guildID, ActorDiscordUserID: "actor-1", Action: "case.create",
			ResourceType: "case", ResourceID: "case-1", Result: quack.AuditResultSuccess}, []string{"case-1"}},
		{"case mentioned in metadata", quack.ListAuditLogEntriesParams{GuildID: guildID, CaseID: "case-1"}, []string{"action-1", "case-1"}},
		{"member mentioned in metadata", quack.ListAuditLogEntriesParams{GuildID: guildID, MemberDiscordUserID: "member-1"}, []string{"action-1"}},
		{"before cursor", quack.ListAuditLogEntriesParams{GuildID: guildID, BeforeID: entries[1].ID}, []string{"case-1"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := s.ListAuditLogEntriesFiltered(ctx, test.params)
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for _, entry := range got.Entries {
				ids = append(ids, entry.ResourceID)
			}
			if len(ids) != len(test.want) || (len(ids) > 0 && ids[0] != test.want[0]) {
				t.Fatalf("entries = %v, want %v", ids, test.want)
			}
		})
	}
}

func TestPendingAuditMirrorEntries(t *testing.T) {
	ctx := context.Background()
	s, guildID := newTestStore(t)
	important := quack.ImportantAuditActions()[0]
	entry := quack.AuditLogEntry{GuildID: guildID, Source: quack.AuditSourceAPI, Action: important, ResourceType: "case", Result: quack.AuditResultSuccess}
	routine := quack.AuditLogEntry{GuildID: guildID, Source: quack.AuditSourceAPI, Action: "case.read", ResourceType: "case", Result: quack.AuditResultSuccess}
	for _, e := range []*quack.AuditLogEntry{&entry, &routine} {
		if err := s.CreateAuditLogEntry(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	pending := func() []quack.AuditLogEntry {
		t.Helper()
		got, err := s.ListPendingAuditMirrorEntries(ctx, 10)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := pending(); len(got) != 1 || got[0].ID != entry.ID {
		t.Fatalf("pending = %+v, want only the important entry", got)
	}
	failed := quack.AuditLogEntry{GuildID: guildID, Source: quack.AuditSourceSystem, Action: string(quack.AuditActionMirrorFailed),
		ResourceType: "audit_entry", ResourceID: entry.ID, Result: quack.AuditResultFailure}
	if err := s.CreateAuditLogEntry(ctx, &failed); err != nil {
		t.Fatal(err)
	}
	if got := pending(); len(got) != 0 {
		t.Fatalf("pending right after a failure = %+v; want a minute's backoff", got)
	}
	delivered := quack.AuditLogEntry{GuildID: guildID, Source: quack.AuditSourceSystem, Action: string(quack.AuditActionMirrorDelivered),
		ResourceType: "audit_entry", ResourceID: entry.ID, Result: quack.AuditResultSuccess}
	if err := s.CreateAuditLogEntry(ctx, &delivered); err != nil {
		t.Fatal(err)
	}
	for _, e := range pending() {
		if e.ID == entry.ID {
			t.Fatal("delivered entry is still pending")
		}
	}
}
