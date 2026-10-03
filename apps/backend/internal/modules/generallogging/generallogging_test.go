package generallogging_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/quackdiscord/bot/internal/modules"
	logmodule "github.com/quackdiscord/bot/internal/modules/generallogging"
	"github.com/quackdiscord/bot/internal/testutil"
	"gorm.io/gorm"
)

type auditRecorder struct{ events []modules.AuditEvent }

func (a *auditRecorder) RecordModuleAudit(_ context.Context, event modules.AuditEvent) error {
	a.events = append(a.events, event)
	return nil
}

type deliveryFake struct {
	mu        sync.Mutex
	attempts  int
	failUntil int
	payloads  []string
}

func (f *deliveryFake) ValidateStaffOnlyChannel(_ context.Context, _ string, channelID string) error {
	if strings.HasPrefix(channelID, "public") {
		return errors.New("destination is not staff-only")
	}
	return nil
}
func (f *deliveryFake) SendStaffLog(_ context.Context, _, _, payload string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts++
	if f.attempts <= f.failUntil {
		return errors.New("temporary Discord failure")
	}
	f.payloads = append(f.payloads, payload)
	return nil
}

func setup(t *testing.T) (*gorm.DB, *logmodule.Service, *deliveryFake, *auditRecorder) {
	t.Helper()
	db := testutil.NewSQLiteDB(t)
	registry, err := modules.NewRegistry(modules.NewSQLSettingsStore(db), logmodule.Descriptor())
	if err != nil {
		t.Fatal(err)
	}
	client := &deliveryFake{}
	audit := &auditRecorder{}
	service := logmodule.NewService(registry, audit, client, logmodule.NewMessageCache(2))
	settings := logmodule.Defaults()
	settings.Channels = map[logmodule.EventType]string{logmodule.MessageDelete: "staff-log", logmodule.MessageBulkDelete: "staff-log", logmodule.MemberJoin: "staff-log"}
	settings.IncludeMessageContent = true
	settings.IncludeAttachmentMetadata = true
	settings.CacheEntriesPerGuild = 2
	if _, err := service.UpdateSettings(context.Background(), logmodule.Actor{GuildID: "guild-a", DiscordUserID: "admin", CanManage: true}, true, settings); err != nil {
		t.Fatal(err)
	}
	return db, service, client, audit
}

func TestPrivacyRedactionRetryAndAuditIsolation(t *testing.T) {
	_, service, client, audit := setup(t)
	client.failUntil = 2
	if err := service.CacheMessage(context.Background(), logmodule.CachedMessage{GuildID: "guild-a", ChannelDiscordID: "source", MessageDiscordID: "message", Content: "token=supersecretvalue", Attachments: []logmodule.AttachmentMetadata{{Filename: "proof.png", Size: 5}}}); err != nil {
		t.Fatal(err)
	}
	err := service.Handle(context.Background(), logmodule.Event{GuildID: "guild-a", Type: logmodule.MessageDelete, MessageDiscordID: "message", Metadata: map[string]string{"webhook": "https://discord.com/api/webhooks/123/secret"}})
	if err != nil {
		t.Fatal(err)
	}
	if client.attempts != 3 {
		t.Fatalf("attempts=%d", client.attempts)
	}
	payload := client.payloads[0]
	if strings.Contains(payload, "supersecretvalue") || strings.Contains(payload, "/123/secret") || !strings.Contains(payload, "REDACTED") {
		t.Fatalf("unredacted payload: %s", payload)
	}
	status := service.Status("guild-a")
	if status.Delivered != 1 || status.Failed != 0 {
		t.Fatalf("status=%+v", status)
	}
	for _, event := range audit.events {
		if strings.Contains(event.Action, "delivery") || event.ResourceType == "audit_log" {
			t.Fatalf("general events contaminated audit: %+v", event)
		}
	}
}

