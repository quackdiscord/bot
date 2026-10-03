package tickets_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/tickets"
	"gorm.io/gorm"
)

// queueFailureFake fails the open ticket's queue post on request.
type queueFailureFake struct {
	*discordFake
	failure error
	calls   int
}

func (f *queueFailureFake) PublishQueue(ctx context.Context, ticket *tickets.Ticket, settings tickets.Settings, transcript *tickets.Transcript) (*tickets.QueueReceipt, error) {
	f.calls++
	if f.failure != nil {
		return nil, f.failure
	}
	return f.discordFake.PublishQueue(ctx, ticket, settings, transcript)
}

// TestQueueRepairRetriesOnlyRefusedSends checks that a refused first post
// is sent again by a repair, while an uncertain one stays fenced even
// across adapter restarts.
func TestQueueRepairRetriesOnlyRefusedSends(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		t.Run(map[bool]string{false: "refused", true: "uncertain"}[uncertain], func(t *testing.T) {
			_, service, _ := setup(t)
			ctx := context.Background()
			failure := tickets.ErrQueueNotSent
			if uncertain {
				failure = errors.New("response lost")
			}
			client := &queueFailureFake{discordFake: &discordFake{}, failure: failure}
			owner := modules.Actor{GuildID: "guild-a", DiscordUserID: "owner"}
			ticket, err := tickets.NewDiscordAdapter(service, client).Open(ctx, owner)
			if err == nil || ticket == nil {
				t.Fatalf("open = %v, %v", ticket, err)
			}
			client.failure = nil
			adapter := tickets.NewDiscordAdapter(service, client)
			manager := modules.Actor{GuildID: "guild-a", DiscordUserID: "admin", CanManage: true, CanModerate: true}
			for range 2 {
				err := adapter.RepairPermissions(ctx, manager, ticket.ID)
				if uncertain != errors.Is(err, tickets.ErrQueueDeliveryUnknown) || (!uncertain && err != nil) {
					t.Fatalf("repair: %v", err)
				}
			}
			want := 2
			if uncertain {
				want = 1
			}
			if client.calls != want || client.channelCalls != 1 {
				t.Fatalf("queue calls = %d, threads = %d", client.calls, client.channelCalls)
			}
			detail, _, err := service.Detail(ctx, manager, ticket.ID)
			if err != nil || (detail.LogMessageDiscordID != "") == uncertain {
				t.Fatalf("detail = %+v, %v", detail, err)
			}
		})
	}
}

// recoveryFake tells edits, sends, and receipt reads apart.
type recoveryFake struct {
	*discordFake
	exists           bool
	readErr, sendErr error
	missingEdit      bool
	sends, edits     int
	receipt          *tickets.QueueReceipt
	validationErr    error
}

func (f *recoveryFake) QueueMessageExists(context.Context, string, string) (bool, error) {
	return f.exists, f.readErr
}

func (f *recoveryFake) PublishQueue(_ context.Context, ticket *tickets.Ticket, _ tickets.Settings, _ *tickets.Transcript) (*tickets.QueueReceipt, error) {
	if ticket.LogMessageDiscordID != "" {
		f.edits++
		if f.missingEdit {
			return nil, tickets.ErrQueueMessageMissing
		}
		return &tickets.QueueReceipt{MessageID: ticket.LogMessageDiscordID, URL: "saved"}, nil
	}
	f.sends++
	if f.sendErr != nil {
		return nil, f.sendErr
	}
	f.exists = true
	return &tickets.QueueReceipt{MessageID: fmt.Sprintf("queue-%d", f.sends), URL: "saved"}, nil
}

func (f *recoveryFake) ValidateQueueMessage(context.Context, *tickets.Ticket, string) (*tickets.QueueReceipt, error) {
	return f.receipt, f.validationErr
}

