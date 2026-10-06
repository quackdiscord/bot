package discord_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discord"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/store"
	"github.com/quackdiscord/bot/internal/testutil"
)

// staffChannels is a StaffChannelValidator that fails with err.
type staffChannels struct{ err error }

func (v staffChannels) ValidateStaffChannel(context.Context, string, string) error { return v.err }

// setupHarness is /setup over SQLite, a fake Discord whose caller holds
// fixed live permissions, and the module registry.
type setupHarness struct {
	store    *store.Store
	registry *modules.Registry
	services *quack.Services
	guildID  string
}

func newSetupHarness(t *testing.T, liveBits uint64, channels staffChannels) *setupHarness {
	t.Helper()
	repository := testutil.NewSQLiteStore(t)
	if err := repository.Migrate(); err != nil {
		t.Fatal(err)
	}
	bootstrap, err := repository.BootstrapGuild(context.Background(), quack.BootstrapGuildParams{
		Starter: quack.StarterTemplate(), DiscordGuildID: "guild-1", Name: "Guild", OwnerDiscordUserID: "owner-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	registry := modules.NewRegistry(repository.DB())
	directory := &fakeDirectory{guildID: "guild-1", actorBits: liveBits}
	services := quack.New(quack.Deps{Store: repository, Guilds: directory, Channels: channels, Modules: registry})
	return &setupHarness{store: repository, registry: registry, services: services, guildID: bootstrap.Guild.ID}
}

// setupInteraction is /setup <subcommand> with options, from mod-1.
func setupInteraction(subcommand string, options ...*discordgo.ApplicationCommandInteractionDataOption) *discordgo.InteractionCreate {
	i := interaction(discordgo.InteractionApplicationCommand, uint64(discordgo.PermissionManageGuild), nil)
	i.Data = discordgo.ApplicationCommandInteractionData{Name: "setup", Options: []*discordgo.ApplicationCommandInteractionDataOption{{
		Name: subcommand, Type: discordgo.ApplicationCommandOptionSubCommand, Options: options,
	}}}
	return i
}

func option(name string, kind discordgo.ApplicationCommandOptionType, value any) *discordgo.ApplicationCommandInteractionDataOption {
	return &discordgo.ApplicationCommandInteractionDataOption{Name: name, Type: kind, Value: value}
}

// runSetup checks the public acknowledgement, runs the task, and returns
// the public result or, on failure, the private error.
func runSetup(t *testing.T, handler discord.Handler, i *discordgo.InteractionCreate) (string, bool) {
	t.Helper()
	result := handler(context.Background(), i)
	if result.Task == nil || result.Response.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource || result.Response.Data != nil {
		t.Fatalf("setup did not defer publicly: %+v", result.Response)
	}
	responder := &fakeResponder{}
	if err := result.Task(context.Background(), responder); err != nil {
		t.Fatal(err)
	}
	if responder.deleted {
		if responder.editCount != 0 || !responder.followup.Ephemeral {
			t.Fatalf("error was public: %+v", responder)
		}
		return responder.followup.Content, false
	}
	if responder.editCount != 1 || responder.edit.Content == nil {
		t.Fatalf("success was not one public response: %+v", responder)
	}
	return *responder.edit.Content, true
}

func TestSetupAuditUsesLiveAuthorityAndValidatedChannel(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		bits     uint64
		channels staffChannels
		want     string
	}{
		{"manager", uint64(discordgo.PermissionManageGuild), staffChannels{}, "Moderation history will go to <#123456789012345678>"},
		{"revoked", uint64(discordgo.PermissionModerateMembers), staffChannels{}, "You need Manage Server permission to change the audit channel."},
		{"inaccessible", uint64(discordgo.PermissionManageGuild), staffChannels{errors.New("public")}, "Choose a text channel"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			h := newSetupHarness(t, scenario.bits, scenario.channels)
			text, ok := runSetup(t, discord.NewSetup(h.services, nil),
				setupInteraction("audit", option("channel", discordgo.ApplicationCommandOptionChannel, "123456789012345678")))
			if ok != (scenario.name == "manager") || !strings.Contains(text, scenario.want) {
				t.Fatalf("feedback = %q, %t", text, ok)
			}
			settings, err := h.store.GetGuildSettings(context.Background(), h.guildID)
			if err != nil {
				t.Fatal(err)
			}
			if (settings.AuditMirrorChannelDiscordID != "") != ok {
				t.Fatalf("audit channel = %q", settings.AuditMirrorChannelDiscordID)
			}
		})
	}
}

