package honeypot_test

import (
	"context"
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
	"github.com/quackdiscord/bot/internal/modules/honeypot"
	"github.com/quackdiscord/bot/internal/quack"
)

// allChannelPermissions is everything Quack needs in the trap channel.
const allChannelPermissions = discordgo.PermissionViewChannel | discordgo.PermissionSendMessages |
	discordgo.PermissionReadMessageHistory | discordgo.PermissionManageMessages

// trapDiscord is a fake Discord guild "guild" with text channel #trap, where
// Quack's channel permissions lack deny.
type trapDiscord struct {
	mu          sync.Mutex
	deny        int64
	channelType discordgo.ChannelType
	posts       []string
	edits       []string
	editStatus  int
}

func (d *trapDiscord) serve(r *http.Request) (int, any) {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch path := r.URL.Path; {
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/channels/trap"):
		return 200, fmt.Sprintf(`{"id":"trap","guild_id":"guild","type":%d,"permission_overwrites":[{"id":"quack","type":1,"deny":"%d","allow":"0"}]}`,
			d.channelType, d.deny)
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/guilds/guild"):
		return 200, fmt.Sprintf(`{"id":"guild","owner_id":"owner","roles":[{"id":"guild","permissions":"%d"}]}`, allChannelPermissions)
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/guilds/guild/members/quack"):
		return 200, `{"user":{"id":"quack","bot":true},"roles":[]}`
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/channels/trap/messages"):
		body, _ := io.ReadAll(r.Body)
		d.posts = append(d.posts, string(body))
		return 200, fmt.Sprintf(`{"id":"warning-%d","channel_id":"trap"}`, len(d.posts))
	case r.Method == http.MethodPatch && strings.Contains(path, "/channels/trap/messages/"):
		body, _ := io.ReadAll(r.Body)
		d.edits = append(d.edits, string(body))
		if d.editStatus == 404 {
			return 404, `{"code":10008,"message":"Unknown Message"}`
		}
		return 200, `{"id":"edited"}`
	}
	return 404, `{"code":0,"message":"unexpected"}`
}

