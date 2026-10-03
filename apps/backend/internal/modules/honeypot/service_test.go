package honeypot_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/honeypot"
	"github.com/quackdiscord/bot/internal/testutil"
	"gorm.io/gorm"
)

type validatorFake struct {
	mu                      sync.Mutex
	channelErr, templateErr error
	channels, templates     []string
}

func (f *validatorFake) ValidateHoneypotChannel(_ context.Context, guildID, channelID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.channels = append(f.channels, guildID+":"+channelID)
	return f.channelErr
}

func (f *validatorFake) ValidateHoneypotTemplate(_ context.Context, guildID, templateID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.templates = append(f.templates, guildID+":"+templateID)
	return f.templateErr
}

type applierFake struct {
	mu       sync.Mutex
	err      error
	requests []honeypot.ApplyRequest
}

func (f *applierFake) ApplyHoneypotCase(_ context.Context, request honeypot.ApplyRequest) (honeypot.ApplyResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, request)
	if f.err != nil {
		return honeypot.ApplyResult{}, f.err
	}
	return honeypot.ApplyResult{CaseID: fmt.Sprintf("case-%d", len(f.requests))}, nil
}

func (f *applierFake) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

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

type fixture struct {
	db        *gorm.DB
	registry  *modules.Registry
	service   *honeypot.Service
	validator *validatorFake
	applier   *applierFake
	audit     *auditRecorder
}

func setup(t *testing.T) *fixture {
	t.Helper()
	db := testutil.NewSQLiteDB(t)
	registry := modules.NewRegistry(db)
	validator := &validatorFake{}
	applier := &applierFake{}
	audit := &auditRecorder{}
	service := honeypot.NewService(registry, honeypot.NewStore(db), audit, validator, validator, applier)
	return &fixture{db: db, registry: registry, service: service, validator: validator, applier: applier, audit: audit}
}

func enable(t *testing.T, fixture *fixture, guildID string) modules.Actor {
	t.Helper()
	actor := modules.Actor{GuildID: guildID, DiscordUserID: "admin", CanManage: true}
	settings := honeypot.Settings{ChannelDiscordID: "trap", TemplateID: "01J500000000000000TEMPLATE", ExemptRoleDiscordIDs: []string{"trusted"}}
	if _, status, err := fixture.service.UpdateSettings(context.Background(), actor, true, settings); err != nil || !status.Enabled {
		t.Fatalf("enable: status=%+v err=%v", status, err)
	}
	return actor
}

func message(id string) honeypot.Message {
	return honeypot.Message{
		GuildID:             "guild-a",
		ChannelDiscordID:    "trap",
		MessageDiscordID:    id,
		AuthorDiscordUserID: "member",
		MessageURL:          "https://discord.com/channels/guild-a/trap/" + id,
	}
}