func TestSetupAppealsSavesQueueRejoinAndReasonRule(t *testing.T) {
	h := newSetupHarness(t, uint64(discordgo.PermissionManageGuild), staffChannels{})
	handler := discord.NewSetup(h.services, nil)
	text, ok := runSetup(t, handler, setupInteraction("appeals",
		option("channel", discordgo.ApplicationCommandOptionChannel, "123456789012345678"),
		option("rejoin", discordgo.ApplicationCommandOptionString, "https://discord.com/invite/quack"),
		option("require-reason", discordgo.ApplicationCommandOptionBoolean, true),
	))
	if !ok || !strings.Contains(text, "Appeal reviews will go to <#123456789012345678>.") || !strings.Contains(text, "must write a decision reason") {
		t.Fatalf("feedback = %q", text)
	}
	settings, err := h.store.GetGuildSettings(context.Background(), h.guildID)
	if err != nil {
		t.Fatal(err)
	}
	if settings.AppealQueueChannelDiscordID != "123456789012345678" || settings.AppealRejoinURL != "https://discord.gg/quack" || !settings.AppealReviewReasonRequired {
		t.Fatalf("settings = %+v", settings)
	}

	text, ok = runSetup(t, handler, setupInteraction("appeals",
		option("channel", discordgo.ApplicationCommandOptionChannel, "123456789012345678"),
		option("rejoin", discordgo.ApplicationCommandOptionString, "none"),
	))
	if !ok || !strings.Contains(text, "must write") {
		t.Fatalf("an omitted require-reason changed the rule: %q", text)
	}
	if settings, _ = h.store.GetGuildSettings(context.Background(), h.guildID); settings.AppealRejoinURL != "" {
		t.Fatalf("none kept the rejoin link: %q", settings.AppealRejoinURL)
	}

	if text, ok = runSetup(t, handler, setupInteraction("appeals",
		option("channel", discordgo.ApplicationCommandOptionChannel, "123456789012345678"),
		option("rejoin", discordgo.ApplicationCommandOptionString, "http://example.com"),
	)); ok || !strings.Contains(text, "HTTPS Discord invite") {
		t.Fatalf("bad invite feedback = %q, %t", text, ok)
	}
}

func TestSetupToggleGoesThroughSettingsAndChecks(t *testing.T) {
	ids := map[string]modules.ID{"tickets": modules.Tickets, "honeypot": modules.Honeypots, "logging": modules.GeneralLogging}
	for command, id := range ids {
		for _, enabled := range []bool{false, true} {
			for _, scenario := range []string{"manager", "revoked", "check fails"} {
				if scenario == "check fails" && !enabled {
					continue
				}
				t.Run(fmt.Sprintf("%s/%t/%s", command, enabled, scenario), func(t *testing.T) {
					bits := uint64(discordgo.PermissionManageGuild)
					if scenario == "revoked" {
						bits = uint64(discordgo.PermissionModerateMembers)
					}
					h := newSetupHarness(t, bits, staffChannels{})
					ctx := context.Background()
					var checked []string
					for _, other := range ids {
						if _, err := h.registry.SetConfiguration(ctx, modules.Configuration{
							GuildID: h.guildID, ModuleID: other, Enabled: !enabled, ConfigJSON: `{"retained":true}`,
						}); err != nil {
							t.Fatal(err)
						}
						h.registry.SetEnablementCheck(other, func(_ context.Context, _ *quack.Guild, configJSON string) error {
							checked = append(checked, string(other)+":"+configJSON)
							if scenario == "check fails" {
								return errors.New("private Discord failure")
							}
							return nil
						})
					}
					text, ok := runSetup(t, discord.NewSetup(h.services, nil),
						setupInteraction(command, option("enabled", discordgo.ApplicationCommandOptionBoolean, enabled)))
					want := "turned on."
					switch {
					case scenario == "revoked":
						want = "Manage Server"
					case scenario == "check fails":
						want = "run `/setup " + command + "` without enabled"
					case !enabled:
						want = "turned off. Your channels are saved."
					}
					if ok != (scenario == "manager") || !strings.Contains(text, want) || strings.Contains(text, "private") {
						t.Fatalf("feedback = %q, %t", text, ok)
					}
					if wantCheck := enabled && scenario != "revoked"; (len(checked) == 1) != wantCheck ||
						wantCheck && checked[0] != string(id)+`:{"retained":true}` {
						t.Fatalf("checks = %v", checked)
					}
					for _, other := range ids {
						saved, err := h.registry.Configuration(ctx, h.guildID, other)
						wantEnabled := !enabled
						if other == id && scenario == "manager" {
							wantEnabled = enabled
						}
						if err != nil || saved.Enabled != wantEnabled || saved.ConfigJSON != `{"retained":true}` {
							t.Fatalf("%s = %+v, %v", other, saved, err)
						}
					}
				})
			}
		}
	}
}