// uncertainQueueFixture leaves an open ticket whose first queue post has an
// unknown outcome.
func uncertainQueueFixture(t *testing.T) (*gorm.DB, *tickets.Service, *tickets.DiscordAdapter, *recoveryFake, *tickets.Ticket) {
	t.Helper()
	db, service, _ := setup(t)
	client := &recoveryFake{discordFake: &discordFake{}, sendErr: errors.New("response lost")}
	adapter := tickets.NewDiscordAdapter(service, client)
	ticket, err := adapter.Open(context.Background(), modules.Actor{GuildID: "guild-a", DiscordUserID: "owner"})
	if err == nil || ticket == nil || ticket.QueueDeliveryAttemptID == "" {
		t.Fatalf("open = %+v, %v; want an uncertain attempt", ticket, err)
	}
	return db, service, adapter, client, ticket
}

// TestQueueRecoveryAuthorityAndOldFences lets only the guild's managers
// inspect, and gives a fence without an attempt ID a stable one.
func TestQueueRecoveryAuthorityAndOldFences(t *testing.T) {
	db, _, adapter, _, ticket := uncertainQueueFixture(t)
	ctx := context.Background()
	for _, actor := range []modules.Actor{
		{GuildID: "guild-a", DiscordUserID: "owner"},
		{GuildID: "guild-a", DiscordUserID: "moderator", CanModerate: true},
		{GuildID: "guild-b", DiscordUserID: "manager", CanManage: true},
	} {
		if recovery, err := adapter.QueueRecovery(ctx, actor, ticket.ID); err == nil || recovery != nil {
			t.Fatalf("unauthorized recovery: %+v, %v", recovery, err)
		}
		input := tickets.QueueRecoveryInput{TicketID: ticket.ID, AttemptID: ticket.QueueDeliveryAttemptID, ConfirmNotDelivered: true}
		if _, err := adapter.ReconcileQueue(ctx, actor, input); err == nil {
			t.Fatal("unauthorized reconciliation")
		}
	}
	if err := db.Table("tickets").Where("id = ?", ticket.ID).Update("queue_delivery_attempt_id", nil).Error; err != nil {
		t.Fatal(err)
	}
	manager := modules.Actor{GuildID: "guild-a", DiscordUserID: "manager", CanManage: true}
	first, err := adapter.QueueRecovery(ctx, manager, ticket.ID)
	if err != nil || first.AttemptID == "" || first.ChannelDiscordID != "queue" {
		t.Fatalf("recovery = %+v, %v", first, err)
	}
	second, err := adapter.QueueRecovery(ctx, manager, ticket.ID)
	if err != nil || *first != *second {
		t.Fatalf("attempt changed on inspection: %+v, %+v, %v", first, second, err)
	}
	if err := db.Table("ticket_member_states").Where("guild_id = ?", "guild-a").Update("open_ticket_id", "new-ticket").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.QueueRecovery(ctx, manager, ticket.ID); !errors.Is(err, tickets.ErrInvalidTransition) {
		t.Fatalf("superseded ticket recovered: %v", err)
	}
}