// setupFixture is a module over a fake trap guild with a usable honeypot
// template the core would create.
func setupFixture(t *testing.T) (moduleFixture, *trapDiscord, *quack.GuildStaffContext) {
	t.Helper()
	discord := &trapDiscord{}
	f := newDiscordModule(t, discordSession(discord.serve))
	guildID := f.guild(t, "guild")
	template, err := f.store.CreateCaseTemplate(context.Background(), quack.CreateCaseTemplateParams{
		Template: quack.CaseTemplate{GuildID: guildID, Slug: "honeypot", Name: "Honeypot", ReasonTemplate: "Trap",
			CreatedByDiscordUserID: "admin", UpdatedByDiscordUserID: "admin"},
		Levels: []quack.ExpandedCaseTemplateLevel{{
			Level:   quack.CaseTemplateLevel{Name: "Default", Position: 1, IsDefault: true},
			Actions: []quack.CaseTemplateLevelAction{{ActionType: quack.ActionBanUser}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	f.templates.template = &quack.TemplateResponse{ID: template.Template.ID}
	staff := &quack.GuildStaffContext{
		Guild:              &quack.Guild{DiscordGuildID: "guild"},
		ActorDiscordUserID: "admin",
		Permissions:        map[quack.PermissionAction]bool{quack.PermissionActionGuildSettingsWrite: true},
	}
	staff.Guild.ID = guildID
	return f, discord, staff
}

func setupRequest(staff *quack.GuildStaffContext, options map[string]string) discord.SetupRequest {
	request := discord.SetupRequest{Guild: staff, UserID: "admin"}
	for name, value := range options {
		request.Options = append(request.Options, &discordgo.ApplicationCommandInteractionDataOption{Name: name, Value: value})
	}
	return request
}

func TestSetupCreatesTemplatePostsWarningAndEnables(t *testing.T) {
	f, fake, staff := setupFixture(t)
	ctx := context.Background()
	reply, err := f.module.Setup(ctx, setupRequest(staff, map[string]string{"channel": "trap", "warning": `# Stay out\nBots only.`}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reply.Content, "Honeypot ready in <#trap>. Staff and Quack can post safely.") || reply.Ephemeral {
		t.Fatalf("reply = %+v", reply)
	}
	settings, enabled := f.settings(t, staff.Guild.ID)
	if !enabled || settings.ChannelDiscordID != "trap" || settings.TemplateID != f.templates.template.ID ||
		settings.WarningMessageID != "warning-1" || settings.WarningText != "# Stay out\nBots only." {
		t.Fatalf("enabled=%v settings=%+v", enabled, settings)
	}
	if len(fake.posts) != 1 || !strings.Contains(fake.posts[0], `# Stay out\nBots only.\n\n-# 0 incidents caught.`) {
		t.Fatalf("posts = %q", fake.posts)
	}
	if f.templates.ensured != 1 {
		t.Fatalf("EnsureHoneypotTemplate called %d times", f.templates.ensured)
	}

	// Running setup again edits the same warning and keeps the template.
	if _, err := f.module.Setup(ctx, setupRequest(staff, nil)); err != nil {
		t.Fatal(err)
	}
	if len(fake.posts) != 1 || len(fake.edits) != 1 || f.templates.ensured != 1 {
		t.Fatalf("rerun posts=%d edits=%d ensured=%d", len(fake.posts), len(fake.edits), f.templates.ensured)
	}

	// A deleted warning is posted again.
	fake.editStatus = 404
	if _, err := f.module.Setup(ctx, setupRequest(staff, nil)); err != nil {
		t.Fatal(err)
	}
	if settings, _ := f.settings(t, staff.Guild.ID); len(fake.posts) != 2 || settings.WarningMessageID != "warning-2" {
		t.Fatalf("repost posts=%d settings=%+v", len(fake.posts), settings)
	}
}

func TestSetupFailuresAreUserErrors(t *testing.T) {
	for _, test := range []struct {
		name    string
		prepare func(moduleFixture, *trapDiscord)
		want    string
	}{
		{"template", func(f moduleFixture, _ *trapDiscord) { f.templates.ensureErr = errors.New("archived") },
			"Could not create the honeypot template. If it is archived, restore it first."},
		{"voice channel", func(_ moduleFixture, d *trapDiscord) { d.channelType = discordgo.ChannelTypeGuildVoice },
			"The honeypot must be a text channel in this server."},
		{"permissions", func(_ moduleFixture, d *trapDiscord) { d.deny = discordgo.PermissionManageMessages },
			"Quack needs View Channel, Send Messages, Read Message History and Manage Messages in the honeypot channel. Update its permissions and run setup again."},
	} {
		t.Run(test.name, func(t *testing.T) {
			f, fake, staff := setupFixture(t)
			test.prepare(f, fake)
			_, err := f.module.Setup(context.Background(), setupRequest(staff, map[string]string{"channel": "trap"}))
			text, ok := discord.UserMessage(err)
			if !ok || text != test.want {
				t.Fatalf("err = %v, want %q", err, test.want)
			}
			if c, err := f.registry.Configuration(context.Background(), staff.Guild.ID, modules.Honeypots); err != nil || c != nil && c.Enabled {
				t.Fatalf("a failed setup left the honeypot on: %+v %v", c, err)
			}
			if len(fake.posts) != 0 {
				t.Fatal("a failed setup posted a warning")
			}
		})
	}
}

// The enablement check runs /setup's live checks on the saved settings.
func TestEnablementCheckValidatesSavedSetup(t *testing.T) {
	f, fake, staff := setupFixture(t)
	ctx := context.Background()
	guild := staff.Guild
	if err := honeypot.CheckEnablement(f.module, ctx, guild, `{}`); err == nil {
		t.Fatal("accepted settings without a channel")
	}
	valid := fmt.Sprintf(`{"channel_discord_id":"trap","template_id":%q}`, f.templates.template.ID)
	if err := honeypot.CheckEnablement(f.module, ctx, guild, valid); err != nil {
		t.Fatal(err)
	}
	fake.deny = discordgo.PermissionReadMessageHistory
	if err := honeypot.CheckEnablement(f.module, ctx, guild, valid); !errors.Is(err, honeypot.ErrChannelUnavailable) {
		t.Fatalf("missing permission: err = %v", err)
	}
	fake.deny = 0
	if err := honeypot.CheckEnablement(f.module, ctx, guild, `{"channel_discord_id":"trap","template_id":"missing"}`); !errors.Is(err, honeypot.ErrTemplateUnavailable) {
		t.Fatalf("missing template: err = %v", err)
	}
	if err := f.registry.CheckModuleEnablement(ctx, guild, quack.ModuleStates{Honeypot: true}); err == nil {
		t.Fatal("the registry did not run the check for a guild never set up")
	}
	f.set(t, guild.ID, honeypot.Settings{ChannelDiscordID: "trap", TemplateID: f.templates.template.ID})
	if err := f.registry.CheckModuleEnablement(ctx, guild, quack.ModuleStates{Honeypot: true}); err != nil {
		t.Fatal(err)
	}
}

// The channel check names each permission Quack is missing and refuses
// anything but a text channel.
func TestChannelValidatorRequiresEvidenceAndCleanupPermissions(t *testing.T) {
	for _, test := range []struct {
		name        string
		deny        int64
		channelType discordgo.ChannelType
		want        string
	}{
		{name: "ready"},
		{name: "missing history", deny: discordgo.PermissionReadMessageHistory, want: "Read Message History"},
		{name: "missing cleanup", deny: discordgo.PermissionManageMessages, want: "Manage Messages"},
		{name: "voice channel", channelType: discordgo.ChannelTypeGuildVoice, want: "unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := &trapDiscord{deny: test.deny, channelType: test.channelType}
			f := newModule(t)
			guildID := f.guild(t, "guild")
			validator := honeypot.NewChannelValidator(discordSession(fake.serve), modules.NewGuilds(f.store))
			err := validator.ValidateHoneypotChannel(context.Background(), guildID, "trap")
			if test.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if !errors.Is(err, honeypot.ErrChannelUnavailable) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err = %v, want %q", err, test.want)
			}
		})
	}
}

// Messages outside the trap, and Quack's own, are dropped before any
// Discord call; ordinary bots in the trap reach the live lookup; a new trap
// channel applies to the very next message.
func TestGatewayFiltersBeforeDiscordLookup(t *testing.T) {
	var reads int
	session := discordSession(func(*http.Request) (int, any) {
		reads++
		return -1, nil
	})
	f := newDiscordModule(t, session)
	guildID := f.guild(t, "guild")
	f.set(t, guildID, honeypot.Settings{ChannelDiscordID: "trap", TemplateID: "template"})
	event := &discordgo.MessageCreate{Message: &discordgo.Message{ID: "message", GuildID: "guild", ChannelID: "ordinary", Author: &discordgo.User{ID: "member"}}}
	honeypot.OnMessageCreate(f.module, session, event)
	event.ChannelID = "trap"
	event.Author.Bot = true
	honeypot.OnMessageCreate(f.module, session, event)
	if reads != 1 {
		t.Fatalf("an ordinary bot must reach the live lookup: %d reads", reads)
	}
	event.Author.ID = "quack"
	honeypot.OnMessageCreate(f.module, session, event)
	event.Author.ID, event.WebhookID = "member", "webhook"
	honeypot.OnMessageCreate(f.module, session, event)
	if reads != 1 {
		t.Fatal("Quack's or a webhook's message reached the lookup")
	}
	event.WebhookID, event.ChannelID = "", "ordinary"
	f.set(t, guildID, honeypot.Settings{ChannelDiscordID: "ordinary", TemplateID: "template"})
	honeypot.OnMessageCreate(f.module, session, event)
	if reads != 2 {
		t.Fatalf("the new trap channel was ignored: %d reads", reads)
	}
}
