package discord

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

// fakePublications is one stored publication with the store's revision
// fence, and the receipt its case currently has.
type fakePublications struct {
	publication quack.CasePublication
	receipt     *quack.CaseReceipt
	receiptErr  error
	retired     bool
}

func (f *fakePublications) Due(_ context.Context, now time.Time, _ int) ([]quack.CasePublication, error) {
	if f.retired || !f.publication.RefreshRequested || now.Before(f.publication.RetryAt) {
		return nil, nil
	}
	return []quack.CasePublication{f.publication}, nil
}

func (f *fakePublications) Receipt(context.Context, quack.CasePublication) (*quack.CaseReceipt, error) {
	return f.receipt, f.receiptErr
}

func (f *fakePublications) Complete(_ context.Context, params quack.CompleteCasePublicationRefreshParams) error {
	if params.Revision != f.publication.Revision {
		return nil // A newer change keeps its refresh request.
	}
	f.publication.LastDigest = params.Digest
	f.publication.RetryAt = params.RetryAt
	f.publication.RefreshRequested = params.RefreshRequested
	return nil
}

func (f *fakePublications) Retire(context.Context, string) error {
	f.retired = true
	return nil
}

// request marks a committed change, as the store does.
func (f *fakePublications) request() {
	f.publication.Revision++
	f.publication.RefreshRequested = true
	f.publication.LastDigest = ""
	f.publication.RetryAt = time.Time{}
}

// fakeEditor records edits and fails them with err.
type fakeEditor struct {
	edits []Message
	err   error
}

func (f *fakeEditor) Edit(_ context.Context, _, _ string, message Message) (*discordgo.Message, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.edits = append(f.edits, message)
	return &discordgo.Message{}, nil
}

func newPublicationFixture() (*fakePublications, *fakeEditor, *PublicationRefresher) {
	source := &fakePublications{
		publication: quack.CasePublication{
			MessageID: "message", ChannelID: "channel", CaseID: "case",
			PresentationJSON: `{"view":"case_receipt"}`, RefreshRequested: true,
		},
		receipt: &quack.CaseReceipt{
			CaseID: "case", CaseNumber: 42, TargetDiscordUserID: "member", RuleName: "Original rule", Reason: "Staff reason",
			Actions: []quack.CaseReceiptAction{{ID: "action", ActionType: quack.ActionBanUser, Status: quack.ActionExecutionSucceeded}},
		},
	}
	editor := &fakeEditor{}
	return source, editor, &PublicationRefresher{source: source, editor: editor}
}

// TestPublicationRefreshSettlesAndWakesOnChange edits a settled receipt
// once, skips unchanged ones, and picks up a later void.
func TestPublicationRefreshSettlesAndWakesOnChange(t *testing.T) {
	source, editor, refresher := newPublicationFixture()
	ctx := context.Background()
	if err := refresher.RefreshDue(ctx); err != nil {
		t.Fatal(err)
	}
	if len(editor.edits) != 1 || source.publication.LastDigest == "" || source.publication.RefreshRequested {
		t.Fatalf("settled receipt did not finish: edits=%d %+v", len(editor.edits), source.publication)
	}
	if !strings.Contains(editor.edits[0].Content, "Original rule") || !strings.Contains(editor.edits[0].Content, "Staff reason") {
		t.Fatalf("unexpected receipt: %s", editor.edits[0].Content)
	}
	// A refresh requested without a visible change edits nothing.
	source.publication.RefreshRequested = true
	if err := refresher.RefreshDue(ctx); err != nil || len(editor.edits) != 1 {
		t.Fatalf("unchanged receipt edited again: %v", err)
	}
	source.request()
	source.receipt.Validity = quack.CaseValidityVoided
	source.receipt.Actions = append(source.receipt.Actions, quack.CaseReceiptAction{ActionType: quack.ActionUnbanUser, Status: quack.ActionExecutionSucceeded, Reversal: true})
	if err := refresher.RefreshDue(ctx); err != nil {
		t.Fatal(err)
	}
	if len(editor.edits) != 2 || !strings.Contains(strings.SplitN(editor.edits[1].Content, "\n", 2)[0], "**Voided**") {
		t.Fatalf("void not shown: %d edits", len(editor.edits))
	}
}

