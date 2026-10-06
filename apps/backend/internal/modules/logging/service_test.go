package logging_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discord"
	"github.com/quackdiscord/bot/internal/modules"
	logmodule "github.com/quackdiscord/bot/internal/modules/logging"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// deliveryFake fails its first failUntil sends and treats channels named
// public* as not staff-only. It records each delivered post's text.
type deliveryFake struct {
	mu        sync.Mutex
	attempts  int
	failUntil int
	payloads  []string
	channels  []string
}

func (f *deliveryFake) ValidateStaffOnlyChannel(_ context.Context, _ string, channelID string) error {
	if strings.HasPrefix(channelID, "public") {
		return errors.New("destination is not staff-only")
	}
	return nil
}

func (f *deliveryFake) SendStaffLog(_ context.Context, _, channelID string, message discord.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts++
	if f.attempts <= f.failUntil {
		return errors.New("temporary Discord failure")
	}
	f.payloads = append(f.payloads, message.Content)
	f.channels = append(f.channels, channelID)
	return nil
}

func (f *deliveryFake) lastPayload() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.payloads[len(f.payloads)-1]
}

// newRegistry returns a registry over a private in-memory database holding
// only the module tables.
func newRegistry(t *testing.T) *modules.Registry {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(modules.Models()...); err != nil {
		t.Fatal(err)
	}
	return modules.NewRegistry(db)
}

var admin = modules.Actor{GuildID: "guild-a", DiscordUserID: "admin", CanManage: true}

// setup returns a service for guild-a with logging on, message content
// and files included, and a two-message cache.
func setup(t *testing.T) (*logmodule.Service, *deliveryFake) {
	t.Helper()
	client := &deliveryFake{}
	service := logmodule.NewService(newRegistry(t), client, logmodule.NewMessageCache(2))
	settings := logmodule.Defaults()
	settings.Channels = map[logmodule.EventType]string{
		logmodule.MessageEdit:       "staff-log",
		logmodule.MessageDelete:     "staff-log",
		logmodule.MessageBulkDelete: "staff-log",
		logmodule.MemberJoin:        "staff-log",
	}
	settings.IncludeMessageContent = true
	settings.IncludeAttachmentMetadata = true
	settings.CacheEntriesPerGuild = 2
	if _, err := service.UpdateSettings(context.Background(), admin, true, settings); err != nil {
		t.Fatal(err)
	}
	return service, client
}

func cache(t *testing.T, service *logmodule.Service, message logmodule.CachedMessage) {
	t.Helper()
	message.GuildID = "guild-a"
	if err := service.CacheMessage(context.Background(), message); err != nil {
		t.Fatal(err)
	}
}

func TestPrivacyRedactionAndRetry(t *testing.T) {
	service, client := setup(t)
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
	})
	if err != nil {
		t.Fatal(err)
	}
	if client.attempts != 3 {
		t.Fatalf("got %d attempts, want 3", client.attempts)
	}
	payload := client.lastPayload()
	if strings.Contains(payload, "supersecretvalue") || !strings.Contains(payload, "REDACTED") {
		t.Fatalf("payload not redacted: %s", payload)
	}
	if status := service.Status("guild-a"); status.Delivered != 1 || status.Failed != 0 {
		t.Fatalf("status = %+v, want 1 delivered and 0 failed", status)
	}
}

