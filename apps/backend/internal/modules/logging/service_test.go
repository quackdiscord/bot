package logging_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/quackdiscord/bot/internal/modules"
	logmodule "github.com/quackdiscord/bot/internal/modules/logging"
	"github.com/quackdiscord/bot/internal/testutil"
)

type auditRecorder struct{ events []modules.AuditEvent }

func (a *auditRecorder) RecordModuleAudit(_ context.Context, event modules.AuditEvent) error {
	a.events = append(a.events, event)
	return nil
}

// deliveryFake fails its first failUntil sends and treats channels named
// public* as not staff-only.
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

func (f *deliveryFake) lastPayload() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.payloads[len(f.payloads)-1]
}

// setup returns a service for guild-a with logging on, message content
// included, and a two-message cache.
func setup(t *testing.T) (*logmodule.Service, *deliveryFake, *auditRecorder) {
	t.Helper()
	db := testutil.NewSQLiteDB(t)
	client := &deliveryFake{}
	audit := &auditRecorder{}
	service := logmodule.NewService(modules.NewRegistry(db), audit, client, logmodule.NewMessageCache(2))
	settings := logmodule.Defaults()
	settings.Channels = map[logmodule.EventType]string{
		logmodule.MessageDelete:     "staff-log",
		logmodule.MessageBulkDelete: "staff-log",
		logmodule.MemberJoin:        "staff-log",
	}
	settings.IncludeMessageContent = true
	settings.IncludeAttachmentMetadata = true
	settings.CacheEntriesPerGuild = 2
	admin := modules.Actor{GuildID: "guild-a", DiscordUserID: "admin", CanManage: true}
	if _, err := service.UpdateSettings(context.Background(), admin, true, settings); err != nil {
		t.Fatal(err)
	}
	return service, client, audit
}

func cache(t *testing.T, service *logmodule.Service, message logmodule.CachedMessage) {
	t.Helper()
	message.GuildID = "guild-a"
	if err := service.CacheMessage(context.Background(), message); err != nil {
		t.Fatal(err)
	}
}

func TestPrivacyRedactionRetryAndAuditIsolation(t *testing.T) {
	service, client, audit := setup(t)
	client.failUntil = 2
	cache(t, service, logmodule.CachedMessage{
		ChannelDiscordID: "source",
		MessageDiscordID: "message",
		Content:          "token=supersecretvalue",
		Attachments:      []logmodule.AttachmentMetadata{{Filename: "proof.png", Size: 5}},
	})
	err := service.Handle(context.Background(), logmodule.Event{
		GuildID:          "guild-a",
		Type:             logmodule.MessageDelete,
		MessageDiscordID: "message",
		Metadata:         map[string]string{"webhook": "https://discord.com/api/webhooks/123/secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.attempts != 3 {
		t.Fatalf("got %d attempts, want 3", client.attempts)
	}
	payload := client.lastPayload()
	if strings.Contains(payload, "supersecretvalue") || strings.Contains(payload, "/123/secret") ||
		!strings.Contains(payload, "REDACTED") {
		t.Fatalf("payload not redacted: %s", payload)
	}
	if status := service.Status("guild-a"); status.Delivered != 1 || status.Failed != 0 {
		t.Fatalf("status = %+v, want 1 delivered and 0 failed", status)
	}
	for _, event := range audit.events {
		if strings.Contains(event.Action, "delivery") || event.ResourceType == "audit_log" {
			t.Fatalf("a delivery reached the audit log: %+v", event)
		}
	}
}

func TestFailedDeleteRetainsCachedContextForGatewayReplay(t *testing.T) {
	service, client, _ := setup(t)
	client.failUntil = 10
	cache(t, service, logmodule.CachedMessage{MessageDiscordID: "replay", Content: "retained context"})
	event := logmodule.Event{GuildID: "guild-a", Type: logmodule.MessageDelete, MessageDiscordID: "replay"}
	if err := service.Handle(context.Background(), event); err == nil {
		t.Fatal("Handle succeeded past the retry limit")
	}
	client.failUntil = client.attempts
	if err := service.Handle(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if payload := client.lastPayload(); !strings.Contains(payload, "retained context") {
		t.Fatalf("replay lost the cached content: %s", payload)
	}
}

func TestBulkDeleteUsesAndThenEvictsCachedContext(t *testing.T) {
	service, client, _ := setup(t)
	for _, id := range []string{"one", "two"} {
		cache(t, service, logmodule.CachedMessage{MessageDiscordID: id, Content: "body-" + id})
	}
	ctx := context.Background()
	if err := service.HandleBulkDelete(ctx, "guild-a", "source", []string{"one", "two", "missing"}); err != nil {
		t.Fatal(err)
	}
	if payload := client.lastPayload(); !strings.Contains(payload, "body-one") || !strings.Contains(payload, "body-two") {
		t.Fatalf("bulk payload lacks cached content: %s", payload)
	}
	if err := service.HandleBulkDelete(ctx, "guild-a", "source", []string{"one", "two"}); err != nil {
		t.Fatal(err)
	}
	if payload := client.lastPayload(); strings.Contains(payload, "body-one") {
		t.Fatalf("bulk delete left messages cached: %s", payload)
	}
}

func TestCacheMessageLoadsPersistedLimit(t *testing.T) {
	service, _, _ := setup(t)
	for i := range 3 {
		cache(t, service, logmodule.CachedMessage{MessageDiscordID: fmt.Sprint(i)})
	}
	if status := service.Status("guild-a"); status.CachedMessages != 2 {
		t.Fatalf("got %d cached messages, want 2", status.CachedMessages)
	}
}

func TestRepairAndGuildModuleIsolation(t *testing.T) {
	service, _, _ := setup(t)
	ctx := context.Background()
	actor := modules.Actor{GuildID: "guild-a", DiscordUserID: "admin", CanManage: true}
	settings, enabled, err := service.RepairDeletedChannel(ctx, actor, "staff-log")
	if err != nil {
		t.Fatal(err)
	}
	if enabled || len(settings.Channels) != 0 {
		t.Fatalf("after repair: enabled %v, settings %+v; want off with no routes", enabled, settings)
	}
	err = service.Handle(ctx, logmodule.Event{GuildID: "guild-b", Type: logmodule.MemberJoin})
	if !errors.Is(err, logmodule.ErrDisabled) {
		t.Fatalf("other guild: got %v, want ErrDisabled", err)
	}
}
