package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newMirrorTestStore returns a migrated in-memory SQLite store. It cannot
// use testutil, which imports this package.
func newMirrorTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	s := New(db, nil)
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	return s
}

// addMirrorGuild creates guild settings for guildID, with channel as its
// audit mirror channel.
func addMirrorGuild(t *testing.T, s *Store, guildID, channel string) {
	t.Helper()
	now := time.Now().UTC()
	record := guildSettingsRecord{ID: quack.NewID(), CreatedAt: now, UpdatedAt: now, GuildID: guildID, AuditMirrorChannelDiscordID: channel}
	if err := s.db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
}

func importantEntry(guildID string) *quack.AuditLogEntry {
	return &quack.AuditLogEntry{
		GuildID: guildID, Source: quack.AuditSourceAPI, Action: string(quack.AuditActionCaseCreate),
		ResourceType: "case", ResourceID: "case-1", Result: quack.AuditResultSuccess,
	}
}

func deliveryRow(t *testing.T, s *Store, entryID string) (auditMirrorDeliveryRecord, bool) {
	t.Helper()
	var record auditMirrorDeliveryRecord
	found, err := first(s.db.Where("audit_entry_id = ?", entryID), &record)
	if err != nil {
		t.Fatal(err)
	}
	return record, found
}

func TestImportantEntryQueuesDeliveryInItsTransaction(t *testing.T) {
	ctx := context.Background()
	s := newMirrorTestStore(t)
	addMirrorGuild(t, s, "mirrored", "123")
	addMirrorGuild(t, s, "unmirrored", "")

	entry := importantEntry("mirrored")
	if err := s.CreateAuditLogEntry(ctx, entry); err != nil {
		t.Fatal(err)
	}
	if row, found := deliveryRow(t, s, entry.ID); !found || row.Status != quack.AuditMirrorPending || row.GuildID != "mirrored" {
		t.Fatalf("delivery = %+v, %v; want a pending row", row, found)
	}
	for _, entry := range []*quack.AuditLogEntry{
		importantEntry("unmirrored"),
		{GuildID: "mirrored", Source: quack.AuditSourceAPI, Action: "case.read", ResourceType: "case", Result: quack.AuditResultDenied},
	} {
		if err := s.CreateAuditLogEntry(ctx, entry); err != nil {
			t.Fatal(err)
		}
		if _, found := deliveryRow(t, s, entry.ID); found {
			t.Errorf("%s in %s queued a delivery", entry.Action, entry.GuildID)
		}
	}

	// The delivery commits and rolls back with the entry's transaction.
	rolledBack := importantEntry("mirrored")
	errAbort := errors.New("abort")
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := createAuditLogEntry(tx, rolledBack, time.Now().UTC()); err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&auditMirrorDeliveryRecord{}).Where("audit_entry_id = ?", rolledBack.ID).Count(&count).Error; err != nil || count != 1 {
			t.Errorf("delivery inside the transaction: count=%d err=%v", count, err)
		}
		return errAbort
	})
	if !errors.Is(err, errAbort) {
		t.Fatal(err)
	}
	if _, found := deliveryRow(t, s, rolledBack.ID); found {
		t.Error("delivery outlived its rolled-back entry")
	}
}