func TestBanReasonIsRedacted(t *testing.T) {
	service, client := setup(t)
	settings, _, _, err := service.Settings(context.Background(), admin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateSettings(context.Background(), admin, true, settings.RouteAllTo("staff-log")); err != nil {
		t.Fatal(err)
	}
	err = service.Handle(context.Background(), logmodule.Event{
		GuildID: "guild-a", Type: logmodule.DiscordBan, ActorDiscordUserID: "moderator",
		Metadata: map[string]string{"target_id": "member", "reason": "posted https://discord.com/api/webhooks/123/secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := client.lastPayload()
	if strings.Contains(payload, "/123/secret") || !strings.Contains(payload, "<@member> was banned by <@moderator>.") {
		t.Fatalf("ban log = %s", payload)
	}
}

// rateLimitedDelivery always answers with Discord's rate-limit error.
type rateLimitedDelivery struct {
	deliveryFake
	retryAfter time.Duration
}

func (f *rateLimitedDelivery) SendStaffLog(context.Context, string, string, discord.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts++
	return &discordgo.RateLimitError{RateLimit: &discordgo.RateLimit{
		TooManyRequests: &discordgo.TooManyRequests{RetryAfter: f.retryAfter},
	}}
}

// TestRateLimitDelaysRetry checks that Discord's retry-after outranks the
// short default backoff: with an hour to wait, the second attempt never
// happens before the context ends.
func TestRateLimitDelaysRetry(t *testing.T) {
	client := &rateLimitedDelivery{retryAfter: time.Hour}
	service := logmodule.NewService(newRegistry(t), client, nil)
	settings := logmodule.Defaults()
	settings.Channels = map[logmodule.EventType]string{logmodule.MemberJoin: "staff-log"}
	settings.MaxDeliveryAttempts = 2
	if _, err := service.UpdateSettings(context.Background(), admin, true, settings); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	err := service.Handle(ctx, logmodule.Event{GuildID: "guild-a", Type: logmodule.MemberJoin})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Handle = %v, want the wait cut short by the context", err)
	}
	if client.attempts != 1 {
		t.Fatalf("got %d attempts, want 1 before the retry-after elapsed", client.attempts)
	}
}

func TestFailedDeleteRetainsCachedContextForGatewayReplay(t *testing.T) {
	service, client := setup(t)
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
	service, client := setup(t)
	for _, id := range []string{"one", "two"} {
		cache(t, service, logmodule.CachedMessage{MessageDiscordID: id, Content: "body-" + id})
	}
	ctx := context.Background()
	if err := service.HandleBulkDelete(ctx, "guild-a", "source", []string{"one", "two", "missing"}); err != nil {
		t.Fatal(err)
	}
	payload := client.lastPayload()
	for _, want := range []string{"body-one", "body-two", "3 messages were deleted in <#source>.", "Messages with saved content: 2."} {
		if !strings.Contains(payload, want) {
			t.Fatalf("bulk log lacks %q: %s", want, payload)
		}
	}
	if err := service.HandleBulkDelete(ctx, "guild-a", "source", []string{"one", "two"}); err != nil {
		t.Fatal(err)
	}
	if payload := client.lastPayload(); strings.Contains(payload, "body-one") {
		t.Fatalf("bulk delete left messages cached: %s", payload)
	}
	tooMany := make([]string, 101)
	if err := service.HandleBulkDelete(ctx, "guild-a", "source", tooMany); err == nil {
		t.Fatal("bulk delete past Discord's limit was accepted")
	}
}

func TestCacheMessageLoadsPersistedLimit(t *testing.T) {
	service, _ := setup(t)
	for i := range 3 {
		cache(t, service, logmodule.CachedMessage{MessageDiscordID: fmt.Sprint(i)})
	}
	if status := service.Status("guild-a"); status.CachedMessages != 2 {
		t.Fatalf("got %d cached messages, want 2", status.CachedMessages)
	}
}

func TestRepairAndGuildModuleIsolation(t *testing.T) {
	service, _ := setup(t)
	ctx := context.Background()
	settings, enabled, err := service.RepairDeletedChannel(ctx, admin, "unrelated")
	if err != nil || !enabled || len(settings.Channels) != 4 {
		t.Fatalf("unrelated deletion: enabled %v, settings %+v, err %v; want untouched", enabled, settings, err)
	}
	settings, enabled, err = service.RepairDeletedChannel(ctx, admin, "staff-log")
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

// TestQueuedEditsKeepTheirOriginalSnapshots checks that two edits queued
// back to back each show their own before and after, not the newest cache.
func TestQueuedEditsKeepTheirOriginalSnapshots(t *testing.T) {
	service, client := setup(t)
	ctx := context.Background()
	current := logmodule.CachedMessage{GuildID: "guild-a", MessageDiscordID: "message", AuthorDiscordUserID: "author"}
	if err := service.CacheMessage(ctx, current); err != nil {
		t.Fatal(err)
	}
	current.Content = "first"
	first, err := service.PrepareMessageEdit(ctx, current, nil)
	if err != nil || first == nil {
		t.Fatalf("first edit: %v", err)
	}
	current.Content = "second"
	second, err := service.PrepareMessageEdit(ctx, current, nil)
	if err != nil || second == nil {
		t.Fatalf("second edit: %v", err)
	}
	unchanged, err := service.PrepareMessageEdit(ctx, current, nil)
	if err != nil || unchanged != nil {
		t.Fatalf("unchanged update generated log: %+v %v", unchanged, err)
	}
	for _, edit := range []*logmodule.Event{first, second} {
		if err := service.Handle(ctx, *edit); err != nil {
			t.Fatal(err)
		}
	}
	firstLog, secondLog := client.payloads[0], client.payloads[1]
	if !strings.Contains(firstLog, "A message from <@author> was edited.") ||
		!strings.Contains(firstLog, "Before: no text.") || !strings.Contains(firstLog, "After:\n> first") ||
		!strings.Contains(secondLog, "Before:\n> first") || !strings.Contains(secondLog, "After:\n> second") {
		t.Fatalf("queued edits read newer cache state: %q", client.payloads)
	}
}

func TestAttachmentOnlyEditsKeepPreviousFiles(t *testing.T) {
	service, _ := setup(t)
	ctx := context.Background()
	current := logmodule.CachedMessage{GuildID: "guild-a", MessageDiscordID: "files", Content: "same",
		Attachments: []logmodule.AttachmentMetadata{{DiscordID: "old", Filename: "proof.png"}}}
	if err := service.CacheMessage(ctx, current); err != nil {
		t.Fatal(err)
	}
	current.Attachments = []logmodule.AttachmentMetadata{{DiscordID: "replacement", Filename: "proof.png"}}
	replaced, err := service.PrepareMessageEdit(ctx, current, nil)
	if err != nil || replaced == nil || replaced.BeforeAttachments[0].DiscordID != "old" {
		t.Fatalf("same-name replacement missed: %+v %v", replaced, err)
	}
	current.Attachments = nil
	removed, err := service.PrepareMessageEdit(ctx, current, nil)
	if err != nil || removed == nil || len(removed.BeforeAttachments) != 1 || len(removed.Attachments) != 0 {
		t.Fatalf("file removal missed: %+v %v", removed, err)
	}
}

// TestSetupRouteDeliversMessageDetails checks the settings /setup logging
// saves: one channel for everything, with the message details shown.
func TestSetupRouteDeliversMessageDetails(t *testing.T) {
	service, client := setup(t)
	ctx := context.Background()
	settings, _, _, err := service.Settings(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateSettings(ctx, admin, true, settings.RouteAllTo("new-staff-log")); err != nil {
		t.Fatal(err)
	}
	saved, enabled, _, err := service.Settings(ctx, admin)
	if err != nil || !enabled || len(saved.Channels) != 9 {
		t.Fatalf("setup not persisted: %+v, %v", saved, err)
	}
	for _, kind := range []logmodule.EventType{logmodule.MessageDelete, logmodule.MessageBulkDelete} {
		cache(t, service, logmodule.CachedMessage{
			ChannelDiscordID: "source", MessageDiscordID: "cached", Content: "original content",
			Attachments: []logmodule.AttachmentMetadata{{Filename: "proof.png", ContentType: "image/png", Size: 42}},
			EmbedTypes:  []string{"image"},
		})
		if kind == logmodule.MessageBulkDelete {
			err = service.HandleBulkDelete(ctx, admin.GuildID, "source", []string{"cached", "uncached"})
		} else {
			err = service.Handle(ctx, logmodule.Event{GuildID: admin.GuildID, Type: kind, MessageDiscordID: "cached"})
		}
		if err != nil {
			t.Fatal(err)
		}
		payload := client.lastPayload()
		for _, want := range []string{"> original content", "Files: proof.png", "Included embeds: image."} {
			if !strings.Contains(payload, want) {
				t.Fatalf("%s lost %q: %s", kind, want, payload)
			}
		}
	}
	for _, channel := range client.channels {
		if channel != "new-staff-log" {
			t.Fatalf("old destination used: %s", channel)
		}
	}
}

// TestBulkDeleteKeepsPerMessageAttributionAndPrivacy checks that each
// message keeps its author and files, secrets are redacted, and content
// and files stay out when the guild did not opt in.
func TestBulkDeleteKeepsPerMessageAttributionAndPrivacy(t *testing.T) {
	for _, include := range []bool{true, false} {
		t.Run(fmt.Sprint(include), func(t *testing.T) {
			service, client := setup(t)
			settings, _, _, err := service.Settings(context.Background(), admin)
			if err != nil {
				t.Fatal(err)
			}
			settings.IncludeMessageContent, settings.IncludeAttachmentMetadata = include, include
			if _, err := service.UpdateSettings(context.Background(), admin, true, settings); err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{"one", "two"} {
				cache(t, service, logmodule.CachedMessage{
					MessageDiscordID: id, AuthorDiscordUserID: "author-" + id,
					Content:     "body-" + id + " token=secret-value",
					Attachments: []logmodule.AttachmentMetadata{{Filename: id + ".png"}},
				})
			}
			if err := service.HandleBulkDelete(context.Background(), "guild-a", "source", []string{"one", "two"}); err != nil {
				t.Fatal(err)
			}
			payload := client.lastPayload()
			if strings.Contains(payload, "secret-value") {
				t.Fatalf("unredacted: %s", payload)
			}
			for _, id := range []string{"one", "two"} {
				if !strings.Contains(payload, "<@author-"+id+"> · Message "+id) {
					t.Fatalf("message author lost: %s", payload)
				}
				hasDetail := strings.Contains(payload, "body-"+id) && strings.Contains(payload, "Files: "+id+".png")
				if include && !hasDetail {
					t.Fatalf("content or files lost: %s", payload)
				}
				if !include && (strings.Contains(payload, "body-") || strings.Contains(payload, ".png")) {
					t.Fatalf("bulk records bypassed privacy: %s", payload)
				}
			}
		})
	}
}

// TestSignedAttachmentURLRefreshIsNotAnEdit keeps the newest download link
// for a deletion log without taking URL rotation for a member's edit.
func TestSignedAttachmentURLRefreshIsNotAnEdit(t *testing.T) {
	service, client := setup(t)
	ctx := context.Background()
	before := logmodule.CachedMessage{GuildID: "guild-a", MessageDiscordID: "message", Content: "unchanged",
		Attachments: []logmodule.AttachmentMetadata{{DiscordID: "file", Filename: "proof.png", ContentType: "image/png", Size: 10,
			URL: "https://cdn.discordapp.com/proof.png?sig=old"}}}
	if err := service.CacheMessage(ctx, before); err != nil {
		t.Fatal(err)
	}
	current := before
	current.Attachments = append([]logmodule.AttachmentMetadata(nil), before.Attachments...)
	current.Attachments[0].URL = "https://cdn.discordapp.com/proof.png?sig=new"
	event, err := service.PrepareMessageEdit(ctx, current, nil)
	if err != nil || event != nil {
		t.Fatalf("URL rotation produced edit: %+v %v", event, err)
	}
	if err := service.Handle(ctx, logmodule.Event{GuildID: "guild-a", MessageDiscordID: "message", Type: logmodule.MessageDelete}); err != nil {
		t.Fatal(err)
	}
	if len(client.payloads) != 1 || !strings.Contains(client.payloads[0], "sig=new") || strings.Contains(client.payloads[0], "sig=old") {
		t.Fatalf("latest URL not cached: %q", client.payloads)
	}
}

// TestAttachmentChangesStillGenerateEdits checks that a file's identity,
// name, type, size, or removal each count as an edit.
func TestAttachmentChangesStillGenerateEdits(t *testing.T) {
	for _, field := range []string{"id", "name", "type", "size", "removed"} {
		t.Run(field, func(t *testing.T) {
			service, _ := setup(t)
			before := logmodule.CachedMessage{GuildID: "guild-a", MessageDiscordID: "message",
				Attachments: []logmodule.AttachmentMetadata{{DiscordID: "file", Filename: "proof.png", ContentType: "image/png", Size: 10,
					URL: "https://cdn.discordapp.com/old"}}}
			if err := service.CacheMessage(context.Background(), before); err != nil {
				t.Fatal(err)
			}
			current := before
			current.Attachments = append([]logmodule.AttachmentMetadata(nil), before.Attachments...)
			switch field {
			case "id":
				current.Attachments[0].DiscordID = "replacement"
			case "name":
				current.Attachments[0].Filename = "renamed.png"
			case "type":
				current.Attachments[0].ContentType = "image/jpeg"
			case "size":
				current.Attachments[0].Size++
			case "removed":
				current.Attachments = nil
			}
			event, err := service.PrepareMessageEdit(context.Background(), current, &before)
			if err != nil || event == nil {
				t.Fatalf("%s change missed: %+v %v", field, event, err)
			}
		})
	}
}

func TestRouteAllToMovesEveryEventTogether(t *testing.T) {
	settings := logmodule.Defaults()
	settings.Channels[logmodule.MessageDelete] = "old"
	settings.CacheEntriesPerGuild = 500
	settings.MaxDeliveryAttempts = 2
	settings = settings.RouteAllTo("staff")
	if len(settings.Channels) != 9 {
		t.Fatalf("routes = %v, want all nine events", settings.Channels)
	}
	for event, channel := range settings.Channels {
		if channel != "staff" {
			t.Fatalf("event %s kept another route", event)
		}
	}
	if !settings.IncludeMessageContent || !settings.IncludeAttachmentMetadata || !settings.IncludeEmbedMetadata ||
		settings.CacheEntriesPerGuild != 500 || settings.MaxDeliveryAttempts != 2 {
		t.Fatalf("incorrect setup settings: %+v", settings)
	}
}
