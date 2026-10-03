package tickets_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discord"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/tickets"
	"github.com/quackdiscord/bot/internal/testutil"
	"gorm.io/gorm"
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

func setup(t *testing.T) (*gorm.DB, *tickets.Service, *auditRecorder) {
	t.Helper()
	db := testutil.NewSQLiteDB(t)
	registry, err := modules.NewRegistry(modules.NewSQLSettingsStore(db), tickets.Descriptor())
	if err != nil {
		t.Fatal(err)
	}
	audit := &auditRecorder{}
	service := tickets.NewService(registry, tickets.NewStore(db), audit)
	settings := tickets.Defaults()
	settings.EntryChannelDiscordID = "entry"
	settings.StaffRoleDiscordIDs = []string{"staff-role"}
	if _, err := service.UpdateSettings(context.Background(), tickets.Actor{GuildID: "guild-a", DiscordUserID: "admin", CanManage: true}, true, settings); err != nil {
		t.Fatal(err)
	}
	return db, service, audit
}

func TestLifecyclePrivacyDuplicateRateAndIsolation(t *testing.T) {
	_, service, audit := setup(t)
	ctx := context.Background()
	member := tickets.Actor{GuildID: "guild-a", DiscordUserID: "member"}
	staff := tickets.Actor{GuildID: "guild-a", DiscordUserID: "staff", CanModerate: true}
	ticket, err := service.Open(ctx, member, "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Open(ctx, member, "thread-2"); !errors.Is(err, tickets.ErrDuplicateOpen) {
		t.Fatalf("duplicate error=%v", err)
	}
	if _, _, err := service.Detail(ctx, tickets.Actor{GuildID: "guild-a", DiscordUserID: "other"}, ticket.ID); !errors.Is(err, tickets.ErrPermissionDenied) {
		t.Fatalf("privacy error=%v", err)
	}
	if _, _, err := service.Detail(ctx, tickets.Actor{GuildID: "guild-b", DiscordUserID: "staff", CanModerate: true}, ticket.ID); !errors.Is(err, tickets.ErrNotFound) {
		t.Fatalf("guild isolation error=%v", err)
	}
	if err := service.Reply(ctx, member, ticket.ID, "private reply"); err != nil {
		t.Fatal(err)
	}
	resolved, err := service.Resolve(ctx, staff, ticket.ID, "private transcript")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != tickets.StatusResolved {
		t.Fatalf("status=%s", resolved.Status)
	}
	transcript, err := service.Transcript(ctx, member, ticket.ID)
	if err != nil || transcript.Content != "private transcript" {
		t.Fatalf("transcript=%+v err=%v", transcript, err)
	}
	reopened, err := service.Reopen(ctx, staff, ticket.ID)
	if err != nil || reopened.Status != tickets.StatusOpen {
		t.Fatalf("reopen=%+v err=%v", reopened, err)
	}
	cancelled, err := service.Cancel(ctx, member, ticket.ID)
	if err != nil || cancelled.Status != tickets.StatusCancelled {
		t.Fatalf("cancel=%+v err=%v", cancelled, err)
	}
	transcript, err = service.Transcript(ctx, member, ticket.ID)
	if err != nil || transcript.Content != "private transcript" {
		t.Fatalf("cancel overwrote transcript=%+v err=%v", transcript, err)
	}
	for index := 2; index <= 3; index++ {
		opened, openErr := service.Open(ctx, member, fmt.Sprintf("thread-%d", index))
		if openErr != nil {
			t.Fatalf("open %d: %v", index, openErr)
		}
		if _, cancelErr := service.Cancel(ctx, member, opened.ID); cancelErr != nil {
			t.Fatalf("cancel %d: %v", index, cancelErr)
		}
	}
	if _, err := service.Open(ctx, member, "thread-4"); !errors.Is(err, tickets.ErrRateLimited) {
		t.Fatalf("rate error=%v", err)
	}
	if len(audit.events) < 5 {
		t.Fatalf("audit events=%d", len(audit.events))
	}
}

type discordFake struct {
	archived        []string
	replies         []string
	permissionCalls int
	channelCalls    int
	archiveAttempts int
	failArchive     int
	permissionError error
}

func (f *discordFake) CreatePrivateTicketChannel(context.Context, string, string, tickets.Settings) (string, error) {
	f.channelCalls++
	return fmt.Sprintf("private-thread-%d", f.channelCalls), nil
}
func (f *discordFake) EnsureTicketPermissions(context.Context, string, string, string, []string) error {
	f.permissionCalls++
	return f.permissionError
}

// TestDiscordOpeningLimitsPrecedeProvisioning covers repeated button clicks
// and failed private ACL setup without creating unbounded orphan channels.
func TestDiscordOpeningLimitsPrecedeProvisioning(t *testing.T) {
	_, service, _ := setup(t)
	client := &discordFake{}
	adapter := tickets.NewDiscordAdapter(service, client)
	actor := tickets.Actor{GuildID: "guild-a", DiscordUserID: "member"}
	if _, err := adapter.Open(context.Background(), actor); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		if _, err := adapter.Open(context.Background(), actor); !errors.Is(err, tickets.ErrDuplicateOpen) {
			t.Fatalf("duplicate opening: %v", err)
		}
	}
	if client.channelCalls != 1 {
		t.Fatalf("duplicate created %d channels", client.channelCalls)
	}
	actor.DiscordUserID = "failed-member"
	client.permissionError = errors.New("private ACL unavailable")
	for range 3 {
		if _, err := adapter.Open(context.Background(), actor); err == nil {
			t.Fatal("expected ACL rejection")
		}
	}
	if _, err := adapter.Open(context.Background(), actor); !errors.Is(err, tickets.ErrRateLimited) {
		t.Fatalf("failed provisioning bypassed daily allowance: %v", err)
	}
	if client.channelCalls != 4 || len(client.archived) != 3 {
		t.Fatalf("unexpected provisional cleanup: %+v", client)
	}
}
func (f *discordFake) SendTicketReply(_ context.Context, _ string, body string) error {
	f.replies = append(f.replies, body)
	return nil
}
func (f *discordFake) CaptureTicketTranscript(context.Context, string) (string, error) {
	return "captured", nil
}
func (f *discordFake) DeleteProvisionalTicketChannel(_ context.Context, id string) error {
	f.archived = append(f.archived, id)
	return nil
}
func (f *discordFake) ArchiveTicketChannel(_ context.Context, id string) error {
	f.archiveAttempts++
	if f.archiveAttempts <= f.failArchive {
		return errors.New("temporary archive failure")
	}
	f.archived = append(f.archived, id)
	return nil
}

