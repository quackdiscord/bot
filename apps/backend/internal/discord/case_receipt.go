package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

// receiptPresentation is what Quack stores with a case publication: which
// view it is, and the Discord guild it was posted in, for the dashboard
// link. Everything shown comes from the case's current receipt, so a
// refresh never restores stale context.
type receiptPresentation struct {
	View string `json:"view"`
	// DiscordGuildID is empty on receipts recorded before it was stored;
	// they refresh without a dashboard link.
	DiscordGuildID string `json:"discord_guild_id,omitempty"`
}

// caseReceiptView names the public /case add receipt.
const caseReceiptView = "case_receipt"

// publishInPlace replaces the public placeholder of /case add with the case
// receipt and records it for refresh. The case is already committed, so
// failures only change what the moderator is told.
func (c *cases) publishInPlace(ctx context.Context, responder Responder, i *discordgo.InteractionCreate, created *quack.CaseResponse) {
	message, err := editWithRetry(ctx, responder, c.receiptMessage(ctx, i, created))
	c.recordReceipt(ctx, responder, i, created, message, err)
}

// publishToChannel posts the case receipt to the interaction's channel as a
// standalone message and removes the private reply that started the flow.
func (c *cases) publishToChannel(ctx context.Context, responder Responder, i *discordgo.InteractionCreate, created *quack.CaseResponse) {
	message, err := c.poster.Send(ctx, i.ChannelID, c.receiptMessage(ctx, i, created))
	if err == nil {
		if err := responder.DeleteOriginal(); err != nil {
			_, _ = responder.EditOriginal(EditMessage(Content(fmt.Sprintf("Case #%d created.", created.CaseNumber), true)))
		}
	}
	c.recordReceipt(ctx, responder, i, created, message, err)
}

// recordReceipt registers a posted receipt so the refresh loop keeps it
// current, and tells the moderator privately when posting or recording
// failed.
func (c *cases) recordReceipt(
	ctx context.Context, responder Responder, i *discordgo.InteractionCreate,
	created *quack.CaseResponse, message *discordgo.Message, postErr error,
) {
	if postErr != nil || message == nil {
		slog.WarnContext(ctx, "Could not post case receipt", "case_id", created.ID, "error", postErr)
		_, _ = responder.Followup(Signal("error", fmt.Sprintf(
			"Case #%d was saved, but I couldn’t post the result. Check `/case view` before trying again.", created.CaseNumber), true))
		return
	}
	presentation, _ := json.Marshal(receiptPresentation{View: caseReceiptView, DiscordGuildID: i.GuildID})
	channelID := message.ChannelID
	if channelID == "" {
		channelID = i.ChannelID
	}
	err := c.services.Publications.Record(ctx, quack.CasePublication{
		MessageID:        message.ID,
		ChannelID:        channelID,
		CaseID:           created.ID,
		PresentationJSON: string(presentation),
	})
	if err != nil {
		slog.ErrorContext(ctx, "Could not record case receipt", "case_id", created.ID, "error", err)
		_, _ = responder.Followup(Signal("error", fmt.Sprintf(
			"Case #%d was saved, but its live updates aren’t working. Check `/case view` for the result.", created.CaseNumber), true))
	}
}

// receiptMessage renders the receipt of a case the moderator just created,
// linked to its dashboard page.
func (c *cases) receiptMessage(ctx context.Context, i *discordgo.InteractionCreate, created *quack.CaseResponse) Message {
	return caseReceiptMessage(c.receipt(ctx, created), c.dashboard.Staff(i.GuildID, "cases", created.ID))
}

// receipt loads the committed receipt of a case the moderator just created,
// falling back to the creation response if it cannot be read.
func (c *cases) receipt(ctx context.Context, created *quack.CaseResponse) *quack.CaseReceipt {
	receipt, err := c.services.Cases.Receipt(ctx, created.GuildID, created.ID)
	if err != nil {
		slog.WarnContext(ctx, "Could not load case receipt", "case_id", created.ID, "error", err)
		return receiptFromCase(created)
	}
	return receipt
}

// receiptFromCase builds a receipt from a creation response, for when the
// stored receipt cannot be read.
func receiptFromCase(created *quack.CaseResponse) *quack.CaseReceipt {
	receipt := &quack.CaseReceipt{
		CaseID:                 created.ID,
		GuildID:                created.GuildID,
		CaseNumber:             created.CaseNumber,
		CreatedAt:              created.CreatedAt,
		TargetDiscordUserID:    created.TargetDiscordUserID,
		ModeratorDiscordUserID: created.ModeratorDiscordUserID,
		RuleName:               created.RuleName,
		Reason:                 created.Reason,
		Validity:               created.Validity,
		ContextValues:          created.ContextValues,
		SelectedLevel:          created.SelectedLevel,
		EvidenceIncomplete:     created.EvidenceIncomplete,
	}
	for _, action := range created.Actions {
		receipt.Actions = append(receipt.Actions, quack.CaseReceiptAction{
			ID:           action.ID,
			ActionType:   action.ActionType,
			Status:       action.Status,
			TimeoutUntil: action.TimeoutUntil,
		})
	}
	return receipt
}

// editWithRetry edits the original response up to three times. The edit is
// idempotent, unlike the moderation it reports, which is never repeated.
func editWithRetry(ctx context.Context, responder Responder, message Message) (*discordgo.Message, error) {
	var err error
	for attempt := range 3 {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, err
			case <-time.After(250 * time.Millisecond):
			}
		}
		var sent *discordgo.Message
		if sent, err = responder.EditOriginal(EditMessage(message)); err == nil {
			return sent, nil
		}
	}
	return nil, err
}
