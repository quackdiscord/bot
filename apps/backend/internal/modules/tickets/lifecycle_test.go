package tickets_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/tickets"
)

func TestLifecyclePrivacyDuplicateAndIsolation(t *testing.T) {
	_, service, audit := setup(t)
	ctx := context.Background()
	adapter := tickets.NewDiscordAdapter(service, &discordFake{})
	member := modules.Actor{GuildID: "guild-a", DiscordUserID: "member"}
	staff := modules.Actor{GuildID: "guild-a", DiscordUserID: "staff", CanModerate: true}

	ticket, err := adapter.Open(ctx, member)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Open(ctx, member); !errors.Is(err, tickets.ErrDuplicateOpen) {
		t.Fatalf("second open: got %v, want ErrDuplicateOpen", err)
	}
	if _, _, err := service.Detail(ctx, modules.Actor{GuildID: "guild-a", DiscordUserID: "other"}, ticket.ID); !errors.Is(err, tickets.ErrPermissionDenied) {
		t.Fatalf("other member read the ticket: %v", err)
	}
	if _, _, err := service.Detail(ctx, modules.Actor{GuildID: "guild-b", DiscordUserID: "staff", CanModerate: true}, ticket.ID); !errors.Is(err, tickets.ErrNotFound) {
		t.Fatalf("another guild's staff read the ticket: %v", err)
	}
	if _, err := adapter.Close(ctx, modules.Actor{GuildID: "guild-a", DiscordUserID: "other"}, ticket.ID); !errors.Is(err, tickets.ErrPermissionDenied) {
		t.Fatalf("unrelated member closed the ticket: %v", err)
	}
	closed, err := adapter.Close(ctx, staff, ticket.ID)
	if err != nil || closed.Status != tickets.StatusResolved {
		t.Fatalf("close = %+v, %v", closed, err)
	}
	if got := transcriptOf(t, service, member, ticket.ID); !strings.Contains(got, "captured") {
		t.Fatalf("transcript = %q", got)
	}
	if _, err := service.Resolve(ctx, member, ticket.ID, "replacement"); !errors.Is(err, tickets.ErrInvalidTransition) {
		t.Fatalf("closed ticket resolved again: %v", err)
	}
	// Closing released the slot, and there is no daily limit.
	for range 3 {
		next, err := adapter.Open(ctx, member)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := adapter.Close(ctx, member, next.ID); err != nil {
			t.Fatal(err)
		}
	}
	if len(audit.events) < 5 {
		t.Fatalf("audit events = %d", len(audit.events))
	}
}

// TestOpeningLimitsPrecedeProvisioning covers repeated button clicks and a
// failed invitation: the saved ticket keeps the slot, staff still hear
// about it, and a repair finishes it without a second thread.
func TestOpeningLimitsPrecedeProvisioning(t *testing.T) {
	_, service, _ := setup(t)
	ctx := context.Background()
	client := &discordFake{}
	adapter := tickets.NewDiscordAdapter(service, client)
	actor := modules.Actor{GuildID: "guild-a", DiscordUserID: "member"}
	if _, err := adapter.Open(ctx, actor); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		if _, err := adapter.Open(ctx, actor); !errors.Is(err, tickets.ErrDuplicateOpen) {
			t.Fatalf("repeated open: got %v, want ErrDuplicateOpen", err)
		}
	}
	if client.channelCalls != 1 {
		t.Fatalf("duplicate opens created %d threads", client.channelCalls)
	}

	actor.DiscordUserID = "failed-member"
	client.permissionError = errors.New("invitation failed")
	saved, err := adapter.Open(ctx, actor)
	if err == nil || saved == nil {
		t.Fatalf("open = %v, %v; want a saved ticket with an error", saved, err)
	}
	if _, err := adapter.Open(ctx, actor); !errors.Is(err, tickets.ErrDuplicateOpen) {
		t.Fatalf("failed invitation released the slot: %v", err)
	}
	detail, _, err := service.Detail(ctx, actor, saved.ID)
	if err != nil || detail.LogMessageDiscordID == "" {
		t.Fatalf("failed invitation skipped the queue post: %+v, %v", detail, err)
	}
	client.permissionError = nil
	if err := adapter.RepairPermissions(ctx, actor, saved.ID); !errors.Is(err, tickets.ErrPermissionDenied) {
		t.Fatalf("member repaired a ticket: %v", err)
	}
	actor.CanManage = true
	if err := adapter.RepairPermissions(ctx, actor, saved.ID); err != nil {
		t.Fatal(err)
	}
	if client.channelCalls != 2 || len(client.deleted) != 0 {
		t.Fatalf("threads = %d, deleted = %v", client.channelCalls, client.deleted)
	}
}