func TestDiscordAdapterPrivateFlowAndRepair(t *testing.T) {
	_, service, _ := setup(t)
	client := &discordFake{}
	adapter := tickets.NewDiscordAdapter(service, client)
	member := tickets.Actor{GuildID: "guild-a", DiscordUserID: "member"}
	ticket, err := adapter.Open(context.Background(), member)
	if err != nil {
		t.Fatal(err)
	}
	staff := tickets.Actor{GuildID: "guild-a", DiscordUserID: "staff", CanModerate: true, CanManage: true}
	if err := adapter.Reply(context.Background(), staff, ticket.ID, "staff reply"); err != nil {
		t.Fatal(err)
	}
	if err := adapter.RepairPermissions(context.Background(), staff, ticket.ID); err != nil {
		t.Fatal(err)
	}
	client.failArchive = 1
	if _, err := adapter.Close(context.Background(), staff, ticket.ID); err == nil {
		t.Fatal("expected first archive failure")
	}
	if _, err := adapter.Close(context.Background(), staff, ticket.ID); err != nil {
		t.Fatalf("retry close: %v", err)
	}
	second, err := adapter.Open(context.Background(), member)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Cancel(context.Background(), member, second.ID); err != nil {
		t.Fatal(err)
	}
	if client.permissionCalls != 3 || len(client.replies) != 1 || len(client.archived) != 2 || client.archiveAttempts != 3 {
		t.Fatalf("client=%+v", client)
	}
	if err := adapter.HandleDeletedChannel(context.Background(), "guild-a", ticket.ID, "private-thread"); err != nil {
		t.Fatal(err)
	}
	if err := adapter.HandleDeletedEntryChannel(context.Background(), "guild-a", "entry"); err != nil {
		t.Fatal(err)
	}
	status, err := service.Status(context.Background(), staff)
	if err != nil || status.Enabled || status.EntryConfigured {
		t.Fatalf("entry repair status=%+v err=%v", status, err)
	}
}

func TestEnabledTicketsRequireStaffRole(t *testing.T) {
	_, service, _ := setup(t)
	settings := tickets.Defaults()
	settings.EntryChannelDiscordID = "entry"
	_, err := service.UpdateSettings(context.Background(), tickets.Actor{GuildID: "guild-a", DiscordUserID: "admin", CanManage: true}, true, settings)
	if err == nil {
		t.Fatal("enabled tickets accepted no staff role")
	}
}

// routeRecorder records the routes a registrar installs.
type routeRecorder map[string]bool

func (r routeRecorder) HandleComponent(namespace, action string, _ discord.Handler) {
	r[namespace+":"+action] = true
}

func TestComponentRegistrarAndControls(t *testing.T) {
	routes := routeRecorder{}
	handler := func(context.Context, *discordgo.InteractionCreate) discord.Result {
		return discord.Immediate(discord.Error("ok"))
	}
	if err := tickets.RegisterComponents(routes, tickets.ComponentHandlers{Open: handler, Queue: handler, View: handler}); err == nil || len(routes) != 0 {
		t.Fatalf("incomplete handlers were registered: %v", routes)
	}
	if err := tickets.RegisterComponents(routes, tickets.ComponentHandlers{Open: handler, Queue: handler, View: handler, Reply: handler, Close: handler}); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"open", "queue", "view", "reply", "close"} {
		if !routes["ticket:"+action] {
			t.Fatalf("action %s not routed", action)
		}
	}
	if len(tickets.EntryComponents()) != 1 || len(tickets.TicketComponents("ticket-id")) != 1 {
		t.Fatal("missing ticket controls")
	}
}

func TestPrivateThreadSettingDefaultsAndRoundTrips(t *testing.T) {
	if !tickets.Defaults().UsePrivateThreads {
		t.Fatal("new ticket settings should default to private threads")
	}
	_, service, _ := setup(t)
	actor := tickets.Actor{GuildID: "guild-a", DiscordUserID: "admin", CanManage: true}
	for _, useThreads := range []bool{true, false} {
		settings := tickets.Defaults()
		settings.EntryChannelDiscordID = "entry"
		settings.StaffRoleDiscordIDs = []string{"staff-role"}
		settings.UsePrivateThreads = useThreads
		saved, err := service.UpdateSettings(context.Background(), actor, true, settings)
		if err != nil || saved.UsePrivateThreads != useThreads {
			t.Fatalf("save thread setting: %+v %v", saved, err)
		}
		loaded, _, err := service.Settings(context.Background(), actor)
		if err != nil || loaded.UsePrivateThreads != useThreads {
			t.Fatalf("read thread setting: %+v %v", loaded, err)
		}
	}
}
