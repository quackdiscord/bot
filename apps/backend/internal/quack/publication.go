package quack

import (
	"context"
	"errors"
	"strings"
	"time"
)

// recordPublicationAttempts is how many times Record tries to save a
// publication before giving up.
const recordPublicationAttempts = 3

// CasePublication is a public Discord message about a case, such as the
// receipt /case add posts, that Quack keeps up to date as the case changes.
// It is edited with bot credentials, so refreshing outlives the interaction
// token that posted it.
//
// Every committed change to the case (enforcement progress, a void, a
// context or evidence update) sets RefreshRequested and bumps Revision in
// the same transaction, so a refresh is never lost to a crash.
type CasePublication struct {
	// MessageID identifies the publication.
	MessageID, ChannelID, CaseID string
	// PresentationJSON is the adapter's snapshot of what it first displayed.
	// It must hold only public fields: never evidence, staff notes, or
	// action configuration.
	PresentationJSON string
	// LastDigest fingerprints the last content written, so an unchanged
	// refresh skips the Discord edit.
	LastDigest string
	// RetryAt is when the publication is next due.
	RetryAt          time.Time
	RefreshRequested bool
	// Revision fences CompleteCasePublicationRefresh against changes
	// committed while a refresh was in flight.
	Revision uint64
}

// CompleteCasePublicationRefreshParams records the outcome of refreshing a
// publication read at Revision. If the publication changed since, the store
// keeps the newer refresh request instead.
type CompleteCasePublicationRefreshParams struct {
	MessageID string
	Revision  uint64
	// Digest is the fingerprint of what is now displayed.
	Digest string
	// RetryAt and RefreshRequested schedule the next refresh: requested
	// with a delay while enforcement is pending or after a failure, and not
	// requested once the receipt is final.
	RetryAt          time.Time
	RefreshRequested bool
}

// caseReceiptStore is what loading a CaseReceipt reads.
type caseReceiptStore interface {
	GetCaseByID(ctx context.Context, caseID string) (*Case, error)
	GetCaseNotification(ctx context.Context, caseID string) (*CaseNotification, error)
	ListCaseActionAttempts(ctx context.Context, executionIDs []string) ([]CaseActionAttempt, error)
	ListCaseActionExecutions(ctx context.Context, caseID string) ([]CaseActionExecution, error)
	// CaseEvidenceIncomplete reports whether any of the case's evidence has
	// a warning or could not be captured, without reading the evidence.
	CaseEvidenceIncomplete(ctx context.Context, caseID string) (bool, error)
}

// CasePublicationStore is what CasePublicationService needs from storage.
type CasePublicationStore interface {
	caseReceiptStore
	CompleteCasePublicationRefresh(context.Context, CompleteCasePublicationRefreshParams) error
	DeleteCasePublication(ctx context.Context, messageID string) error
	// ListDueCasePublications returns publications with a refresh requested
	// and RetryAt at or before now, oldest first.
	ListDueCasePublications(ctx context.Context, now time.Time, limit int) ([]CasePublication, error)
	// SaveCasePublication records a publication with a refresh requested.
	// Saving one that exists is a no-op that keeps the original.
	SaveCasePublication(context.Context, CasePublication) error
}

// CaseReceipt is the committed state of a case as its Discord receipts show
// it: the decision, enforcement progress, and whether the member was told.
// It carries no evidence content, action configuration, or delivery error
// text; public renderers pick which fields to show.
type CaseReceipt struct {
	CaseID                 string
	GuildID                string
	CaseNumber             uint64
	CreatedAt              time.Time
	TargetDiscordUserID    string
	ModeratorDiscordUserID string
	// RuleName and Reason are the template name and official reason frozen
	// in the case snapshot.
	RuleName, Reason string
	Validity         CaseValidity
	// Appealable is true while the case is valid and its template allowed
	// appeals.
	Appealable         bool
	ContextValues      []CaseContextValueResponse
	SelectedLevel      *CaseSelectedLevel
	EvidenceIncomplete bool
	Actions            []CaseReceiptAction
	// Notification is the member DM's status, or nil when the level does
	// not notify.
	Notification *NotificationStatus
}

// CaseReceiptAction is one execution as a receipt shows it.
type CaseReceiptAction struct {
	ID         string
	ActionType ActionType
	Status     ActionExecutionStatus
	// Reversal is set for executions that undo another one.
	Reversal bool
	// TimeoutUntil is when a succeeded timeout ends, as Discord confirmed.
	TimeoutUntil *time.Time
}

// Pending reports whether the receipt will still change on its own: some
// enforcement is queued or running, or the member DM is not settled.
func (r *CaseReceipt) Pending() bool {
	for _, action := range r.Actions {
		switch action.Status {
		case ActionExecutionPending, ActionExecutionRunning, ActionExecutionRetrying:
			return true
		}
	}
	return r.Notification != nil && *r.Notification != NotificationSent && *r.Notification != NotificationFailed
}