// TestPublicationRefreshLinksTheDashboard links a receipt that recorded
// its Discord guild to the case's dashboard page, and leaves receipts
// recorded before that without a link.
func TestPublicationRefreshLinksTheDashboard(t *testing.T) {
	for presentation, want := range map[string]string{
		`{"view":"case_receipt","discord_guild_id":"discord-guild"}`: "https://dash.example/guilds/discord-guild/cases/case",
		`{"view":"case_receipt"}`:                                    "",
	} {
		source, editor, refresher := newPublicationFixture()
		source.publication.PresentationJSON = presentation
		refresher.dashboard = quack.NewDashboardLinks("https://dash.example")
		if err := refresher.RefreshDue(context.Background()); err != nil || len(editor.edits) != 1 {
			t.Fatalf("refresh: %v, %d edits", err, len(editor.edits))
		}
		row := editor.edits[0].Components[0].(discordgo.ActionsRow).Components
		last := row[len(row)-1].(discordgo.Button)
		if got := last.URL; got != want {
			t.Fatalf("%s: link %q, want %q", presentation, got, want)
		}
	}
}

// TestPublicationRefreshFollowsPendingEnforcement keeps a receipt due while
// its enforcement runs, and shows a confirmed timeout's end once it lands.
func TestPublicationRefreshFollowsPendingEnforcement(t *testing.T) {
	source, editor, refresher := newPublicationFixture()
	source.receipt.Actions = []quack.CaseReceiptAction{{ID: "action", ActionType: quack.ActionTimeoutUser, Status: quack.ActionExecutionRunning}}
	if err := refresher.RefreshDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !source.publication.RefreshRequested || time.Until(source.publication.RetryAt) > publicationPendingDelay {
		t.Fatalf("pending receipt not rescheduled: %+v", source.publication)
	}
	until := time.Unix(1700086400, 0)
	source.receipt.Actions[0].Status = quack.ActionExecutionSucceeded
	source.receipt.Actions[0].TimeoutUntil = &until
	source.publication.RetryAt = time.Time{}
	if err := refresher.RefreshDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(editor.edits) != 2 || !strings.Contains(editor.edits[1].Content, "Timed out until <t:1700086400:f> (<t:1700086400:R>).") {
		t.Fatalf("timeout end missing: %+v", editor.edits)
	}
	if source.publication.RefreshRequested {
		t.Fatal("settled receipt still requested")
	}
}

// TestPublicationRefreshRetriesOrRetires retries outages later and retires
// only a publication whose message or case is gone.
func TestPublicationRefreshRetriesOrRetires(t *testing.T) {
	unknownMessage := &discordgo.RESTError{
		Response: &http.Response{StatusCode: http.StatusNotFound},
		Message:  &discordgo.APIErrorMessage{Code: discordgo.ErrCodeUnknownMessage},
	}
	forbidden := &discordgo.RESTError{Response: &http.Response{StatusCode: http.StatusForbidden}, Message: &discordgo.APIErrorMessage{Code: 50001}}
	for name, test := range map[string]struct {
		editErr, receiptErr error
		retired             bool
	}{
		"store outage":    {receiptErr: errors.New("database unavailable")},
		"missing access":  {editErr: forbidden},
		"network":         {editErr: errors.New("connection reset")},
		"deleted message": {editErr: unknownMessage, retired: true},
		"deleted case":    {receiptErr: quack.ErrCaseNotFound, retired: true},
	} {
		t.Run(name, func(t *testing.T) {
			source, editor, refresher := newPublicationFixture()
			source.receiptErr, editor.err = test.receiptErr, test.editErr
			err := refresher.RefreshDue(context.Background())
			if source.retired != test.retired {
				t.Fatalf("retired = %v, want %v", source.retired, test.retired)
			}
			if test.retired {
				if err != nil {
					t.Fatalf("retiring is not a failure: %v", err)
				}
				return
			}
			if err == nil || !source.publication.RefreshRequested || time.Until(source.publication.RetryAt) < 20*time.Second {
				t.Fatalf("failure not retried later: err=%v %+v", err, source.publication)
			}
		})
	}
}

// TestPublicationRefreshKeepsChangesMadeDuringRefresh relies on the
// revision fence: a change committed mid-refresh stays requested.
func TestPublicationRefreshKeepsChangesMadeDuringRefresh(t *testing.T) {
	source, _, _ := newPublicationFixture()
	editor := &racingEditor{source: source}
	refresher := &PublicationRefresher{source: source, editor: editor}
	if err := refresher.RefreshDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !source.publication.RefreshRequested || source.publication.LastDigest != "" {
		t.Fatalf("change during refresh was lost: %+v", source.publication)
	}
}

// racingEditor commits a change to the case while its edit is in flight.
type racingEditor struct{ source *fakePublications }

func (r *racingEditor) Edit(context.Context, string, string, Message) (*discordgo.Message, error) {
	r.source.request()
	return &discordgo.Message{}, nil
}