func TestSetupDispatchesModulesAndRejectsMixedToggles(t *testing.T) {
	h := newSetupHarness(t, uint64(discordgo.PermissionManageGuild), staffChannels{})
	var requests []discord.SetupRequest
	module := func(_ context.Context, request discord.SetupRequest) (discord.Message, error) {
		requests = append(requests, request)
		if request.String("channel") == "fail" {
			return discord.Message{}, &discord.UserError{Message: "Quack needs Manage Messages."}
		}
		return discord.Signal("settings", "Ready.", false), nil
	}
	handler := discord.NewSetup(h.services, map[string]discord.SetupHandler{"honeypot": module})

	text, ok := runSetup(t, handler, setupInteraction("honeypot", option("channel", discordgo.ApplicationCommandOptionChannel, "trap")))
	if !ok || !strings.Contains(text, "Ready.") || len(requests) != 1 ||
		requests[0].Guild.Guild.ID != h.guildID || requests[0].UserID != "mod-1" || requests[0].String("channel") != "trap" {
		t.Fatalf("dispatch = %q %t %+v", text, ok, requests)
	}
	if text, ok = runSetup(t, handler, setupInteraction("honeypot", option("channel", discordgo.ApplicationCommandOptionChannel, "fail"))); ok || !strings.HasSuffix(text, "Quack needs Manage Messages.") {
		t.Fatalf("user error = %q, %t", text, ok)
	}

	mixed := handler(context.Background(), setupInteraction("honeypot",
		option("enabled", discordgo.ApplicationCommandOptionBoolean, false),
		option("warning", discordgo.ApplicationCommandOptionString, "Stay out"),
	))
	if mixed.Task != nil || mixed.Response.Data.Flags&discordgo.MessageFlagsEphemeral == 0 || !strings.Contains(mixed.Response.Data.Content, "on its own") {
		t.Fatalf("mixed options were not refused privately: %+v", mixed.Response)
	}
	missing := handler(context.Background(), setupInteraction("tickets"))
	if missing.Task != nil || !strings.Contains(missing.Response.Data.Content, "unavailable") {
		t.Fatalf("missing module = %+v", missing.Response)
	}
	if len(requests) != 2 {
		t.Fatalf("module ran for a refused request: %d", len(requests))
	}
}

// TestSetupLinksTheMatchingDashboardPage links core setup to the settings
// page and each module to its own page.
func TestSetupLinksTheMatchingDashboardPage(t *testing.T) {
	h := newSetupHarness(t, uint64(discordgo.PermissionManageGuild), staffChannels{})
	module := func(context.Context, discord.SetupRequest) (discord.Message, error) {
		return discord.Signal("settings", "Ready.", false), nil
	}
	handler := discord.NewSetupWithDashboard(h.services, map[string]discord.SetupHandler{"honeypot": module}, "http://localhost:3000")
	for _, test := range []struct {
		interaction *discordgo.InteractionCreate
		want        string
	}{
		{setupInteraction("audit", option("channel", discordgo.ApplicationCommandOptionChannel, "123456789012345678")), "http://localhost:3000/guilds/guild-1/settings"},
		{setupInteraction("honeypot", option("channel", discordgo.ApplicationCommandOptionChannel, "trap")), "http://localhost:3000/guilds/guild-1/modules/honeypot"},
		{setupInteraction("logging", option("enabled", discordgo.ApplicationCommandOptionBoolean, false)), "http://localhost:3000/guilds/guild-1/modules/logging"},
	} {
		result := handler(context.Background(), test.interaction)
		responder := &fakeResponder{}
		if err := result.Task(context.Background(), responder); err != nil {
			t.Fatal(err)
		}
		if responder.edit.Components == nil || len(*responder.edit.Components) != 1 {
			t.Fatalf("%s: no link: %+v", test.want, responder.edit)
		}
		button := (*responder.edit.Components)[0].(discordgo.ActionsRow).Components[0].(discordgo.Button)
		if button.URL != test.want || button.Label != "Open settings" {
			t.Fatalf("got %+v, want %s", button, test.want)
		}
	}
}