func TestDeletedChannelsAreRecordedAndEntryDisablesTickets(t *testing.T) {
	_, service, _ := setup(t)
	ctx := context.Background()
	staff := modules.Actor{GuildID: "guild-a", DiscordUserID: "staff", CanModerate: true, CanManage: true}
	ticket, err := tickets.NewDiscordAdapter(service, &discordFake{}).Open(ctx, modules.Actor{GuildID: "guild-a", DiscordUserID: "member"})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RecordChannelMissing(ctx, "guild-a", ticket.ID, ticket.ThreadDiscordChannelID); err != nil {
		t.Fatal(err)
	}
	if err := service.RepairDeletedEntryChannel(ctx, "guild-a", "entry"); err != nil {
		t.Fatal(err)
	}
	status, err := service.Status(ctx, staff)
	if err != nil || status.Enabled || status.EntryConfigured || status.OpenTickets != 1 {
		t.Fatalf("status = %+v, %v", status, err)
	}
	_, events, err := service.Detail(ctx, staff, ticket.ID)
	if err != nil || events[len(events)-1].Type != tickets.EventChannelMissing {
		t.Fatalf("events = %+v, %v", events, err)
	}
}

// TestOwnerCanCloseAfterModuleDisabled keeps closing available while no
// new ticket can open.
func TestOwnerCanCloseAfterModuleDisabled(t *testing.T) {
	_, service, _ := setup(t)
	ctx := context.Background()
	member := modules.Actor{GuildID: "guild-a", DiscordUserID: "member"}
	adapter := tickets.NewDiscordAdapter(service, &discordFake{})
	ticket, err := adapter.Open(ctx, member)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateSettings(ctx, admin, false, enabledSettings()); err != nil {
		t.Fatal(err)
	}
	closed, err := adapter.Close(ctx, member, ticket.ID)
	if err != nil || closed.Status != tickets.StatusResolved {
		t.Fatalf("owner close = %+v, %v", closed, err)
	}
	if _, err := adapter.Open(ctx, member); !errors.Is(err, tickets.ErrDisabled) {
		t.Fatalf("disabled module opened a ticket: %v", err)
	}
}

