package honeypot_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/honeypot"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/store"
	"github.com/quackdiscord/bot/internal/testutil"
)

// moduleFixture is a honeypot module over a real store, with Discord
// behind session, if any.
type moduleFixture struct {
	store     *store.Store
	registry  *modules.Registry
	templates *templateServiceFake
	module    *honeypot.Module
}

func newModule(t *testing.T) moduleFixture {
	t.Helper()
	return newDiscordModule(t, nil)
}

func newDiscordModule(t *testing.T, session *discordgo.Session) moduleFixture {
	t.Helper()
	st := testutil.NewSQLiteStore(t)
	registry := modules.NewRegistry(st.DB())
	templates := &templateServiceFake{actions: []quack.ActionType{quack.ActionBanUser}}
	module := honeypot.New(st.DB(), registry, nil, modules.NewGuilds(st), session, st, nil, templates)
	return moduleFixture{store: st, registry: registry, templates: templates, module: module}
}

// templateServiceFake stands in for the core template service.
type templateServiceFake struct {
	actions   []quack.ActionType
	template  *quack.TemplateResponse
	ensureErr error
	ensured   int
}

func (f *templateServiceFake) UnattendedTemplateActions(context.Context, string, string) ([]quack.ActionType, error) {
	return f.actions, nil
}

func (f *templateServiceFake) EnsureHoneypotTemplate(context.Context, *quack.GuildStaffContext) (*quack.TemplateResponse, error) {
	f.ensured++
	return f.template, f.ensureErr
}

// roundTripper serves a fake Discord API.
type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// discordSession returns a session, logged in as "quack", whose REST calls
// go to serve. serve returns a status and a body: a string as is, anything
// else as JSON. A negative status fails the request without a response.
func discordSession(serve func(*http.Request) (int, any)) *discordgo.Session {
	session, _ := discordgo.New("Bot test")
	session.State.User = &discordgo.User{ID: "quack", Bot: true}
	session.Client = &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		status, body := serve(r)
		if status < 0 {
			return nil, errors.New("response lost")
		}
		raw, ok := body.(string)
		if !ok {
			encoded, _ := json.Marshal(body)
			raw = string(encoded)
		}
		return &http.Response{
			StatusCode: status,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(raw)),
			Request:    r,
		}, nil
	})}
	return session
}

// set stores an enabled honeypot configuration directly.
func (f moduleFixture) set(t *testing.T, guildID string, settings honeypot.Settings) {
	t.Helper()
	payload, _ := json.Marshal(settings)
	if _, err := f.registry.SetConfiguration(context.Background(), modules.Configuration{
		GuildID: guildID, ModuleID: modules.Honeypots, Enabled: true, ConfigJSON: string(payload),
	}); err != nil {
		t.Fatal(err)
	}
}

func (f moduleFixture) settings(t *testing.T, guildID string) (honeypot.Settings, bool) {
	t.Helper()
	configuration, err := f.registry.Configuration(context.Background(), guildID, modules.Honeypots)
	if err != nil || configuration == nil {
		t.Fatalf("configuration = %+v, %v", configuration, err)
	}
	var settings honeypot.Settings
	if err := json.Unmarshal([]byte(configuration.ConfigJSON), &settings); err != nil {
		t.Fatal(err)
	}
	return settings, configuration.Enabled
}

func (f moduleFixture) guild(t *testing.T, discordGuildID string) string {
	t.Helper()
	guild, err := f.store.UpsertGuild(context.Background(), quack.UpsertGuildParams{
		DiscordGuildID: discordGuildID, Name: discordGuildID, OwnerDiscordUserID: "owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	return guild.ID
}

func TestTemplateArchiveDisablesTheHoneypotUsingIt(t *testing.T) {
	ctx := context.Background()
	f := newModule(t)
	guildID := f.guild(t, "discord")
	template, err := f.store.CreateCaseTemplate(ctx, quack.CreateCaseTemplateParams{
		Template: quack.CaseTemplate{GuildID: guildID, Slug: "trap", Name: "Trap", ReasonTemplate: "Trap",
			CreatedByDiscordUserID: "admin", UpdatedByDiscordUserID: "admin"},
		Levels: []quack.ExpandedCaseTemplateLevel{{Level: quack.CaseTemplateLevel{Name: "Default", Position: 1, IsDefault: true}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	f.set(t, guildID, honeypot.Settings{ChannelDiscordID: "channel", TemplateID: template.Template.ID})
	f.module.HandleTemplateChange(ctx, guildID, template.Template.ID)
	if _, enabled := f.settings(t, guildID); !enabled {
		t.Fatal("a usable template change disabled the honeypot")
	}
	if _, err := f.store.ArchiveCaseTemplate(ctx, guildID, template.Template.ID, nil); err != nil {
		t.Fatal(err)
	}
	f.module.HandleTemplateChange(ctx, guildID, template.Template.ID)
	settings, enabled := f.settings(t, guildID)
	if enabled || settings.TemplateID != template.Template.ID || settings.ChannelDiscordID != "channel" || settings.DisabledReason == "" {
		t.Fatalf("archive left enabled=%v settings=%+v; want disabled with repair context kept", enabled, settings)
	}
}

func TestDeletionsDisableOnlyTheAffectedGuild(t *testing.T) {
	f := newModule(t)
	one, two := f.guild(t, "discord-1"), f.guild(t, "discord-2")
	f.set(t, one, honeypot.Settings{ChannelDiscordID: "channel-1", TemplateID: "template"})
	f.set(t, two, honeypot.Settings{ChannelDiscordID: "channel-2", TemplateID: "template"})
	check := func(when string) {
		t.Helper()
		if _, enabled := f.settings(t, one); enabled {
			t.Errorf("%s: guild one still enabled", when)
		}
		if _, enabled := f.settings(t, two); !enabled {
			t.Errorf("%s: guild two was disabled", when)
		}
	}
	honeypot.OnChannelDelete(f.module, nil, &discordgo.ChannelDelete{Channel: &discordgo.Channel{ID: "channel-1", GuildID: "discord-1"}})
	check("channel deleted")
	f.set(t, one, honeypot.Settings{ChannelDiscordID: "channel-1", TemplateID: "template"})
	honeypot.OnGuildDelete(f.module, nil, &discordgo.GuildDelete{Guild: &discordgo.Guild{ID: "discord-1"}})
	check("guild left")
}

func TestPoolDrainsOnStop(t *testing.T) {
	fixture := setup(t)
	enable(t, fixture, "guild-a")
	pool := honeypot.NewPool(fixture.service)
	pool.Start(context.Background())
	for index := range 100 {
		event := message(fmt.Sprintf("queued-%d", index))
		event.AuthorDiscordUserID = fmt.Sprintf("member-%d", index)
		if !pool.Submit(event) {
			t.Fatal("message was dropped")
		}
	}
	if err := pool.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := pool.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if pool.Submit(message("after-stop")) {
		t.Fatal("submit after stop succeeded")
	}
	if fixture.applier.count() != 100 {
		t.Fatalf("drain applied %d cases", fixture.applier.count())
	}
}