// loadCaseReceipt reads a case's receipt, or returns ErrCaseNotFound.
func loadCaseReceipt(ctx context.Context, store caseReceiptStore, caseID string) (*CaseReceipt, error) {
	item, err := store.GetCaseByID(ctx, caseID)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, ErrCaseNotFound
	}
	actions, err := store.ListCaseActionExecutions(ctx, item.ID)
	if err != nil {
		return nil, err
	}
	var timeouts []string
	for _, action := range actions {
		if action.endsAt() {
			timeouts = append(timeouts, action.ID)
		}
	}
	var attempts []CaseActionAttempt
	if len(timeouts) > 0 {
		if attempts, err = store.ListCaseActionAttempts(ctx, timeouts); err != nil {
			return nil, err
		}
	}
	notification, err := store.GetCaseNotification(ctx, item.ID)
	if err != nil {
		return nil, err
	}
	incomplete, err := store.CaseEvidenceIncomplete(ctx, item.ID)
	if err != nil {
		return nil, err
	}
	receipt := &CaseReceipt{
		CaseID:                 item.ID,
		GuildID:                item.GuildID,
		CaseNumber:             item.CaseNumber,
		CreatedAt:              item.CreatedAt,
		TargetDiscordUserID:    item.TargetDiscordUserID,
		ModeratorDiscordUserID: item.ModeratorDiscordUserID,
		RuleName:               snapshotRuleName(item.TemplateSnapshotJSON),
		Reason:                 item.Reason,
		Validity:               item.Validity,
		Appealable:             item.Validity == CaseValidityValid && snapshotAppealable(item.TemplateSnapshotJSON),
		ContextValues:          parseContextValues(item.ContextValuesJSON),
		SelectedLevel:          snapshotSelectedLevel(item.TemplateSnapshotJSON),
		EvidenceIncomplete:     incomplete,
	}
	for _, action := range actions {
		receipt.Actions = append(receipt.Actions, CaseReceiptAction{
			ID:           action.ID,
			ActionType:   action.ActionType,
			Status:       action.Status,
			Reversal:     action.ReversalOfExecutionID != nil,
			TimeoutUntil: recordedTimeoutUntil(action, attempts),
		})
	}
	if notification != nil {
		receipt.Notification = &notification.Status
	}
	return receipt, nil
}

// Receipt returns the receipt of a case the caller just created or was
// already authorized to see, for following its progress after the
// interaction that created it. It checks no permission and writes no audit
// entry, so adapters must never pass it a case reference a user typed;
// interactive views use Get or GetCompact.
func (s *CaseService) Receipt(ctx context.Context, guildID, caseID string) (*CaseReceipt, error) {
	receipt, err := loadCaseReceipt(ctx, s.store, strings.TrimSpace(caseID))
	if err != nil {
		return nil, err
	}
	if receipt.GuildID != guildID {
		return nil, ErrCaseNotFound
	}
	return receipt, nil
}

// CasePublicationService keeps public case messages up to date. The Discord
// adapter records each message it posts, then a refresh loop asks for due
// publications, rerenders each from its presentation and current receipt,
// edits the message if the rendering changed, and reports back.
type CasePublicationService struct {
	store CasePublicationStore
}

// NewCasePublicationService returns a CasePublicationService backed by store.
func NewCasePublicationService(store CasePublicationStore) *CasePublicationService {
	return &CasePublicationService{store: store}
}

// Record registers a message already posted for a committed case, due for
// refresh at publication.RetryAt (now when zero). Recording the same message
// again keeps the first record. The post and this write cannot share a
// transaction, so a crash between them leaves an untracked message; Record
// retries a few times to make that unlikely.
func (s *CasePublicationService) Record(ctx context.Context, publication CasePublication) error {
	if publication.MessageID == "" || publication.ChannelID == "" || publication.CaseID == "" {
		return errors.New("case publication message, channel, and case are required")
	}
	if publication.RetryAt.IsZero() {
		publication.RetryAt = time.Now().UTC()
	}
	var err error
	for range recordPublicationAttempts {
		if err = s.store.SaveCasePublication(ctx, publication); err == nil || ctx.Err() != nil {
			break
		}
	}
	return err
}

// Due returns up to limit (1 to 100) publications whose refresh is due at
// now.
func (s *CasePublicationService) Due(ctx context.Context, now time.Time, limit int) ([]CasePublication, error) {
	if limit < 1 || limit > 100 {
		return nil, errors.New("case publication limit must be between 1 and 100")
	}
	return s.store.ListDueCasePublications(ctx, now.UTC(), limit)
}

// Receipt returns the current receipt of a publication's case. It returns
// ErrCaseNotFound when the case is gone, in which case the publication
// should be retired.
func (s *CasePublicationService) Receipt(ctx context.Context, publication CasePublication) (*CaseReceipt, error) {
	return loadCaseReceipt(ctx, s.store, publication.CaseID)
}

// Complete records a finished refresh. See
// CompleteCasePublicationRefreshParams.
func (s *CasePublicationService) Complete(ctx context.Context, params CompleteCasePublicationRefreshParams) error {
	return s.store.CompleteCasePublicationRefresh(ctx, params)
}

// Retire stops refreshing a publication whose message or case is gone.
func (s *CasePublicationService) Retire(ctx context.Context, messageID string) error {
	return s.store.DeleteCasePublication(ctx, messageID)
}