// TestDeletionWaitsForTranscriptPublication fails the upload, then the
// delete, and checks nothing is deleted or uploaded twice.
func TestDeletionWaitsForTranscriptPublication(t *testing.T) {
	_, service, _ := setup(t)
	ctx := context.Background()
	client := &discordFake{}
	adapter := tickets.NewDiscordAdapter(service, client)
	actor := modules.Actor{GuildID: "guild-a", DiscordUserID: "member"}
	ticket, err := adapter.Open(ctx, actor)
	if err != nil {
		t.Fatal(err)
	}
	active, err := service.ActiveForMember(ctx, actor)
	if err != nil || active == nil || active.ID != ticket.ID {
		t.Fatalf("own ticket = %+v, %v", active, err)
	}
	for _, other := range []modules.Actor{{GuildID: "guild-a", DiscordUserID: "other"}, {GuildID: "other-guild", DiscordUserID: "member"}} {
		if active, err := service.ActiveForMember(ctx, other); err != nil || active != nil {
			t.Fatalf("leaked another member's ticket: %+v, %v", active, err)
		}
	}
	client.failPublish = true
	if _, err := adapter.Close(ctx, actor, ticket.ID); err == nil {
		t.Fatal("failed upload reported success")
	}
	if client.deleteAttempts != 0 {
		t.Fatal("thread deleted before the transcript was published")
	}
	if got := transcriptOf(t, service, actor, ticket.ID); !strings.Contains(got, "captured") {
		t.Fatalf("failed upload lost the transcript: %q", got)
	}
	if _, err := adapter.Open(ctx, actor); !errors.Is(err, tickets.ErrDuplicateOpen) {
		t.Fatalf("opened during a failed close: %v", err)
	}
	client.failPublish, client.failDelete = false, 1
	if _, err := adapter.Close(ctx, actor, ticket.ID); err == nil {
		t.Fatal("expected the first delete to fail")
	}
	if _, err := adapter.Open(ctx, actor); !errors.Is(err, tickets.ErrDuplicateOpen) {
		t.Fatalf("opened before the thread was deleted: %v", err)
	}
	if _, err := adapter.Close(ctx, actor, ticket.ID); err != nil {
		t.Fatal(err)
	}
	if client.transcriptPublishes != 2 || client.deleteAttempts != 2 {
		t.Fatalf("publishes = %d, deletes = %d", client.transcriptPublishes, client.deleteAttempts)
	}
	detail, _, err := service.Detail(ctx, actor, ticket.ID)
	if err != nil || detail.TranscriptURL == "" || detail.LogMessageDiscordID == "" {
		t.Fatalf("missing transcript receipt: %+v, %v", detail, err)
	}
	if _, err := adapter.Open(ctx, actor); err != nil {
		t.Fatalf("finished close kept the slot: %v", err)
	}
	// A late retry of the old close must not free the new ticket's slot.
	if _, err := adapter.Close(ctx, actor, ticket.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Open(ctx, actor); !errors.Is(err, tickets.ErrDuplicateOpen) {
		t.Fatalf("old close released the newer ticket: %v", err)
	}
}

// TestConcurrentCloseRunsOnePipeline has the Discord button and the API
// close at once; the transcript is published once.
func TestConcurrentCloseRunsOnePipeline(t *testing.T) {
	_, service, _ := setup(t)
	client := &discordFake{}
	adapter := tickets.NewDiscordAdapter(service, client)
	actor := modules.Actor{GuildID: "guild-a", DiscordUserID: "member"}
	ticket, err := adapter.Open(context.Background(), actor)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 12)
	for range 12 {
		go func() {
			<-start
			_, err := adapter.Close(context.Background(), actor, ticket.ID)
			results <- err
		}()
	}
	close(start)
	for range 12 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if client.transcriptPublishes != 1 {
		t.Fatalf("published the transcript %d times", client.transcriptPublishes)
	}
}

// TestEntryPanelReceiptKeepsNewerSettings keeps a slow setup from
// overwriting a newer entry channel.
func TestEntryPanelReceiptKeepsNewerSettings(t *testing.T) {
	_, service, _ := setup(t)
	ctx := context.Background()
	if err := service.RecordEntryPanel(ctx, admin, "entry", "panel"); err != nil {
		t.Fatal(err)
	}
	settings, _, err := service.Settings(ctx, admin)
	if err != nil || settings.EntryPanelMessageID != "panel" || settings.QueueChannelDiscordID != "queue" {
		t.Fatalf("settings = %+v, %v", settings, err)
	}
	settings.EntryChannelDiscordID = "new-entry"
	if _, err := service.UpdateSettings(ctx, admin, true, settings); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordEntryPanel(ctx, admin, "entry", "late-panel"); err == nil {
		t.Fatal("stale panel receipt accepted")
	}
	settings, _, err = service.Settings(ctx, admin)
	if err != nil || settings.EntryChannelDiscordID != "new-entry" || settings.EntryPanelMessageID != "panel" {
		t.Fatalf("stale receipt overwrote settings: %+v, %v", settings, err)
	}
	if err := service.RecordEntryPanel(ctx, modules.Actor{GuildID: "guild-a"}, "new-entry", "panel"); !errors.Is(err, tickets.ErrPermissionDenied) {
		t.Fatalf("non-manager recorded a panel: %v", err)
	}
}

