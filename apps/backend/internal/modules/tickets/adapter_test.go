package tickets_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/tickets"
)

// discordFake is a DiscordClient that records calls and fails on request.
type discordFake struct {
	channelCalls    int
	accessCalls     int
	accessError     error
	replies         []string
	archiveAttempts int
	failArchives    int
	// channelGone makes transcript and archive calls fail as Discord does
	// for a deleted channel.
	channelGone bool
	// removed lists channels archived or deleted, in order.
	removed []string
}

func (f *discordFake) CreateChannel(context.Context, string, string, tickets.Settings) (string, error) {
	f.channelCalls++
	return fmt.Sprintf("private-thread-%d", f.channelCalls), nil
}

func (f *discordFake) EnsureAccess(context.Context, string, string, string, []string) error {
	f.accessCalls++
	return f.accessError
}

func (f *discordFake) SendReply(_ context.Context, _ string, body string) error {
	f.replies = append(f.replies, body)
	return nil
}

func (f *discordFake) CaptureTranscript(context.Context, string) (string, error) {
	if f.channelGone {
		return "", tickets.ErrChannelMissing
	}
	return "captured", nil
}

func (f *discordFake) ArchiveChannel(_ context.Context, id string) error {
	f.archiveAttempts++
	if f.channelGone {
		return tickets.ErrChannelMissing
	}
	if f.archiveAttempts <= f.failArchives {
		return errors.New("temporary archive failure")
	}
	f.removed = append(f.removed, id)
	return nil
}

func (f *discordFake) DeleteChannel(_ context.Context, id string) error {
	f.removed = append(f.removed, id)
	return nil
}

// TestOpeningLimitsPrecedeProvisioning covers repeated button clicks and
// failed private ACL setup without creating unbounded orphan channels.
func TestOpeningLimitsPrecedeProvisioning(t *testing.T) {
	_, adapter, client, _ := setup(t)
	ctx := context.Background()
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
		t.Fatalf("repeated opens created %d channels, want 1", client.channelCalls)
	}

	actor.DiscordUserID = "failed-member"
	client.accessError = errors.New("private ACL unavailable")
	for range 3 {
		if _, err := adapter.Open(ctx, actor); err == nil {
			t.Fatal("open succeeded without a private ACL")
		}
	}
	if _, err := adapter.Open(ctx, actor); !errors.Is(err, tickets.ErrRateLimited) {
		t.Fatalf("open after three failures: got %v, want ErrRateLimited", err)
	}
	if client.channelCalls != 4 || len(client.removed) != 3 {
		t.Fatalf("created %d channels and deleted %d, want 4 and 3", client.channelCalls, len(client.removed))
	}
}

// TestReplyToClosedTicketPostsNothing checks that a reply to a closed
// ticket is refused before anything reaches Discord.
func TestReplyToClosedTicketPostsNothing(t *testing.T) {
	_, adapter, client, _ := setup(t)
	ctx := context.Background()
	member := modules.Actor{GuildID: "guild-a", DiscordUserID: "member"}
	ticket, err := adapter.Open(ctx, member)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Cancel(ctx, member, ticket.ID); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Reply(ctx, member, ticket.ID, "too late"); !errors.Is(err, tickets.ErrInvalidTransition) {
		t.Fatalf("Reply = %v, want ErrInvalidTransition", err)
	}
	if len(client.replies) != 0 {
		t.Fatalf("replies posted to a closed ticket: %q", client.replies)
	}
}

// TestCloseWithDeletedChannel checks that a ticket whose channel was
// deleted can still be closed, so staff are not stuck with it.
func TestCloseWithDeletedChannel(t *testing.T) {
	_, adapter, client, _ := setup(t)
	ctx := context.Background()
	ticket, err := adapter.Open(ctx, modules.Actor{GuildID: "guild-a", DiscordUserID: "member"})
	if err != nil {
		t.Fatal(err)
	}
	client.channelGone = true
	staff := modules.Actor{GuildID: "guild-a", DiscordUserID: "staff", CanModerate: true}
	closed, err := adapter.Close(ctx, staff, ticket.ID)
	if err != nil || closed.Status != tickets.StatusResolved {
		t.Fatalf("Close = %+v, %v; want resolved", closed, err)
	}
}

func TestAdapterPrivateFlowAndRepair(t *testing.T) {
	service, adapter, client, _ := setup(t)
	ctx := context.Background()
	member := modules.Actor{GuildID: "guild-a", DiscordUserID: "member"}
	staff := modules.Actor{GuildID: "guild-a", DiscordUserID: "staff", CanModerate: true, CanManage: true}

	ticket, err := adapter.Open(ctx, member)
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.Reply(ctx, staff, ticket.ID, "staff reply"); err != nil {
		t.Fatal(err)
	}
	if err := adapter.RepairPermissions(ctx, staff, ticket.ID); err != nil {
		t.Fatal(err)
	}
	client.failArchives = 1
	if _, err := adapter.Close(ctx, staff, ticket.ID); err == nil {
		t.Fatal("Close hid the archive failure")
	}
	if _, err := adapter.Close(ctx, staff, ticket.ID); err != nil {
		t.Fatalf("retried Close: %v", err)
	}

	second, err := adapter.Open(ctx, member)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Cancel(ctx, member, second.ID); err != nil {
		t.Fatal(err)
	}
	if client.accessCalls != 3 || len(client.replies) != 1 || len(client.removed) != 2 || client.archiveAttempts != 3 {
		t.Fatalf("access calls %d, replies %d, archived %d, archive attempts %d; want 3, 1, 2, 3",
			client.accessCalls, len(client.replies), len(client.removed), client.archiveAttempts)
	}

	if err := service.RecordChannelMissing(ctx, "guild-a", ticket.ID, "private-thread-1"); err != nil {
		t.Fatal(err)
	}
	_, events, err := service.Detail(ctx, staff, ticket.ID)
	if err != nil {
		t.Fatal(err)
	}
	if last := events[len(events)-1]; last.Type != tickets.EventChannelMissing {
		t.Fatalf("last event = %s, want %s", last.Type, tickets.EventChannelMissing)
	}
}
