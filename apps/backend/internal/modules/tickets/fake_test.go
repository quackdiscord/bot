package tickets_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

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

var admin = modules.Actor{GuildID: "guild-a", DiscordUserID: "admin", CanManage: true}

// setup returns a database and a service for guild-a with tickets on.
func setup(t *testing.T) (*gorm.DB, *tickets.Service, *auditRecorder) {
	t.Helper()
	db := testutil.NewSQLiteDB(t)
	audit := &auditRecorder{}
	service := tickets.NewService(modules.NewRegistry(db), tickets.NewStore(db), audit)
	if _, err := service.UpdateSettings(context.Background(), admin, true, enabledSettings()); err != nil {
		t.Fatal(err)
	}
	return db, service, audit
}

func enabledSettings() tickets.Settings {
	settings := tickets.Defaults()
	settings.EntryChannelDiscordID = "entry"
	settings.QueueChannelDiscordID = "queue"
	return settings
}

// discordFake is a DiscordClient that records calls and fails on request.
// It is safe for concurrent closes.
type discordFake struct {
	mu                  sync.Mutex
	channelCalls        int
	permissionCalls     int
	permissionError     error
	frozen              bool
	messages            []tickets.TranscriptMessage
	failPublish         bool
	transcriptPublishes int
	deleteAttempts      int
	failDelete          int
	deleted             []string
}

func (f *discordFake) CreateThread(context.Context, string, string, tickets.Settings) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.channelCalls++
	return fmt.Sprintf("private-thread-%d", f.channelCalls), nil
}

func (f *discordFake) EnsureAccess(context.Context, string, string, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.permissionCalls++
	return f.permissionError
}

func (f *discordFake) SendWelcome(context.Context, *tickets.Ticket) error { return nil }

func (f *discordFake) FreezeThread(context.Context, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.frozen = true
	return nil
}

// CaptureMessages returns the configured history, or one "captured"
// message, and refuses to run before the thread is frozen.
func (f *discordFake) CaptureMessages(context.Context, string) ([]tickets.TranscriptMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.frozen {
		return nil, errors.New("capture preceded thread freeze")
	}
	if f.messages != nil {
		return f.messages, nil
	}
	return []tickets.TranscriptMessage{{MessageID: "1", AuthorID: "member", Body: "captured"}}, nil
}

func (f *discordFake) DeleteThread(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteAttempts++
	if f.deleteAttempts <= f.failDelete {
		return errors.New("temporary delete failure")
	}
	f.deleted = append(f.deleted, id)
	return nil
}

func (f *discordFake) PublishQueue(_ context.Context, ticket *tickets.Ticket, _ tickets.Settings, transcript *tickets.Transcript) (*tickets.QueueReceipt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if transcript != nil {
		f.transcriptPublishes++
		if f.failPublish {
			return nil, errors.New("transcript upload failed")
		}
	}
	return &tickets.QueueReceipt{MessageID: "queue-" + ticket.ID, URL: "https://discord.com/channels/guild/queue/message"}, nil
}

// QueueMessageExists keeps saved receipts live.
func (f *discordFake) QueueMessageExists(context.Context, string, string) (bool, error) {
	return true, nil
}

// ValidateQueueMessage rejects adoption unless a fixture overrides it.
func (f *discordFake) ValidateQueueMessage(context.Context, *tickets.Ticket, string) (*tickets.QueueReceipt, error) {
	return nil, tickets.ErrInvalidQueueReceipt
}

// DeliverCloseNotice confirms the member's DM.
func (f *discordFake) DeliverCloseNotice(context.Context, *tickets.Ticket, *tickets.Transcript, bool) (string, error) {
	return "notice", nil
}

// transcriptOf returns a ticket's saved transcript text.
func transcriptOf(t *testing.T, service *tickets.Service, actor modules.Actor, ticketID string) string {
	t.Helper()
	transcript, err := service.Transcript(context.Background(), actor, ticketID)
	if err != nil {
		t.Fatalf("transcript: %v", err)
	}
	return transcript.Content
}
