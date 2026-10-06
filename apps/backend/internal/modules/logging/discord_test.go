package logging

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discord"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/quack"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// testGuild is guild "guild" with internal ID "internal".
var testGuild = &quack.Guild{ULIDModel: quack.ULIDModel{ID: "internal"}, DiscordGuildID: "guild", IsActive: true}

// guildStore knows only testGuild.
type guildStore struct{}

func (guildStore) GetGuildByID(_ context.Context, id string) (*quack.Guild, error) {
	if id == testGuild.ID {
		return testGuild, nil
	}
	return nil, nil
}

func (guildStore) GetGuildByDiscordID(_ context.Context, id string) (*quack.Guild, error) {
	if id == testGuild.DiscordGuildID {
		return testGuild, nil
	}
	return nil, nil
}

// fakeDiscord answers the REST calls delivery makes for guild "guild",
// bot "bot", and the private channel "log", and records what is posted.
type fakeDiscord struct {
	t *testing.T
	// everyone is the @everyone role's guild permissions.
	everyone int64
	// botAllow is Quack's overwrite in #log.
	botAllow int64

	mu   sync.Mutex
	sent []string
}

func (f *fakeDiscord) RoundTrip(r *http.Request) (*http.Response, error) {
	body := ""
	switch path := r.URL.Path; {
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/channels/log/messages"):
		f.mu.Lock()
		f.sent = append(f.sent, postedText(f.t, r))
		f.mu.Unlock()
		body = `{"id":"delivered","channel_id":"log"}`
	case strings.HasSuffix(path, "/channels/log"):
		body = fmt.Sprintf(`{"id":"log","guild_id":"guild","type":0,"permission_overwrites":[`+
			`{"id":"guild","type":0,"deny":"1024","allow":"0"},{"id":"bot","type":1,"allow":"%d","deny":"0"}]}`, f.botAllow)
	case strings.HasSuffix(path, "/guilds/guild/members/bot"):
		body = `{"user":{"id":"bot"},"roles":[]}`
	case strings.HasSuffix(path, "/guilds/guild"):
		body = fmt.Sprintf(`{"id":"guild","owner_id":"owner","roles":[{"id":"guild","permissions":"%d"}]}`, f.everyone)
	default:
		f.t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		return &http.Response{StatusCode: http.StatusNotFound, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
}

// postedText returns a posted message's content, or its attached full
// text when it had to be attached.
func postedText(t *testing.T, r *http.Request) string {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		defer r.MultipartForm.RemoveAll()
		files := r.MultipartForm.File["files[0]"]
		if len(files) != 1 {
			t.Fatal("long log was not attached")
		}
		f, err := files[0].Open()
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		data, err := io.ReadAll(f)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	var message discordgo.MessageSend
	if err := json.NewDecoder(r.Body).Decode(&message); err != nil {
		t.Fatal(err)
	}
	return message.Content
}

// newBot returns a bot whose REST calls go to fake.
func newBot(t *testing.T, fake *fakeDiscord) *discord.Bot {
	t.Helper()
	session, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.State.User = &discordgo.User{ID: "bot"}
	session.Client = &http.Client{Transport: fake}
	return &discord.Bot{Session: session}
}

func newTestRegistry(t *testing.T) *modules.Registry {
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

const writable = discordgo.PermissionViewChannel | discordgo.PermissionSendMessages | discordgo.PermissionReadMessageHistory

// TestLongLogDeliveryAttachesFullTextAndChecksAttachFiles posts a log too
// long for one message, which needs Attach Files, and refuses to post
// without it.
func TestLongLogDeliveryAttachesFullTextAndChecksAttachFiles(t *testing.T) {
	for _, allowFiles := range []bool{true, false} {
		t.Run(fmt.Sprint(allowFiles), func(t *testing.T) {
			fake := &fakeDiscord{t: t, botAllow: writable}
			if allowFiles {
				fake.botAllow |= discordgo.PermissionAttachFiles
			}
			client := delivery{bot: newBot(t, fake), guilds: modules.NewGuilds(guildStore{})}
			message := logMessage(entry{Type: MessageDelete, Before: strings.Repeat("message content ", 300) + "final-marker"})
			err := client.SendStaffLog(context.Background(), "internal", "log", message)
			if allowFiles != (err == nil) || len(fake.sent) != map[bool]int{true: 1, false: 0}[allowFiles] {
				t.Fatalf("sent=%d allowFiles=%v err=%v", len(fake.sent), allowFiles, err)
			}
			if allowFiles && !strings.Contains(fake.sent[0], "final-marker") {
				t.Fatal("long log lost its tail")
			}
		})
	}
}

func TestEnablementCheckRequiresAuditLogAndDeliverableChannels(t *testing.T) {
	routed, err := json.Marshal(Defaults().RouteAllTo("log"))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name               string
		everyone, botAllow int64
		config             string
		wantErr            error
		ok                 bool
	}{
		{"ready", discordgo.PermissionViewAuditLogs, writable | discordgo.PermissionAttachFiles, string(routed), nil, true},
		{"no audit log", 0, writable | discordgo.PermissionAttachFiles, string(routed), errAuditLogRequired, false},
		{"no attach files", discordgo.PermissionViewAuditLogs, writable, string(routed), nil, false},
		{"no routes", discordgo.PermissionViewAuditLogs, writable | discordgo.PermissionAttachFiles, `{}`, nil, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := &fakeDiscord{t: t, everyone: test.everyone, botAllow: test.botAllow}
			registry := newTestRegistry(t)
			New(registry, modules.NewGuilds(guildStore{}), newBot(t, fake))
			if _, err := registry.SetConfiguration(context.Background(), modules.Configuration{
				GuildID: testGuild.ID, ModuleID: modules.GeneralLogging, ConfigJSON: test.config,
			}); err != nil {
				t.Fatal(err)
			}
			err := registry.CheckModuleEnablement(context.Background(), testGuild, quack.ModuleStates{GeneralLogging: true})
			if test.ok != (err == nil) || (test.wantErr != nil && !errors.Is(err, test.wantErr)) {
				t.Fatalf("CheckModuleEnablement = %v", err)
			}
		})
	}
}

func TestSetupRoutesEverythingToTheChosenChannel(t *testing.T) {
	staff := &quack.GuildStaffContext{
		Guild: testGuild, ActorDiscordUserID: "admin",
		Permissions: map[quack.PermissionAction]bool{quack.PermissionActionGuildSettingsWrite: true},
	}
	request := discord.SetupRequest{Guild: staff, UserID: "admin", Options: []*discordgo.ApplicationCommandInteractionDataOption{
		{Name: "channel", Type: discordgo.ApplicationCommandOptionChannel, Value: "log"},
	}}

	fake := &fakeDiscord{t: t, everyone: discordgo.PermissionViewAuditLogs, botAllow: writable | discordgo.PermissionAttachFiles}
	registry := newTestRegistry(t)
	m := New(registry, modules.NewGuilds(guildStore{}), newBot(t, fake))
	message, err := m.Setup(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if message.Content != "{{quack:settings}} Discord logs will go to <#log>." || message.Ephemeral {
		t.Fatalf("confirmation = %+v", message)
	}
	settings, enabled, err := modules.LoadSettings(context.Background(), registry, testGuild.ID, modules.GeneralLogging, Defaults())
	if err != nil || !enabled || len(settings.Channels) != len(eventTypes) || !settings.IncludeMessageContent {
		t.Fatalf("saved %+v enabled=%v err=%v", settings, enabled, err)
	}

	fake.everyone = 0
	_, err = m.Setup(context.Background(), request)
	if text, ok := discord.UserMessage(err); !ok || !strings.Contains(text, "View Audit Log") {
		t.Fatalf("Setup without View Audit Log = %v", err)
	}
}
