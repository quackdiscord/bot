package honeypot_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/honeypot"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/store"
	"github.com/quackdiscord/bot/internal/testutil"
)

// moduleFixture is a honeypot module over a real store, with no Discord.
type moduleFixture struct {
	store    *store.Store
	registry *modules.Registry
	module   *honeypot.Module
}

func newModule(t *testing.T) moduleFixture {
	t.Helper()
	st := testutil.NewSQLiteStore(t)
	registry := modules.NewRegistry(st.DB())
	module := honeypot.New(st.DB(), registry, nil, modules.NewGuilds(st), nil, st, nil)
	return moduleFixture{store: st, registry: registry, module: module}
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
	guild, err := f.store.UpsertGuild(context.Background(), quack.UpsertGuildParams{DiscordGuildID: discordGuildID, Name: discordGuildID, OwnerDiscordUserID: "owner"})
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
