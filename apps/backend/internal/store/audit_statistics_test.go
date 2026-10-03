package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/quackdiscord/bot/internal/quack"
	storage "github.com/quackdiscord/bot/internal/store"
)

func TestAuditRowsAreAppendOnlyAndRedactedAtStorageBoundary(t *testing.T) {
	repository, guildID := templateTestStore(t)
	entry := quack.AuditLogEntry{GuildID: guildID, Source: quack.AuditSourceAPI, Action: string(quack.AuditActionSettingsUpdate), ResourceType: "guild_settings", ResourceID: "settings", Result: quack.AuditResultFailure, FailureReason: "token=top-secret", MetadataJSON: `{"authorization":"Bearer secret","safe":"value"}`}
	if err := repository.CreateAuditLogEntry(context.Background(), &entry); err != nil {
		t.Fatal(err)
	}
	stored, err := repository.ListAuditLogEntriesFiltered(context.Background(), quack.ListAuditLogEntriesParams{GuildID: guildID, Limit: 10})
	if err != nil || len(stored.Entries) != 1 {
		t.Fatalf("read stored audit: %+v err=%v", stored, err)
	}
	if stored.Entries[0].MetadataJSON != `{"authorization":"[REDACTED]","safe":"value"}` || stored.Entries[0].FailureReason != "sensitive failure detail redacted" {
		t.Fatalf("storage boundary did not redact audit: %+v", stored.Entries[0])
	}
	if err := repository.DB().Model(&quack.AuditLogEntry{}).Where("id = ?", entry.ID).Update("result", quack.AuditResultSuccess).Error; !errors.Is(err, storage.ErrAuditImmutable) {
		t.Fatalf("expected update rejection, got %v", err)
	}
	if err := repository.DB().Delete(&quack.AuditLogEntry{}, "id = ?", entry.ID).Error; !errors.Is(err, storage.ErrAuditImmutable) {
		t.Fatalf("expected delete rejection, got %v", err)
	}
}
