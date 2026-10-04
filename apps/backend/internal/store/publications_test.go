package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/store"
)

// due returns the publications due a second from now.
func due(t *testing.T, s *store.Store) []quack.CasePublication {
	t.Helper()
	publications, err := s.ListDueCasePublications(context.Background(), time.Now().Add(time.Second), 10)
	if err != nil {
		t.Fatalf("list due publications: %v", err)
	}
	return publications
}

// settle records a final refresh of p.
func settle(t *testing.T, s *store.Store, p quack.CasePublication) {
	t.Helper()
	if err := s.CompleteCasePublicationRefresh(context.Background(), quack.CompleteCasePublicationRefreshParams{
		MessageID: p.MessageID, Revision: p.Revision, Digest: "digest", RetryAt: time.Now(),
	}); err != nil {
		t.Fatalf("complete refresh: %v", err)
	}
}

func TestCasePublicationRevisionFencesStaleRefreshes(t *testing.T) {
	ctx := context.Background()
	s, guildID := newTestStore(t)
	created := createCase(t, s, guildID, timeout(0), nil)
	publication := quack.CasePublication{MessageID: "m1", ChannelID: "c1", CaseID: created.Case.ID, PresentationJSON: "first", RetryAt: time.Now()}
	if err := s.SaveCasePublication(ctx, publication); err != nil {
		t.Fatal(err)
	}
	publication.PresentationJSON = "second"
	if err := s.SaveCasePublication(ctx, publication); err != nil {
		t.Fatal(err)
	}
	read := due(t, s)
	if len(read) != 1 || read[0].PresentationJSON != "first" || read[0].Revision != 0 {
		t.Fatalf("due = %+v", read)
	}

	// Claiming the timeout changes the case while the refresh is in flight.
	claimed := claim(t, s, created.Case.ID)
	settle(t, s, read[0])
	stale := due(t, s)
	if len(stale) != 1 || stale[0].Revision != 2 || stale[0].LastDigest != "" {
		t.Fatalf("stale completion cleared a newer request: %+v", stale)
	}
	settle(t, s, stale[0])
	if got := due(t, s); len(got) != 0 {
		t.Fatalf("settled publication stayed due: %+v", got)
	}

	// Completing the action requests another refresh.
	complete(t, s, claimed, quack.ActionExecutionSucceeded)
	if got := due(t, s); len(got) != 1 {
		t.Fatalf("action completion did not request a refresh: %+v", got)
	}
	if err := s.DeleteCasePublication(ctx, "m1"); err != nil {
		t.Fatal(err)
	}
	if got := due(t, s); len(got) != 0 {
		t.Fatalf("deleted publication is due: %+v", got)
	}
}

func TestCaseUpdatesRequestRefreshAndKeepHistory(t *testing.T) {
	ctx := context.Background()
	s, guildID := newTestStore(t)
	created := createCase(t, s, guildID, nil, nil)
	caseID := created.Case.ID
	if err := s.SaveCasePublication(ctx, quack.CasePublication{MessageID: "m1", ChannelID: "c1", CaseID: caseID, RetryAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	settle(t, s, due(t, s)[0])

	audit := &quack.AuditLogEntry{ActorDiscordUserID: "moderator-1", Source: quack.AuditSourceDiscord,
		Action: string(quack.AuditActionCaseUpdate), ResourceType: "case", Result: quack.AuditResultSuccess}
	updated, err := s.UpdateCaseContext(ctx, quack.UpdateCaseContextParams{GuildID: guildID, CaseRef: "1",
		ContextValuesJSON: `[{"key":"context","value":"spam"}]`, Audit: audit})
	if err != nil || updated == nil || updated.ContextValuesJSON != `[{"key":"context","value":"spam"}]` {
		t.Fatalf("update context = %+v, %v", updated, err)
	}
	if err := notFound(s.UpdateCaseContext(ctx, quack.UpdateCaseContextParams{GuildID: "other", CaseRef: caseID, ContextValuesJSON: "[]"})); err != nil {
		t.Fatalf("other guild: %v", err)
	}
	refreshed := due(t, s)
	if len(refreshed) != 1 {
		t.Fatal("context update did not request a refresh")
	}
	settle(t, s, refreshed[0])

	evidenceID := quack.NewID()
	err = s.AppendCaseEvidence(ctx, quack.AppendCaseEvidenceParams{
		GuildID: guildID, CaseID: caseID, Audit: audit,
		Evidence: []quack.CaseEvidenceSnapshot{
			{ULIDModel: quack.ULIDModel{ID: evidenceID}, MessageCreatedAt: time.Now(), CaptureOutcome: "uploaded"},
			{MessageCreatedAt: time.Now(), CaptureOutcome: "deleted", CaptureWarning: "message was deleted"},
		},
		Attachments: []quack.CaseEvidenceAttachment{{EvidenceID: evidenceID, Filename: "proof.png", CopyOutcome: "preserved"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(due(t, s)) != 1 {
		t.Fatal("adding evidence did not request a refresh")
	}
	err = s.AppendCaseEvidence(ctx, quack.AppendCaseEvidenceParams{GuildID: guildID, CaseID: caseID,
		Attachments: []quack.CaseEvidenceAttachment{{EvidenceID: "elsewhere"}}})
	if err == nil {
		t.Fatal("attached a file to evidence outside the batch")
	}
	if err := s.AppendCaseEvidence(ctx, quack.AppendCaseEvidenceParams{GuildID: "other", CaseID: caseID}); !errors.Is(err, quack.ErrCaseNotFound) {
		t.Fatalf("other guild evidence = %v", err)
	}

	snapshot, attachments, total, err := s.GetCaseEvidencePage(ctx, caseID, 0)
	if err != nil || total != 2 || snapshot.ID != evidenceID || len(attachments) != 1 {
		t.Fatalf("first page = %+v %+v %d %v", snapshot, attachments, total, err)
	}
	snapshot, attachments, _, err = s.GetCaseEvidencePage(ctx, caseID, 7)
	if err != nil || snapshot.CaptureOutcome != "deleted" || len(attachments) != 0 {
		t.Fatalf("clamped page = %+v %+v %v", snapshot, attachments, err)
	}
	if incomplete, err := s.CaseEvidenceIncomplete(ctx, caseID); err != nil || !incomplete {
		t.Fatalf("incomplete = %v, %v", incomplete, err)
	}
	empty := createCase(t, s, guildID, nil, nil)
	if snapshot, _, total, err := s.GetCaseEvidencePage(ctx, empty.Case.ID, 1); err != nil || snapshot != nil || total != 0 {
		t.Fatalf("empty page = %+v %d %v", snapshot, total, err)
	}
	if incomplete, err := s.CaseEvidenceIncomplete(ctx, empty.Case.ID); err != nil || incomplete {
		t.Fatalf("empty incomplete = %v, %v", incomplete, err)
	}

	events, err := s.ListRecentCaseEvents(ctx, caseID, 2)
	if err != nil || len(events) != 2 || events[0].EventType != quack.CaseEventContextUpdated || events[1].EventType != quack.CaseEventEvidenceAdded {
		t.Fatalf("recent events = %+v, %v", events, err)
	}
	if _, err := s.ListRecentCaseEvents(ctx, caseID, 0); err == nil {
		t.Fatal("accepted a zero event limit")
	}
}