func TestNormalPathContractStatisticsAndAudit(t *testing.T) {
	fixture := setup(t)
	actor := enable(t, fixture, "guild-a")
	result, err := fixture.service.HandleMessage(context.Background(), message("message-1"))
	if err != nil {
		t.Fatal(err)
	}
	if result.CaseID == "" || fixture.applier.count() != 1 {
		t.Fatalf("result=%+v calls=%d", result, fixture.applier.count())
	}
	request := fixture.applier.requests[0]
	if request.Source != honeypot.SourceHoneypot || request.ActorType != honeypot.ActorTypeSystem ||
		request.ActorDiscordUserID != "" || request.TargetDiscordUserID != "member" {
		t.Fatalf("system attribution contract=%+v", request)
	}
	if request.IdempotencyKey != "honeypot:guild-a:message-1" || request.ContextMessageDiscordID != "message-1" || request.ContextURL == "" {
		t.Fatalf("normal-path context=%+v", request)
	}
	_, status, err := fixture.service.Settings(context.Background(), actor)
	if err != nil || status.Statistics.Total != 1 || status.Statistics.Created != 1 || status.Statistics.Failed != 0 {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	wantActions := map[string]bool{"honeypot.settings.update": false, "honeypot.trigger.detected": false, "honeypot.case.created": false}
	for _, event := range fixture.audit.events {
		if _, ok := wantActions[event.Action]; ok {
			wantActions[event.Action] = true
		}
	}
	for action, found := range wantActions {
		if !found {
			t.Errorf("missing audit %s: %+v", action, fixture.audit.events)
		}
	}
}

func TestTriggerExemptionsAndLoopPrevention(t *testing.T) {
	fixture := setup(t)
	actor := enable(t, fixture, "guild-a")
	cases := []struct {
		name   string
		mutate func(*honeypot.Message)
	}{
		{"quack", func(message *honeypot.Message) { message.IsQuack = true }},
		{"bot", func(message *honeypot.Message) { message.IsBot = true }},
		{"webhook", func(message *honeypot.Message) { message.IsWebhook = true }},
		{"staff", func(message *honeypot.Message) { message.AuthorCanModerate = true }},
		{"role", func(message *honeypot.Message) { message.AuthorRoleDiscordIDs = []string{"trusted"} }},
	}
	for index, testCase := range cases {
		event := message(fmt.Sprintf("exempt-%d", index))
		testCase.mutate(&event)
		if _, err := fixture.service.HandleMessage(context.Background(), event); !errors.Is(err, honeypot.ErrExempt) {
			t.Errorf("%s error=%v", testCase.name, err)
		}
	}
	offChannel := message("elsewhere")
	offChannel.ChannelDiscordID = "staff-log"
	if _, err := fixture.service.HandleMessage(context.Background(), offChannel); !errors.Is(err, honeypot.ErrNotTrigger) {
		t.Fatalf("off-channel error=%v", err)
	}
	if fixture.applier.count() != 0 {
		t.Fatalf("exemptions applied %d cases", fixture.applier.count())
	}
	_, status, err := fixture.service.Settings(context.Background(), actor)
	if err != nil || status.Statistics.Exempt != uint64(len(cases)) || status.Statistics.Total != uint64(len(cases)) {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	duplicate := message("exempt-0")
	duplicate.IsQuack = true
	if _, err := fixture.service.HandleMessage(context.Background(), duplicate); !errors.Is(err, honeypot.ErrDuplicate) {
		t.Fatalf("duplicate exemption error=%v", err)
	}
}

func TestConcurrentReplayCreatesExactlyOneCase(t *testing.T) {
	fixture := setup(t)
	enable(t, fixture, "guild-a")
	var wg sync.WaitGroup
	errorsSeen := make(chan error, 64)
	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := fixture.service.HandleMessage(context.Background(), message("same-message"))
			errorsSeen <- err
		}()
	}
	wg.Wait()
	close(errorsSeen)
	succeeded := 0
	for err := range errorsSeen {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, honeypot.ErrDuplicate):
		default:
			t.Errorf("unexpected replay error: %v", err)
		}
	}
	if succeeded != 1 || fixture.applier.count() != 1 {
		t.Fatalf("successes=%d apply calls=%d", succeeded, fixture.applier.count())
	}
}

func TestFailureAndDriftDisableSafelyAndRepair(t *testing.T) {
	fixture := setup(t)
	actor := enable(t, fixture, "guild-a")
	fixture.applier.err = errors.New("action path unavailable")
	if _, err := fixture.service.HandleMessage(context.Background(), message("failure")); err == nil {
		t.Fatal("expected case application failure")
	}
	_, status, err := fixture.service.Settings(context.Background(), actor)
	if err != nil || !status.Enabled || status.Statistics.Failed != 1 {
		t.Fatalf("ordinary failure changed enablement: status=%+v err=%v", status, err)
	}
	fixture.applier.err = nil
	fixture.validator.templateErr = errors.New("archived template")
	if _, err := fixture.service.HandleMessage(context.Background(), message("drift")); !errors.Is(err, honeypot.ErrTemplateUnavailable) {
		t.Fatalf("template drift error=%v", err)
	}
	_, status, err = fixture.service.Settings(context.Background(), actor)
	if err != nil || status.Enabled || status.DisabledReason == "" || status.Statistics.Failed != 2 {
		t.Fatalf("drift status=%+v err=%v", status, err)
	}
	fixture.validator.templateErr = nil
	if _, status, err = fixture.service.Repair(context.Background(), actor); err != nil || !status.Enabled || status.DisabledReason != "" {
		t.Fatalf("repair status=%+v err=%v", status, err)
	}
	if err := fixture.service.HandleDeletedChannel(context.Background(), "guild-a", "trap"); err != nil {
		t.Fatal(err)
	}
	_, status, _ = fixture.service.Settings(context.Background(), actor)
	if status.Enabled || status.DisabledReason == "" {
		t.Fatalf("deleted-channel status=%+v", status)
	}
}

