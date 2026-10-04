package tickets_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/tickets"
)

// queueFake tells queue edits and sends apart, and fails them on request.
type queueFake struct {
	*discordFake
	sendErr, editErr error
	missingEdit      bool
	sends, edits     int
	sentTo           []string
}

func (f *queueFake) PublishQueue(_ context.Context, ticket *tickets.Ticket, settings tickets.Settings, _ *tickets.Transcript) (*tickets.QueueReceipt, error) {
	if ticket.LogMessageDiscordID != "" && ticket.LogChannelDiscordID == settings.QueueChannelDiscordID {
		f.edits++
		if f.missingEdit {
			return nil, tickets.ErrQueueMessageMissing
		}
		if f.editErr != nil {
			return nil, f.editErr
		}
		return &tickets.QueueReceipt{MessageID: ticket.LogMessageDiscordID, URL: "edited"}, nil
	}
	f.sends++
	f.sentTo = append(f.sentTo, settings.QueueChannelDiscordID)
	if f.sendErr != nil {
		return nil, f.sendErr
	}
	return &tickets.QueueReceipt{MessageID: fmt.Sprintf("queue-%d", f.sends), URL: "sent"}, nil
}

// TestOpenSurvivesQueueFailure opens the ticket and leaves repair alone
// when the queue post fails, then posts the closed entry fresh on close.
func TestOpenSurvivesQueueFailure(t *testing.T) {
	_, service, _ := setup(t)
	ctx := context.Background()
	client := &queueFake{discordFake: &discordFake{}, sendErr: errors.New("response lost")}
	adapter := tickets.NewDiscordAdapter(service, client)
	owner := modules.Actor{GuildID: "guild-a", DiscordUserID: "owner"}
	ticket, err := adapter.Open(ctx, owner)
	if err != nil || ticket == nil || ticket.LogMessageDiscordID != "" || client.permissionCalls != 1 {
		t.Fatalf("open = %+v, %v", ticket, err)
	}
	manager := modules.Actor{GuildID: "guild-a", DiscordUserID: "admin", CanManage: true, CanModerate: true}
	if err := adapter.RepairPermissions(ctx, manager, ticket.ID); err != nil || client.sends != 1 {
		t.Fatalf("repair = %v, sends = %d", err, client.sends)
	}
	client.sendErr = nil
	closed, err := adapter.Close(ctx, owner, ticket.ID)
	if err != nil || closed.LogMessageDiscordID != "queue-2" || closed.TranscriptURL != "sent" || client.edits != 0 {
		t.Fatalf("close = %+v, %v, edits = %d", closed, err, client.edits)
	}
}

// TestCloseEditsKnownPostOrPostsFresh edits the open post in place, and
// sends a fresh closed entry when that post is gone or the queue moved.
func TestCloseEditsKnownPostOrPostsFresh(t *testing.T) {
	for _, scenario := range []struct {
		name         string
		missing      bool
		moved        bool
		edits, sends int
		channel      string
	}{
		{"known", false, false, 1, 1, "queue"},
		{"missing", true, false, 1, 2, "queue"},
		{"moved", false, true, 0, 2, "new-queue"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			_, service, _ := setup(t)
			ctx := context.Background()
			client := &queueFake{discordFake: &discordFake{}}
			adapter := tickets.NewDiscordAdapter(service, client)
			actor := modules.Actor{GuildID: "guild-a", DiscordUserID: "owner", CanManage: true}
			ticket, err := adapter.Open(ctx, actor)
			if err != nil {
				t.Fatal(err)
			}
			if scenario.moved {
				settings := enabledSettings()
				settings.QueueChannelDiscordID = "new-queue"
				if _, err := service.UpdateSettings(ctx, actor, true, settings); err != nil {
					t.Fatal(err)
				}
			}
			client.missingEdit = scenario.missing
			closed, err := adapter.Close(ctx, actor, ticket.ID)
			if err != nil {
				t.Fatal(err)
			}
			if client.edits != scenario.edits || client.sends != scenario.sends ||
				closed.LogChannelDiscordID != scenario.channel || closed.TranscriptURL == "" {
				t.Fatalf("edits = %d, sends = %d, ticket = %+v", client.edits, client.sends, closed)
			}
		})
	}
}

// TestCloseIsNeverBlockedByQueue finishes the close, DM, and thread
// cleanup when the closed queue entry cannot be posted.
func TestCloseIsNeverBlockedByQueue(t *testing.T) {
	for _, failure := range []string{"edit", "send"} {
		t.Run(failure, func(t *testing.T) {
			_, service, _ := setup(t)
			ctx := context.Background()
			client := &queueFake{discordFake: &discordFake{}}
			adapter := tickets.NewDiscordAdapter(service, client)
			actor := modules.Actor{GuildID: "guild-a", DiscordUserID: "owner"}
			ticket, err := adapter.Open(ctx, actor)
			if err != nil {
				t.Fatal(err)
			}
			client.editErr = errors.New("discord unavailable")
			if failure == "send" {
				client.missingEdit, client.sendErr = true, errors.New("response lost")
			}
			closed, err := adapter.Close(ctx, actor, ticket.ID)
			if err != nil || closed.TranscriptURL != "" || !closed.CloseNoticeDelivered || len(client.deleted) != 1 {
				t.Fatalf("close = %+v, %v, deleted = %v", closed, err, client.deleted)
			}
			if pending, err := service.ClosurePending(ctx, actor, ticket.ID); err != nil || pending {
				t.Fatalf("slot still held: %v, %v", pending, err)
			}
		})
	}
}
