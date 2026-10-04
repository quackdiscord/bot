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
