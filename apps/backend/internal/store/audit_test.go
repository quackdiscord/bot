package store_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/store"
)

func TestAuditIsRedactedAndAppendOnly(t *testing.T) {
	ctx := context.Background()
	s, guildID := newTestStore(t)
	entry := quack.AuditLogEntry{
		GuildID:       guildID,
		Source:        quack.AuditSourceAPI,
		Action:        string(quack.AuditActionSettingsUpdate),
		ResourceType:  "guild_settings",
		ResourceID:    "settings",
		Result:        quack.AuditResultFailure,
		FailureReason: "token=top-secret",
		MetadataJSON:  `{"authorization":"Bearer secret","safe":"value"}`,
	}
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
	if got, want := stored[0].MetadataJSON, `{"authorization":"[REDACTED]","safe":"value"}`; got != want {
		t.Errorf("metadata = %s, want %s", got, want)
	}
	if got, want := stored[0].FailureReason, "sensitive failure detail redacted"; got != want {
		t.Errorf("failure reason = %q, want %q", got, want)
	}

	update := s.DB().Table("audit_log_entries").Where("id = ?", entry.ID).Update("result", quack.AuditResultSuccess)
	if !errors.Is(update.Error, store.ErrAuditImmutable) {
		t.Errorf("update = %v, want ErrAuditImmutable", update.Error)
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
		{
			GuildID:            guildID,
			ActorDiscordUserID: "actor-1",
			Source:             quack.AuditSourceAPI,
			Action:             "case.create",
			ResourceType:       "case",
			ResourceID:         "case-1",
			Result:             quack.AuditResultSuccess,
		},
		{
			GuildID:            guildID,
			ActorDiscordUserID: "actor-2",
			Source:             quack.AuditSourceSystem,
			Action:             "case_action.failed",
			ResourceType:       "case_action_execution",
			ResourceID:         "action-1",
			Result:             quack.AuditResultFailure,
			MetadataJSON:       `{"case_id":"case-1","target_discord_user_id":"member-1"}`,
		},
		{
			GuildID:            otherID,
			ActorDiscordUserID: "actor-1",
			Source:             quack.AuditSourceAPI,
			Action:             "case.create",
			ResourceType:       "case",
			ResourceID:         "case-2",
			Result:             quack.AuditResultSuccess,
		},
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
		{
			name:   "guild newest first",
			params: quack.ListAuditLogEntriesParams{GuildID: guildID},
			want:   []string{"action-1", "case-1"},
		},
		{
			name: "exact filters",
			params: quack.ListAuditLogEntriesParams{
				GuildID:            guildID,
				ActorDiscordUserID: "actor-1",
				Action:             "case.create",
				ResourceType:       "case",
				ResourceID:         "case-1",
				Result:             quack.AuditResultSuccess,
			},
			want: []string{"case-1"},
		},
		{
			name:   "case mentioned in metadata",
			params: quack.ListAuditLogEntriesParams{GuildID: guildID, CaseID: "case-1"},
			want:   []string{"action-1", "case-1"},
		},
		{
			name:   "member mentioned in metadata",
			params: quack.ListAuditLogEntriesParams{GuildID: guildID, MemberDiscordUserID: "member-1"},
			want:   []string{"action-1"},
		},
		{
			name:   "case wildcards match literally",
			params: quack.ListAuditLogEntriesParams{GuildID: guildID, CaseID: "case_%"},
			want:   nil,
		},
		{
			name:   "member wildcards match literally",
			params: quack.ListAuditLogEntriesParams{GuildID: guildID, MemberDiscordUserID: "%"},
			want:   nil,
		},
		{
			name:   "before cursor",
			params: quack.ListAuditLogEntriesParams{GuildID: guildID, BeforeID: entries[1].ID},
			want:   []string{"case-1"},
		},
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
			if !slices.Equal(ids, test.want) {
				t.Errorf("entries = %v, want %v", ids, test.want)
			}
		})
	}
}

func TestPendingAuditMirrorEntries(t *testing.T) {
	ctx := context.Background()
	s, guildID := newTestStore(t)
	// add appends an entry about a case, or, when mirrored is set, a mirror
	// outcome for that audit entry.
	add := func(source quack.AuditSource, action, mirrored string, result quack.AuditResult) quack.AuditLogEntry {
		t.Helper()
		entry := quack.AuditLogEntry{
			GuildID:      guildID,
			Source:       source,
			Action:       action,
			ResourceType: "case",
			Result:       result,
		}
		if mirrored != "" {
			entry.ResourceType, entry.ResourceID = "audit_entry", mirrored
		}
		if err := s.CreateAuditLogEntry(ctx, &entry); err != nil {
			t.Fatal(err)
		}
		return entry
	}
	pending := func() []quack.AuditLogEntry {
		t.Helper()
		got, err := s.ListPendingAuditMirrorEntries(ctx, 10)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	important := add(quack.AuditSourceAPI, quack.ImportantAuditActions()[0], "", quack.AuditResultSuccess)
	add(quack.AuditSourceAPI, "case.read", "", quack.AuditResultSuccess)
	if got := pending(); len(got) != 1 || got[0].ID != important.ID {
		t.Fatalf("pending = %+v, want only the important entry", got)
	}

	add(quack.AuditSourceSystem, string(quack.AuditActionMirrorFailed), important.ID, quack.AuditResultFailure)
	if got := pending(); len(got) != 0 {
		t.Fatalf("pending right after a failure = %+v; want a minute's backoff", got)
	}

	add(quack.AuditSourceSystem, string(quack.AuditActionMirrorDelivered), important.ID, quack.AuditResultSuccess)
	for _, e := range pending() {
		if e.ID == important.ID {
			t.Fatal("delivered entry is still pending")
		}
	}
}

// TestPendingAuditMirrorLimitClamps checks that an oversized limit is
// clamped to the shared page maximum rather than reset to the default.
func TestPendingAuditMirrorLimitClamps(t *testing.T) {
	ctx := context.Background()
	s, guildID := newTestStore(t)
	for range 60 {
		entry := quack.AuditLogEntry{
			GuildID: guildID, Source: quack.AuditSourceAPI, Action: quack.ImportantAuditActions()[0],
			ResourceType: "case", Result: quack.AuditResultSuccess,
		}
		if err := s.CreateAuditLogEntry(ctx, &entry); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ListPendingAuditMirrorEntries(ctx, 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 60 {
		t.Fatalf("got %d entries for limit 500, want all 60", len(got))
	}
}