func TestFailedDeleteRetainsCachedContextForGatewayReplay(t *testing.T) {
	_, service, client, _ := setup(t)
	client.failUntil = 10
	if err := service.CacheMessage(context.Background(), logmodule.CachedMessage{GuildID: "guild-a", MessageDiscordID: "replay", Content: "retained context"}); err != nil {
		t.Fatal(err)
	}
	event := logmodule.Event{GuildID: "guild-a", Type: logmodule.MessageDelete, MessageDiscordID: "replay"}
	if err := service.Handle(context.Background(), event); err == nil {
		t.Fatal("expected bounded failure")
	}
	client.failUntil = client.attempts
	if err := service.Handle(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(client.payloads[len(client.payloads)-1], "retained context") {
		t.Fatalf("replay lost cache: %s", client.payloads[len(client.payloads)-1])
	}
}

func TestBulkDeleteUsesAndThenEvictsCachedContext(t *testing.T) {
	_, service, client, _ := setup(t)
	for _, id := range []string{"one", "two"} {
		if err := service.CacheMessage(context.Background(), logmodule.CachedMessage{GuildID: "guild-a", MessageDiscordID: id, Content: "body-" + id}); err != nil {
			t.Fatal(err)
		}
	}
	if err := service.HandleBulkDelete(context.Background(), "guild-a", "source", []string{"one", "two", "missing"}); err != nil {
		t.Fatal(err)
	}
	payload := client.payloads[len(client.payloads)-1]
	if !strings.Contains(payload, "body-one") || !strings.Contains(payload, "body-two") {
		t.Fatalf("bulk payload=%s", payload)
	}
	if err := service.HandleBulkDelete(context.Background(), "guild-a", "source", []string{"one", "two"}); err != nil {
		t.Fatal(err)
	}
	payload = client.payloads[len(client.payloads)-1]
	if strings.Contains(payload, "body-one") {
		t.Fatalf("bulk cache not evicted: %s", payload)
	}
}

func TestCacheMessageLoadsPersistedLimit(t *testing.T) {
	_, service, _, _ := setup(t)
	for i := 0; i < 3; i++ {
		if err := service.CacheMessage(context.Background(), logmodule.CachedMessage{GuildID: "guild-a", MessageDiscordID: fmt.Sprint(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if status := service.Status("guild-a"); status.CachedMessages != 2 {
		t.Fatalf("cached=%d", status.CachedMessages)
	}
}

func TestRepairAndGuildModuleIsolation(t *testing.T) {
	_, service, _, _ := setup(t)
	actor := logmodule.Actor{GuildID: "guild-a", DiscordUserID: "admin", CanManage: true}
	settings, enabled, err := service.RepairDeletedChannel(context.Background(), actor, "staff-log")
	if err != nil {
		t.Fatal(err)
	}
	if enabled || len(settings.Channels) != 0 {
		t.Fatalf("repair enabled=%v settings=%+v", enabled, settings)
	}
	if err := service.Handle(context.Background(), logmodule.Event{GuildID: "guild-b", Type: logmodule.MemberJoin}); !errors.Is(err, logmodule.ErrDisabled) {
		t.Fatalf("guild leak error=%v", err)
	}
}

func TestCacheConcurrentBoundedAndGuildScoped(t *testing.T) {
	cache := logmodule.NewMessageCache(10)
	cache.SetGuildLimit("a", 5)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cache.Put(logmodule.CachedMessage{GuildID: "a", MessageDiscordID: fmt.Sprint(i), Content: "a"})
			cache.Put(logmodule.CachedMessage{GuildID: "b", MessageDiscordID: fmt.Sprint(i), Content: "b"})
			cache.Get("a", fmt.Sprint(i))
		}(i)
	}
	wg.Wait()
	if cache.Len("a") != 5 || cache.Len("b") != 10 {
		t.Fatalf("lengths a=%d b=%d", cache.Len("a"), cache.Len("b"))
	}
}

func TestDeliveryQueueConcurrentBoundedLifecycle(t *testing.T) {
	_, service, _, _ := setup(t)
	queue := logmodule.NewDeliveryQueue(context.Background(), service, 8, 3)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := queue.Submit(logmodule.Event{GuildID: "guild-a", Type: logmodule.MemberJoin, ActorDiscordUserID: fmt.Sprint(i)})
			if err != nil && !errors.Is(err, logmodule.ErrQueueFull) {
				t.Errorf("submit: %v", err)
			}
		}(i)
	}
	wg.Wait()
	queue.Close()
	if err := queue.Submit(logmodule.Event{}); err == nil {
		t.Fatal("submit after close succeeded")
	}
}