// noticeFake models a delivered, refused, or uncertain close DM.
type noticeFake struct {
	*discordFake
	mode              string
	sends, reconciles int
}

func (f *noticeFake) DeliverCloseNotice(_ context.Context, _ *tickets.Ticket, transcript *tickets.Transcript, reconcileOnly bool) (string, error) {
	if transcript == nil || transcript.Content == "" {
		return "", errors.New("transcript missing")
	}
	if reconcileOnly {
		f.reconciles++
		return "member-receipt", nil
	}
	f.sends++
	switch f.mode {
	case "blocked":
		return "", tickets.ErrCloseNoticeNotSent
	case "uncertain":
		return "", errors.New("connection lost after send")
	}
	return "member-receipt", nil
}

// TestCloseNoticeNeverDuplicatesAcceptedDM checks receipts and uncertain
// sends across adapter restarts, and that a blocked DM still closes.
func TestCloseNoticeNeverDuplicatesAcceptedDM(t *testing.T) {
	for _, mode := range []string{"success", "uncertain", "blocked"} {
		t.Run(mode, func(t *testing.T) {
			_, service, _ := setup(t)
			ctx := context.Background()
			client := &noticeFake{discordFake: &discordFake{}, mode: mode}
			actor := modules.Actor{GuildID: "guild-a", DiscordUserID: "member"}
			ticket, err := tickets.NewDiscordAdapter(service, client).Open(ctx, actor)
			if err != nil {
				t.Fatal(err)
			}
			if mode != "blocked" {
				client.failDelete = 1
			}
			closed, err := tickets.NewDiscordAdapter(service, client).Close(ctx, actor, ticket.ID)
			if mode == "blocked" {
				if err != nil || closed.CloseNoticeDelivered {
					t.Fatalf("blocked DM: %+v, %v", closed, err)
				}
				if active, err := service.ActiveForMember(ctx, actor); err != nil || active != nil {
					t.Fatalf("slot not released: %+v, %v", active, err)
				}
				client.mode = "success"
				closed, err = tickets.NewDiscordAdapter(service, client).Close(ctx, actor, ticket.ID)
				if err != nil || !closed.CloseNoticeDelivered || client.sends != 2 {
					t.Fatalf("refused DM could not be retried: %+v, %v, sends = %d", closed, err, client.sends)
				}
				return
			}
			if err == nil {
				t.Fatal("expected the delete to fail")
			}
			closed, err = tickets.NewDiscordAdapter(service, client).Close(ctx, actor, ticket.ID)
			if err != nil || !closed.CloseNoticeDelivered {
				t.Fatalf("retry = %+v, %v", closed, err)
			}
			if client.sends != 1 {
				t.Fatalf("sent %d DMs", client.sends)
			}
			if mode == "uncertain" && client.reconciles != 1 {
				t.Fatal("uncertain DM was not looked for")
			}
		})
	}
}

// progressFake records the order of the steps the member can observe.
type progressFake struct {
	*discordFake
	order []string
}

func (f *progressFake) PublishQueue(ctx context.Context, ticket *tickets.Ticket, settings tickets.Settings, transcript *tickets.Transcript) (*tickets.QueueReceipt, error) {
	if transcript != nil {
		f.order = append(f.order, "publish")
	}
	return f.discordFake.PublishQueue(ctx, ticket, settings, transcript)
}

