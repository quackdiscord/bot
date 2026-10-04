package discord

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

// Refresh timing for case publications.
const (
	// PublicationRefreshInterval is how often the worker should call
	// PublicationRefresher.RefreshDue.
	PublicationRefreshInterval = 2 * time.Second

	publicationBatch = 50
	// publicationPendingDelay is how soon a receipt whose enforcement or DM
	// is still settling is checked again.
	publicationPendingDelay = 2 * time.Second
	// publicationRetryDelay is how long a failed refresh waits.
	publicationRetryDelay = 30 * time.Second
)

// publicationSource is what PublicationRefresher needs from
// quack.CasePublicationService.
type publicationSource interface {
	Due(ctx context.Context, now time.Time, limit int) ([]quack.CasePublication, error)
	Receipt(ctx context.Context, publication quack.CasePublication) (*quack.CaseReceipt, error)
	Complete(ctx context.Context, params quack.CompleteCasePublicationRefreshParams) error
	Retire(ctx context.Context, messageID string) error
}

// messageEditor edits a channel message with bot credentials. *Bot
// implements it.
type messageEditor interface {
	Edit(ctx context.Context, channelID, messageID string, message Message) (*discordgo.Message, error)
}

// PublicationRefresher keeps public case receipts in Discord current. It
// edits messages with bot credentials, so receipts stay live long after the
// interaction that posted them has expired, and edits are idempotent, so
// retries and overlapping processes never post duplicates.
type PublicationRefresher struct {
	source publicationSource
	editor messageEditor
	// dashboard builds each receipt's dashboard link.
	dashboard quack.DashboardLinks
}

// NewPublicationRefresher returns a refresher that edits through bot and
// links receipts to bot's dashboard.
func NewPublicationRefresher(bot *Bot, publications *quack.CasePublicationService) *PublicationRefresher {
	return &PublicationRefresher{source: publications, editor: bot, dashboard: bot.Dashboard}
}

// RefreshDue rerenders every publication that is due and edits the ones
// whose rendering changed. A receipt still pending is checked again shortly;
// a settled one sleeps until a committed change requests a refresh. A
// publication whose message or case is gone is retired, and any other
// failure is retried later.
func (r *PublicationRefresher) RefreshDue(ctx context.Context) error {
	now := time.Now().UTC()
	due, err := r.source.Due(ctx, now, publicationBatch)
	if err != nil {
		return fmt.Errorf("list due case publications: %w", err)
	}
	var failures []error
	for _, publication := range due {
		if err := ctx.Err(); err != nil {
			return err
		}
		digest, pending, err := r.refresh(ctx, publication)
		if errors.Is(err, errPublicationGone) {
			if err := r.source.Retire(ctx, publication.MessageID); err != nil {
				failures = append(failures, err)
			}
			continue
		}
		params := quack.CompleteCasePublicationRefreshParams{
			MessageID: publication.MessageID,
			Revision:  publication.Revision,
			Digest:    digest,
		}
		switch {
		case err != nil:
			failures = append(failures, err)
			params.Digest = publication.LastDigest
			params.RetryAt, params.RefreshRequested = now.Add(publicationRetryDelay), true
		case pending:
			params.RetryAt, params.RefreshRequested = now.Add(publicationPendingDelay), true
		default:
			params.RetryAt = now
		}
		if err := r.source.Complete(ctx, params); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// errPublicationGone means a publication's message or case no longer
// exists, so it can never be refreshed.
var errPublicationGone = errors.New("case publication is gone")

// refresh renders one publication and edits its message if the rendering
// changed. It returns the digest now displayed and whether the receipt is
// still settling.
func (r *PublicationRefresher) refresh(ctx context.Context, publication quack.CasePublication) (string, bool, error) {
	var presentation receiptPresentation
	if err := json.Unmarshal([]byte(publication.PresentationJSON), &presentation); err != nil || presentation.View != caseReceiptView {
		return "", false, fmt.Errorf("case publication %s has an unknown presentation", publication.MessageID)
	}
	receipt, err := r.source.Receipt(ctx, publication)
	if errors.Is(err, quack.ErrCaseNotFound) {
		return "", false, errPublicationGone
	}
	if err != nil {
		return "", false, err
	}
	message := caseReceiptMessage(receipt, r.dashboard.Staff(presentation.DiscordGuildID, "cases", receipt.CaseID))
	encoded, err := json.Marshal(message)
	if err != nil {
		return "", false, err
	}
	sum := sha256.Sum256(encoded)
	digest := hex.EncodeToString(sum[:])
	if digest == publication.LastDigest {
		return digest, receipt.Pending(), nil
	}
	if _, err := r.editor.Edit(ctx, publication.ChannelID, publication.MessageID, message); err != nil {
		if restErr, ok := errors.AsType[*discordgo.RESTError](err); ok && restErr.Message != nil &&
			(restErr.Message.Code == discordgo.ErrCodeUnknownMessage || restErr.Message.Code == discordgo.ErrCodeUnknownChannel) {
			return "", false, errPublicationGone
		}
		return "", false, fmt.Errorf("edit case publication %s: %w", publication.MessageID, err)
	}
	return digest, receipt.Pending(), nil
}
