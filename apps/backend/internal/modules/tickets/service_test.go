package tickets_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/tickets"
	"github.com/quackdiscord/bot/internal/testutil"
)

type auditRecorder struct {
	mu     sync.Mutex
	events []modules.AuditEvent
}

func (a *auditRecorder) RecordModuleAudit(_ context.Context, event modules.AuditEvent) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, event)
	return nil
}

var admin = modules.Actor{GuildID: "guild-a", DiscordUserID: "admin", CanManage: true}

// setup returns a service for guild-a with tickets on, and an adapter over
// it with a fake Discord.
func setup(t *testing.T) (*tickets.Service, *tickets.DiscordAdapter, *discordFake, *auditRecorder) {
	t.Helper()
	db := testutil.NewSQLiteDB(t)
	audit := &auditRecorder{}
	service := tickets.NewService(modules.NewRegistry(db), tickets.NewStore(db), audit)
	if _, err := service.UpdateSettings(context.Background(), admin, true, enabledSettings()); err != nil {
		t.Fatal(err)
	}
	client := &discordFake{}
	return service, tickets.NewDiscordAdapter(service, client), client, audit
}

func enabledSettings() tickets.Settings {
	settings := tickets.Defaults()
	settings.EntryChannelDiscordID = "entry"
	settings.StaffRoleDiscordIDs = []string{"staff-role"}
	return settings
}

func TestLifecyclePrivacyDuplicateRateAndIsolation(t *testing.T) {
	service, adapter, _, audit := setup(t)
	ctx := context.Background()
	member := modules.Actor{GuildID: "guild-a", DiscordUserID: "member"}
	staff := modules.Actor{GuildID: "guild-a", DiscordUserID: "staff", CanModerate: true}

	ticket, err := adapter.Open(ctx, member)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Open(ctx, member); !errors.Is(err, tickets.ErrDuplicateOpen) {
		t.Fatalf("second open: got %v, want ErrDuplicateOpen", err)
	}
	stranger := modules.Actor{GuildID: "guild-a", DiscordUserID: "other"}
	if _, _, err := service.Detail(ctx, stranger, ticket.ID); !errors.Is(err, tickets.ErrPermissionDenied) {
		t.Fatalf("stranger detail: got %v, want ErrPermissionDenied", err)
	}
	otherGuild := modules.Actor{GuildID: "guild-b", DiscordUserID: "staff", CanModerate: true}
	if _, _, err := service.Detail(ctx, otherGuild, ticket.ID); !errors.Is(err, tickets.ErrNotFound) {
		t.Fatalf("other guild detail: got %v, want ErrNotFound", err)
	}

	if err := service.Reply(ctx, member, ticket.ID, "private reply"); err != nil {
		t.Fatal(err)
	}
	resolved, err := service.Resolve(ctx, staff, ticket.ID, "private transcript")
	if err != nil || resolved.Status != tickets.StatusResolved {
		t.Fatalf("resolve = %+v, %v; want resolved", resolved, err)
	}
	transcript, err := service.Transcript(ctx, member, ticket.ID)
	if err != nil || transcript.Content != "private transcript" {
		t.Fatalf("transcript = %+v, %v; want the resolved transcript", transcript, err)
	}
	reopened, err := service.Reopen(ctx, staff, ticket.ID)
	if err != nil || reopened.Status != tickets.StatusOpen {
		t.Fatalf("reopen = %+v, %v; want open", reopened, err)
	}
	cancelled, err := service.Cancel(ctx, member, ticket.ID)
	if err != nil || cancelled.Status != tickets.StatusCancelled {
		t.Fatalf("cancel = %+v, %v; want cancelled", cancelled, err)
	}
	transcript, err = service.Transcript(ctx, member, ticket.ID)
	if err != nil || transcript.Content != "private transcript" {
		t.Fatalf("transcript after cancel = %+v, %v; want it kept", transcript, err)
	}

	// The member has used one of three daily opens.
	for i := range 2 {
		opened, err := adapter.Open(ctx, member)
		if err != nil {
			t.Fatalf("open %d: %v", i+2, err)
		}
		if _, err := service.Cancel(ctx, member, opened.ID); err != nil {
			t.Fatalf("cancel %d: %v", i+2, err)
		}
	}
	if _, err := adapter.Open(ctx, member); !errors.Is(err, tickets.ErrRateLimited) {
		t.Fatalf("fourth open: got %v, want ErrRateLimited", err)
	}
	if len(audit.events) < 5 {
		t.Fatalf("got %d audit events, want at least 5", len(audit.events))
	}
}

func TestEnabledTicketsRequireStaffRole(t *testing.T) {
	service, _, _, _ := setup(t)
	settings := enabledSettings()
	settings.StaffRoleDiscordIDs = nil
	if _, err := service.UpdateSettings(context.Background(), admin, true, settings); err == nil {
		t.Fatal("enabled tickets accepted no staff role")
	}
}

func TestPrivateThreadSettingDefaultsAndRoundTrips(t *testing.T) {
	if !tickets.Defaults().UsePrivateThreads {
		t.Fatal("new ticket settings should default to private threads")
	}
	service, _, _, _ := setup(t)
	for _, useThreads := range []bool{true, false} {
		settings := enabledSettings()
		settings.UsePrivateThreads = useThreads
		saved, err := service.UpdateSettings(context.Background(), admin, true, settings)
		if err != nil || saved.UsePrivateThreads != useThreads {
			t.Fatalf("save UsePrivateThreads=%v: got %+v, %v", useThreads, saved, err)
		}
		loaded, _, err := service.Settings(context.Background(), admin)
		if err != nil || loaded.UsePrivateThreads != useThreads {
			t.Fatalf("read UsePrivateThreads=%v: got %+v, %v", useThreads, loaded, err)
		}
	}
}

func TestDeletedEntryChannelDisablesTickets(t *testing.T) {
	service, _, _, _ := setup(t)
	ctx := context.Background()
	if err := service.RepairDeletedEntryChannel(ctx, "guild-a", "unrelated"); err != nil {
		t.Fatal(err)
	}
	if status, err := service.Status(ctx, admin); err != nil || !status.Enabled {
		t.Fatalf("status after unrelated deletion = %+v, %v; want enabled", status, err)
	}
	if err := service.RepairDeletedEntryChannel(ctx, "guild-a", "entry"); err != nil {
		t.Fatal(err)
	}
	if status, err := service.Status(ctx, admin); err != nil || status.Enabled || status.EntryConfigured {
		t.Fatalf("status after entry deletion = %+v, %v; want disabled with no entry", status, err)
	}
}