func (f *progressFake) DeleteThread(ctx context.Context, thread string) error {
	f.order = append(f.order, "delete")
	return f.discordFake.DeleteThread(ctx, thread)
}

// TestCloseProgressFollowsPublication checks the acknowledgement comes
// after the transcript is safe and before the delete, and every failure
// stays a failure.
func TestCloseProgressFollowsPublication(t *testing.T) {
	for _, scenario := range []string{"success", "publish_failure", "ack_failure", "delete_failure"} {
		t.Run(scenario, func(t *testing.T) {
			_, service, _ := setup(t)
			client := &progressFake{discordFake: &discordFake{}}
			adapter := tickets.NewDiscordAdapter(service, client)
			actor := modules.Actor{GuildID: "guild-a", DiscordUserID: "member"}
			ticket, err := adapter.Open(context.Background(), actor)
			if err != nil {
				t.Fatal(err)
			}
			client.failPublish = scenario == "publish_failure"
			if scenario == "delete_failure" {
				client.failDelete = 1
			}
			_, err = adapter.CloseWithProgress(context.Background(), actor, ticket.ID, func(saved *tickets.Ticket) error {
				client.order = append(client.order, "ack")
				if saved.TranscriptURL == "" {
					t.Error("acknowledged before the transcript receipt")
				}
				if scenario == "ack_failure" {
					return errors.New("response unavailable")
				}
				return nil
			})
			want := map[string][]string{
				"success":         {"publish", "ack", "delete"},
				"publish_failure": {"publish"},
				"ack_failure":     {"publish", "ack"},
				"delete_failure":  {"publish", "ack", "delete"},
			}[scenario]
			if !reflect.DeepEqual(client.order, want) {
				t.Fatalf("order = %v, want %v", client.order, want)
			}
			if (err == nil) != (scenario == "success") {
				t.Fatalf("result = %v", err)
			}
		})
	}
}

// TestClosurePendingFollowsOwnerSlot covers a failed delete and the final
// cleanup for owner and staff, without leaking to others.
func TestClosurePendingFollowsOwnerSlot(t *testing.T) {
	_, service, _ := setup(t)
	ctx := context.Background()
	client := &discordFake{}
	adapter := tickets.NewDiscordAdapter(service, client)
	owner := modules.Actor{GuildID: "guild-a", DiscordUserID: "member"}
	staff := modules.Actor{GuildID: "guild-a", DiscordUserID: "staff", CanModerate: true}
	ticket, err := adapter.Open(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	if pending, err := service.ClosurePending(ctx, owner, ticket.ID); err != nil || pending {
		t.Fatalf("open ticket pending = %v, %v", pending, err)
	}
	client.failDelete = 1
	if _, err := adapter.Close(ctx, owner, ticket.ID); err == nil {
		t.Fatal("expected the delete to fail")
	}
	for _, actor := range []modules.Actor{owner, staff} {
		if pending, err := service.ClosurePending(ctx, actor, ticket.ID); err != nil || !pending {
			t.Fatalf("lost cleanup state: %v, %v", pending, err)
		}
	}
	for _, actor := range []modules.Actor{{GuildID: "guild-a", DiscordUserID: "other"}, {GuildID: "other-guild", DiscordUserID: "member", CanModerate: true}} {
		if pending, err := service.ClosurePending(ctx, actor, ticket.ID); err == nil || pending {
			t.Fatalf("leaked cleanup state: %v, %v", pending, err)
		}
	}
	if _, err := adapter.Close(ctx, owner, ticket.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Open(ctx, owner); err != nil {
		t.Fatal(err)
	}
	for _, actor := range []modules.Actor{owner, staff} {
		if pending, err := service.ClosurePending(ctx, actor, ticket.ID); err != nil || pending {
			t.Fatalf("old ticket follows the newer slot: %v, %v", pending, err)
		}
	}
}