func TestClaimAuditMirrorDeliveries(t *testing.T) {
	ctx := context.Background()
	s := newMirrorTestStore(t)
	addMirrorGuild(t, s, "busy", "1")
	addMirrorGuild(t, s, "quiet", "2")
	var busy []string
	for range 3 {
		entry := importantEntry("busy")
		if err := s.CreateAuditLogEntry(ctx, entry); err != nil {
			t.Fatal(err)
		}
		busy = append(busy, entry.ID)
	}
	quiet := importantEntry("quiet")
	if err := s.CreateAuditLogEntry(ctx, quiet); err != nil {
		t.Fatal(err)
	}

	// Each guild gets a slot before the busy one gets a second.
	got, err := s.ClaimAuditMirrorDeliveries(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Entry.ID != busy[0] || got[1].Entry.ID != quiet.ID || got[0].LeaseToken == "" {
		t.Fatalf("claimed %+v, want the busy guild's oldest and the quiet guild's entry", got)
	}
	again, err := s.ClaimAuditMirrorDeliveries(ctx, 10)
	if err != nil || len(again) != 2 || again[0].Entry.ID != busy[1] || again[1].Entry.ID != busy[2] {
		t.Fatalf("second claim = %+v, %v; want only the unclaimed rows", again, err)
	}

	// A lease is needed to begin or settle.
	if err := s.BeginAuditMirrorDelivery(ctx, busy[0], "wrong"); !errors.Is(err, quack.ErrAuditMirrorLeaseLost) {
		t.Fatalf("begin with a foreign lease = %v", err)
	}
	if err := s.CompleteAuditMirrorDelivery(ctx, quack.CompleteAuditMirrorDeliveryParams{
		AuditEntryID: busy[0], LeaseToken: "wrong", Status: quack.AuditMirrorDelivered,
	}); !errors.Is(err, quack.ErrAuditMirrorLeaseLost) {
		t.Fatalf("complete with a foreign lease = %v", err)
	}

	// A retry waits for its next attempt time.
	retryAt := time.Now().UTC().Add(time.Minute)
	if err := s.CompleteAuditMirrorDelivery(ctx, quack.CompleteAuditMirrorDeliveryParams{
		AuditEntryID: quiet.ID, LeaseToken: got[1].LeaseToken, Status: quack.AuditMirrorPending,
		Attempts: 1, NextAttemptAt: retryAt, LastError: "delivery_failed",
	}); err != nil {
		t.Fatal(err)
	}
	if row, _ := deliveryRow(t, s, quiet.ID); row.Status != quack.AuditMirrorPending || row.Attempts != 1 ||
		!row.NextAttemptAt.Equal(retryAt) || row.LeaseToken != "" || row.LastError != "delivery_failed" {
		t.Fatalf("retried row = %+v", row)
	}

	// A lapsed claim is due again; a lapsed send is never retried.
	if err := s.BeginAuditMirrorDelivery(ctx, busy[0], got[0].LeaseToken); err != nil {
		t.Fatal(err)
	}
	past := time.Now().UTC().Add(-time.Second)
	if err := s.db.Model(&auditMirrorDeliveryRecord{}).Where("audit_entry_id IN ?", []string{busy[0], busy[1]}).
		Update("lease_expires_at", past).Error; err != nil {
		t.Fatal(err)
	}
	reclaimed, err := s.ClaimAuditMirrorDeliveries(ctx, 10)
	if err != nil || len(reclaimed) != 1 || reclaimed[0].Entry.ID != busy[1] {
		t.Fatalf("claim after lapse = %+v, %v; want only the unsent claim", reclaimed, err)
	}
	if row, _ := deliveryRow(t, s, busy[0]); row.Status != quack.AuditMirrorFailed || row.LastError != auditMirrorOutcomeUnknown {
		t.Fatalf("lapsed send = %+v, want failed with an unknown outcome", row)
	}
	if err := s.CompleteAuditMirrorDelivery(ctx, quack.CompleteAuditMirrorDeliveryParams{
		AuditEntryID: busy[1], LeaseToken: reclaimed[0].LeaseToken, Status: quack.AuditMirrorDelivered, DeliveredMessageID: "message-1",
	}); err != nil {
		t.Fatal(err)
	}
	if row, _ := deliveryRow(t, s, busy[1]); row.Status != quack.AuditMirrorDelivered || row.DeliveredMessageID != "message-1" {
		t.Fatalf("delivered row = %+v", row)
	}
}

// TestGiveUpAuditsOncePerOutage checks that only the first delivery a guild
// gives up on after a successful one is audited.
func TestGiveUpAuditsOncePerOutage(t *testing.T) {
	ctx := context.Background()
	s := newMirrorTestStore(t)
	addMirrorGuild(t, s, "guild", "1")
	settle := func(status quack.AuditMirrorDeliveryStatus) {
		t.Helper()
		if err := s.CreateAuditLogEntry(ctx, importantEntry("guild")); err != nil {
			t.Fatal(err)
		}
		claimed, err := s.ClaimAuditMirrorDeliveries(ctx, 1)
		if err != nil || len(claimed) != 1 {
			t.Fatalf("claim = %+v, %v", claimed, err)
		}
		params := quack.CompleteAuditMirrorDeliveryParams{
			AuditEntryID: claimed[0].Entry.ID, LeaseToken: claimed[0].LeaseToken, Status: status,
		}
		if status == quack.AuditMirrorFailed {
			params.LastError = "delivery_failed"
			params.GiveUpAudit = &quack.AuditLogEntry{
				Source: quack.AuditSourceSystem, Action: string(quack.AuditActionMirrorFailed),
				ResourceType: "audit_entry", ResourceID: claimed[0].Entry.ID, Result: quack.AuditResultFailure,
			}
		}
		if err := s.CompleteAuditMirrorDelivery(ctx, params); err != nil {
			t.Fatal(err)
		}
		// Keep updated_at strictly ordered on coarse clocks.
		time.Sleep(2 * time.Millisecond)
	}
	gaveUp := func() int64 {
		t.Helper()
		var count int64
		if err := s.db.Model(&auditRecord{}).Where("action = ?", quack.AuditActionMirrorFailed).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		return count
	}
	settle(quack.AuditMirrorFailed)
	settle(quack.AuditMirrorFailed)
	if n := gaveUp(); n != 1 {
		t.Fatalf("%d give-up entries for one outage, want 1", n)
	}
	settle(quack.AuditMirrorDelivered)
	settle(quack.AuditMirrorFailed)
	if n := gaveUp(); n != 2 {
		t.Fatalf("%d give-up entries after a second outage, want 2", n)
	}
}
