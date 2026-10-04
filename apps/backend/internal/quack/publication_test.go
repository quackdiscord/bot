package quack_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
)

func TestCasePublicationsRefreshAfterCaseChanges(t *testing.T) {
	ctx := context.Background()
	store, cases, moderator, _, created := updateFixture(t)
	publications := quack.NewCasePublicationService(store)

	publication := quack.CasePublication{MessageID: "message-1", ChannelID: "channel-1", CaseID: created.ID, PresentationJSON: `{"case":1}`}
	if err := publications.Record(ctx, publication); err != nil {
		t.Fatal(err)
	}
	if err := publications.Record(ctx, quack.CasePublication{MessageID: "message-1", ChannelID: "channel-1", CaseID: created.ID, PresentationJSON: "replaced"}); err != nil {
		t.Fatal(err)
	}
	if err := publications.Record(ctx, quack.CasePublication{MessageID: "message-2"}); err == nil {
		t.Fatal("recorded a publication without its case")
	}
	if _, err := publications.Due(ctx, time.Now(), 0); err == nil {
		t.Fatal("accepted a zero limit")
	}

	due, err := publications.Due(ctx, time.Now().Add(time.Second), 10)
	if err != nil || len(due) != 1 || due[0].PresentationJSON != `{"case":1}` || !due[0].RefreshRequested {
		t.Fatalf("due = %+v err=%v", due, err)
	}
	receipt, err := publications.Receipt(ctx, due[0])
	if err != nil || receipt.CaseNumber != created.CaseNumber || receipt.Validity != quack.CaseValidityValid || !receipt.Appealable {
		t.Fatalf("receipt = %+v err=%v", receipt, err)
	}
	// The default level notifies, so the receipt waits for the DM.
	if !receipt.Pending() || receipt.Notification == nil || *receipt.Notification != quack.NotificationPending {
		t.Fatalf("pending = %v notification = %v", receipt.Pending(), receipt.Notification)
	}
	if err := publications.Complete(ctx, quack.CompleteCasePublicationRefreshParams{
		MessageID: due[0].MessageID, Revision: due[0].Revision, Digest: "digest-1", RetryAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if due, err := publications.Due(ctx, time.Now().Add(time.Second), 10); err != nil || len(due) != 0 {
		t.Fatalf("a final publication stayed due: %+v err=%v", due, err)
	}

	// Voiding the case makes the publication due again, with the digest
	// cleared so the message is rewritten.
	if _, err := cases.Void(ctx, moderator, created.ID, "mistake"); err != nil {
		t.Fatal(err)
	}
	due, err = publications.Due(ctx, time.Now().Add(time.Second), 10)
	if err != nil || len(due) != 1 || due[0].LastDigest != "" {
		t.Fatalf("due after void = %+v err=%v", due, err)
	}
	receipt, err = publications.Receipt(ctx, due[0])
	if err != nil || receipt.Validity != quack.CaseValidityVoided || receipt.Appealable {
		t.Fatalf("receipt after void = %+v err=%v", receipt, err)
	}
	if err := publications.Retire(ctx, due[0].MessageID); err != nil {
		t.Fatal(err)
	}
	if due, _ := publications.Due(ctx, time.Now().Add(time.Second), 10); len(due) != 0 {
		t.Fatalf("a retired publication stayed due: %+v", due)
	}
	if _, err := publications.Receipt(ctx, quack.CasePublication{CaseID: "missing"}); !errors.Is(err, quack.ErrCaseNotFound) {
		t.Fatalf("missing case = %v", err)
	}
}