func TestGuildAndModuleConfigurationIsolation(t *testing.T) {
	fixture := setup(t)
	enable(t, fixture, "guild-a")
	for _, configuration := range []modules.Configuration{
		{GuildID: "guild-a", ModuleID: modules.Tickets, Enabled: true, ConfigJSON: `{}`},
		{GuildID: "guild-a", ModuleID: modules.GeneralLogging, Enabled: true, ConfigJSON: `{}`},
		{GuildID: "guild-b", ModuleID: modules.Honeypots, Enabled: true, ConfigJSON: `{"channel_discord_id":"other","template_id":"other-template"}`},
	} {
		if _, err := fixture.registry.SetConfiguration(context.Background(), configuration); err != nil {
			t.Fatal(err)
		}
	}
	if err := fixture.service.HandleDeletedChannel(context.Background(), "guild-a", "trap"); err != nil {
		t.Fatal(err)
	}
	for _, moduleID := range []modules.ID{modules.Tickets, modules.GeneralLogging} {
		configuration, err := fixture.registry.Configuration(context.Background(), "guild-a", moduleID)
		if err != nil || configuration == nil || !configuration.Enabled {
			t.Fatalf("module %s contaminated: %+v err=%v", moduleID, configuration, err)
		}
	}
	other, err := fixture.registry.Configuration(context.Background(), "guild-b", modules.Honeypots)
	if err != nil || other == nil || !other.Enabled {
		t.Fatalf("other guild contaminated: %+v err=%v", other, err)
	}
}

func TestManagerPermissions(t *testing.T) {
	fixture := setup(t)
	ctx := context.Background()
	stranger := modules.Actor{GuildID: "guild-a"}
	if _, _, err := fixture.service.Settings(ctx, stranger); !errors.Is(err, honeypot.ErrPermissionDenied) {
		t.Fatalf("read without Manage Guild: got %v, want ErrPermissionDenied", err)
	}
	_, _, err := fixture.service.UpdateSettings(ctx, stranger, true, honeypot.Settings{})
	if !errors.Is(err, honeypot.ErrPermissionDenied) {
		t.Fatalf("write without Manage Guild: got %v, want ErrPermissionDenied", err)
	}
	fixture.validator.channelErr = errors.New("cannot observe channel")
	actor := modules.Actor{GuildID: "guild-a", DiscordUserID: "admin", CanManage: true}
	_, _, err = fixture.service.UpdateSettings(ctx, actor, true, honeypot.Settings{ChannelDiscordID: "trap", TemplateID: "template"})
	if !errors.Is(err, honeypot.ErrChannelUnavailable) {
		t.Fatalf("unobservable channel: got %v, want ErrChannelUnavailable", err)
	}
}

func TestContextCancellationDoesNotInventRetry(t *testing.T) {
	fixture := setup(t)
	enable(t, fixture, "guild-a")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := fixture.service.HandleMessage(ctx, message("cancelled"))
	if err == nil {
		t.Fatal("cancelled request unexpectedly succeeded")
	}
	time.Sleep(time.Millisecond)
	if fixture.applier.count() != 0 {
		t.Fatalf("cancelled application calls=%d", fixture.applier.count())
	}
}