// TestQueueAdoptionKeepsCloseBoundary adopts a post without sending, then
// closes normally: the transcript is attached by editing that post before
// the thread goes.
func TestQueueAdoptionKeepsCloseBoundary(t *testing.T) {
	_, service, adapter, client, ticket := uncertainQueueFixture(t)
	ctx := context.Background()
	owner := modules.Actor{GuildID: "guild-a", DiscordUserID: "owner"}
	manager := modules.Actor{GuildID: "guild-a", DiscordUserID: "manager", CanManage: true}
	if _, err := service.Resolve(ctx, owner, ticket.ID, "canonical transcript"); err != nil {
		t.Fatal(err)
	}
	client.receipt = &tickets.QueueReceipt{MessageID: "existing", URL: "not-trusted-as-transcript"}
	adopted, err := adapter.ReconcileQueue(ctx, manager, tickets.QueueRecoveryInput{
		TicketID: ticket.ID, AttemptID: ticket.QueueDeliveryAttemptID,
		MessageURL: "https://discord.com/channels/guild/queue/existing",
	})
	if err != nil {
		t.Fatal(err)
	}
	if adopted.LogMessageDiscordID != "existing" || adopted.TranscriptURL != "" || adopted.QueueDeliveryAttemptID != "" ||
		client.sends != 1 || client.deleteAttempts != 0 {
		t.Fatalf("adoption bypassed the lifecycle: %+v", adopted)
	}
	if pending, err := service.ClosurePending(ctx, owner, ticket.ID); err != nil || !pending {
		t.Fatalf("adoption released the slot: %v, %v", pending, err)
	}
	client.sendErr, client.exists = nil, true
	if _, err := adapter.Close(ctx, owner, ticket.ID); err != nil {
		t.Fatal(err)
	}
	if client.edits != 1 || client.sends != 1 || client.deleteAttempts != 1 {
		t.Fatalf("edits = %d, sends = %d, deletes = %d", client.edits, client.sends, client.deleteAttempts)
	}
	_, events, err := service.Detail(ctx, owner, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range events {
		if event.Type == tickets.EventQueueReconciled {
			count++
			if event.ActorDiscordUserID != manager.DiscordUserID {
				t.Fatalf("event actor = %q", event.ActorDiscordUserID)
			}
		}
	}
	if count != 1 {
		t.Fatalf("reconciled events = %d", count)
	}
	if _, err := adapter.QueueRecovery(ctx, manager, ticket.ID); !errors.Is(err, tickets.ErrInvalidTransition) {
		t.Fatalf("closed ticket recovered: %v", err)
	}
}

// TestQueueConfirmationUsesAttemptIdentity allows one replacement after a
// confirmation and refuses the old confirmation for the next attempt.
func TestQueueConfirmationUsesAttemptIdentity(t *testing.T) {
	_, service, adapter, client, ticket := uncertainQueueFixture(t)
	ctx := context.Background()
	manager := modules.Actor{GuildID: "guild-a", DiscordUserID: "owner", CanManage: true}
	input := tickets.QueueRecoveryInput{TicketID: ticket.ID, AttemptID: ticket.QueueDeliveryAttemptID, ConfirmNotDelivered: true}
	cleared, err := adapter.ReconcileQueue(ctx, manager, input)
	if err != nil || cleared.QueueDeliveryAttemptID != "" || cleared.LogChannelDiscordID != "" || client.sends != 1 {
		t.Fatalf("confirmation = %+v, %v", cleared, err)
	}
	if active, err := service.ActiveForMember(ctx, manager); err != nil || active == nil || active.ID != ticket.ID {
		t.Fatalf("confirmation released the member: %+v, %v", active, err)
	}
	if err := adapter.RepairPermissions(ctx, manager, ticket.ID); err == nil {
		t.Fatal("expected another uncertain send")
	}
	next, err := adapter.QueueRecovery(ctx, manager, ticket.ID)
	if err != nil || next.AttemptID == input.AttemptID || next.ChannelDiscordID != "queue" {
		t.Fatalf("next = %+v, %v", next, err)
	}
	if _, err := adapter.ReconcileQueue(ctx, manager, input); !errors.Is(err, tickets.ErrQueueDeliveryUnknown) {
		t.Fatalf("stale confirmation cleared a newer attempt: %v", err)
	}
	if client.sends != 2 {
		t.Fatalf("sends = %d", client.sends)
	}
}

// TestQueueReconciliationRejectsBadInputAndRollsBack leaves the fence in
// place for invalid input, a rejected link, and a failed timeline write.
func TestQueueReconciliationRejectsBadInputAndRollsBack(t *testing.T) {
	db, _, adapter, client, ticket := uncertainQueueFixture(t)
	ctx := context.Background()
	manager := modules.Actor{GuildID: "guild-a", DiscordUserID: "manager", CanManage: true}
	attempt := ticket.QueueDeliveryAttemptID
	for _, input := range []tickets.QueueRecoveryInput{
		{TicketID: ticket.ID, AttemptID: attempt},
		{TicketID: ticket.ID, AttemptID: attempt, MessageURL: "link", ConfirmNotDelivered: true},
		{TicketID: ticket.ID, ConfirmNotDelivered: true},
	} {
		if _, err := adapter.ReconcileQueue(ctx, manager, input); !errors.Is(err, tickets.ErrInvalidQueueReceipt) {
			t.Fatalf("input %+v: %v", input, err)
		}
	}
	client.validationErr = tickets.ErrInvalidQueueReceipt
	input := tickets.QueueRecoveryInput{TicketID: ticket.ID, AttemptID: attempt, MessageURL: "wrong-message"}
	if _, err := adapter.ReconcileQueue(ctx, manager, input); !errors.Is(err, tickets.ErrInvalidQueueReceipt) {
		t.Fatal(err)
	}
	want := errors.New("timeline write failed")
	if err := db.Callback().Create().Before("gorm:create").Register("reject_recovery_event", func(tx *gorm.DB) {
		if tx.Statement.Table == "ticket_events" {
			_ = tx.AddError(want)
		}
	}); err != nil {
		t.Fatal(err)
	}
	input = tickets.QueueRecoveryInput{TicketID: ticket.ID, AttemptID: attempt, ConfirmNotDelivered: true}
	if _, err := adapter.ReconcileQueue(ctx, manager, input); !errors.Is(err, want) {
		t.Fatal(err)
	}
	current, err := adapter.QueueRecovery(ctx, manager, ticket.ID)
	if err != nil || current.AttemptID != attempt {
		t.Fatalf("failed write lost the fence: %+v, %v", current, err)
	}
}

// TestQueueConcurrentConfirmation admits one of two simultaneous clicks.
func TestQueueConcurrentConfirmation(t *testing.T) {
	_, service, adapter, _, ticket := uncertainQueueFixture(t)
	ctx := context.Background()
	manager := modules.Actor{GuildID: "guild-a", DiscordUserID: "owner", CanManage: true}
	input := tickets.QueueRecoveryInput{TicketID: ticket.ID, AttemptID: ticket.QueueDeliveryAttemptID, ConfirmNotDelivered: true}
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			_, err := adapter.ReconcileQueue(ctx, manager, input)
			results <- err
		})
	}
	workers.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("succeeded = %d", succeeded)
	}
	_, events, err := service.Detail(ctx, manager, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range events {
		if event.Type == tickets.EventQueueReconciled {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("reconciled events = %d", count)
	}
}

// TestMissingQueueRepairFencesUncertainReplacement replaces a deleted
// queue post, and keeps an uncertain replacement fenced across restarts.
func TestMissingQueueRepairFencesUncertainReplacement(t *testing.T) {
	for _, failure := range []string{"none", "refused", "response", "receipt"} {
		t.Run(failure, func(t *testing.T) {
			db, service, _ := setup(t)
			ctx := context.Background()
			client := &recoveryFake{discordFake: &discordFake{}}
			actor := modules.Actor{GuildID: "guild-a", DiscordUserID: "owner", CanManage: true}
			ticket, err := tickets.NewDiscordAdapter(service, client).Open(ctx, actor)
			if err != nil {
				t.Fatal(err)
			}
			client.exists = false
			switch failure {
			case "response":
				client.sendErr = errors.New("response lost")
			case "refused":
				client.sendErr = tickets.ErrQueueNotSent
			case "receipt":
				err := db.Callback().Update().Before("gorm:update").Register("fail_replacement_receipt", func(tx *gorm.DB) {
					if values, ok := tx.Statement.Dest.(map[string]any); ok && values["log_message_discord_id"] == "queue-2" {
						_ = tx.AddError(errors.New("receipt write failed"))
					}
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			err = tickets.NewDiscordAdapter(service, client).RepairPermissions(ctx, actor, ticket.ID)
			if (err == nil) != (failure == "none") {
				t.Fatalf("replacement: %v", err)
			}
			client.sendErr = nil
			err = tickets.NewDiscordAdapter(service, client).RepairPermissions(ctx, actor, ticket.ID)
			if failure == "response" || failure == "receipt" {
				if !errors.Is(err, tickets.ErrQueueDeliveryUnknown) {
					t.Fatalf("uncertain replacement retried: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			wantSends := 2
			if failure == "refused" {
				wantSends = 3
			}
			if client.sends != wantSends {
				t.Fatalf("sends = %d, want %d", client.sends, wantSends)
			}
		})
	}
}

// TestCloseRechecksReceiptAfterDeleteFailure keeps the thread on an
// uncertain read, and republishes a transcript whose post is gone.
func TestCloseRechecksReceiptAfterDeleteFailure(t *testing.T) {
	_, service, _ := setup(t)
	ctx := context.Background()
	client := &recoveryFake{discordFake: &discordFake{failDelete: 1}}
	adapter := tickets.NewDiscordAdapter(service, client)
	actor := modules.Actor{GuildID: "guild-a", DiscordUserID: "owner"}
	ticket, err := adapter.Open(ctx, actor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Close(ctx, actor, ticket.ID); err == nil {
		t.Fatal("expected the delete to fail")
	}
	client.readErr = errors.New("queue read unavailable")
	if _, err := adapter.Close(ctx, actor, ticket.ID); err == nil || client.deleteAttempts != 1 {
		t.Fatalf("deleted after an uncertain read: %v", err)
	}
	client.readErr, client.exists, client.sendErr = nil, false, tickets.ErrQueueNotSent
	if _, err := adapter.Close(ctx, actor, ticket.ID); err == nil || client.deleteAttempts != 1 {
		t.Fatalf("deleted before the replacement: %v", err)
	}
	current, _, err := service.Detail(ctx, actor, ticket.ID)
	if err != nil || current.TranscriptURL != "" {
		t.Fatalf("missing transcript still trusted: %+v, %v", current, err)
	}
	if pending, err := service.ClosurePending(ctx, actor, ticket.ID); err != nil || !pending {
		t.Fatalf("slot released: %v, %v", pending, err)
	}
	client.sendErr = nil
	if _, err := adapter.Close(ctx, actor, ticket.ID); err != nil {
		t.Fatal(err)
	}
	if client.deleteAttempts != 2 || client.sends != 3 {
		t.Fatalf("deletes = %d, sends = %d", client.deleteAttempts, client.sends)
	}
	if pending, err := service.ClosurePending(ctx, actor, ticket.ID); err != nil || pending {
		t.Fatalf("cleanup still pending: %v, %v", pending, err)
	}
}

// TestCloseReplacementSendIsFenced covers a deleted post and a moved queue:
// neither may send again after an uncertain replacement.
func TestCloseReplacementSendIsFenced(t *testing.T) {
	for _, moved := range []bool{false, true} {
		t.Run(fmt.Sprint(moved), func(t *testing.T) {
			_, service, _ := setup(t)
			ctx := context.Background()
			client := &recoveryFake{discordFake: &discordFake{}}
			actor := modules.Actor{GuildID: "guild-a", DiscordUserID: "owner", CanManage: true}
			ticket, err := tickets.NewDiscordAdapter(service, client).Open(ctx, actor)
			if err != nil {
				t.Fatal(err)
			}
			if moved {
				settings := enabledSettings()
				settings.QueueChannelDiscordID = "new-queue"
				if _, err := service.UpdateSettings(ctx, actor, true, settings); err != nil {
					t.Fatal(err)
				}
			} else {
				client.missingEdit = true
			}
			client.sendErr = errors.New("response lost")
			if _, err := tickets.NewDiscordAdapter(service, client).Close(ctx, actor, ticket.ID); err == nil {
				t.Fatal("uncertain send succeeded")
			}
			if _, err := tickets.NewDiscordAdapter(service, client).Close(ctx, actor, ticket.ID); !errors.Is(err, tickets.ErrQueueDeliveryUnknown) {
				t.Fatalf("replacement retried: %v", err)
			}
			if client.sends != 2 || client.deleteAttempts != 0 {
				t.Fatalf("sends = %d, deletes = %d", client.sends, client.deleteAttempts)
			}
		})
	}
}
