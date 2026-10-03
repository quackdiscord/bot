package tickets

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discord"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/quack"
)

// setupDiscord fakes a guild where Quack holds every permission tickets
// need, the queue is staff-only, and the entry channel is public. It
// records posted panels.
type setupDiscord struct {
	t      *testing.T
	panels []string
}

func (d *setupDiscord) serve(r *http.Request) (*http.Response, error) {
	everyone := int64(discordgo.PermissionViewChannel | discordgo.PermissionSendMessages | discordgo.PermissionReadMessageHistory |
		discordgo.PermissionCreatePrivateThreads | discordgo.PermissionSendMessagesInThreads | discordgo.PermissionManageThreads | discordgo.PermissionAttachFiles)
	path := r.URL.Path
	switch {
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/guilds/guild"):
		return reply(fmt.Sprintf(`{"id":"guild","owner_id":"owner","roles":[{"id":"guild","permissions":"%d"}]}`, everyone)), nil
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/members/bot"):
		return reply(`{"user":{"id":"bot"},"roles":[]}`), nil
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/channels/entry"):
		return reply(`{"id":"entry","guild_id":"guild","type":0}`), nil
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/channels/queue"):
		return reply(fmt.Sprintf(`{"id":"queue","guild_id":"guild","type":0,"permission_overwrites":[{"id":"guild","type":0,"deny":"%d","allow":"0"},{"id":"bot","type":1,"allow":"%d","deny":"0"}]}`,
			discordgo.PermissionViewChannel, everyone)), nil
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/channels/public"):
		return reply(`{"id":"public","guild_id":"guild","type":0}`), nil
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/channels/entry/messages"):
		var payload struct{ Content string }
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			d.t.Fatal(err)
		}
		d.panels = append(d.panels, payload.Content)
		return reply(fmt.Sprintf(`{"id":"panel-%d"}`, len(d.panels))), nil
	case r.Method == http.MethodPatch && strings.HasSuffix(path, "/channels/entry/messages/panel-1"):
		return reply(`{"id":"panel-1"}`), nil
	}
	d.t.Fatalf("unexpected request %s %s", r.Method, path)
	return nil, nil
}

func setupModule(t *testing.T) (*Module, *setupDiscord, *modules.Registry) {
	t.Helper()
	fake := &setupDiscord{t: t}
	session := testSession(t, fake.serve)
	db := testDB(t)
	registry := modules.NewRegistry(db)
	m := New(db, registry, nil, modules.NewGuilds(guildStore{discordID: "guild"}), session, deniedStaff{})
	return m, fake, registry
}

func setupRequest(entry, queue string) discord.SetupRequest {
	option := func(name, value string) *discordgo.ApplicationCommandInteractionDataOption {
		return &discordgo.ApplicationCommandInteractionDataOption{Name: name, Type: discordgo.ApplicationCommandOptionChannel, Value: value}
	}
	return discord.SetupRequest{
		Guild: &quack.GuildStaffContext{
			Guild:              &quack.Guild{ULIDModel: quack.ULIDModel{ID: "internal"}, DiscordGuildID: "guild"},
			ActorDiscordUserID: "admin",
			Permissions:        map[quack.PermissionAction]bool{quack.PermissionActionGuildSettingsWrite: true},
		},
		UserID:  "admin",
		Options: []*discordgo.ApplicationCommandInteractionDataOption{option("entry", entry), option("queue", queue)},
	}
}

// TestSetupSavesEnablesAndKeepsOnePanel posts the panel once and edits it
// in place on a second setup.
func TestSetupSavesEnablesAndKeepsOnePanel(t *testing.T) {
	m, fake, registry := setupModule(t)
	ctx := context.Background()
	for range 2 {
		message, err := m.Setup(ctx, setupRequest("entry", "queue"))
		if err != nil {
			t.Fatal(err)
		}
		if message.Content != "{{quack:ticket}} Tickets are ready in <#entry>. Staff notifications and transcripts will go to <#queue>." || message.Ephemeral {
			t.Fatalf("confirmation = %+v", message)
		}
	}
	if len(fake.panels) != 1 || !strings.HasPrefix(fake.panels[0], "# Need a hand?") {
		t.Fatalf("panels = %q", fake.panels)
	}
	settings, enabled, err := modules.LoadSettings(ctx, registry, "internal", modules.Tickets, Defaults())
	if err != nil || !enabled || settings.EntryChannelDiscordID != "entry" || settings.QueueChannelDiscordID != "queue" ||
		settings.EntryPanelChannelID != "entry" || settings.EntryPanelMessageID != "panel-1" {
		t.Fatalf("settings = %+v, enabled = %v, %v", settings, enabled, err)
	}
	if err := registry.CheckModuleEnablement(ctx, &quack.Guild{ULIDModel: quack.ULIDModel{ID: "internal"}, DiscordGuildID: "guild"}, quack.ModuleStates{Tickets: true}); err != nil {
		t.Fatalf("enablement check rejected the setup: %v", err)
	}
}

func TestSetupRefusesBadChannels(t *testing.T) {
	for _, test := range []struct {
		name, entry, queue, want string
	}{
		{"same channel", "entry", "entry", "Choose separate entry and staff queue channels."},
		{"public queue", "entry", "public", "Quack needs to view, send, read history and attach files in the queue channel."},
	} {
		t.Run(test.name, func(t *testing.T) {
			m, fake, registry := setupModule(t)
			_, err := m.Setup(context.Background(), setupRequest(test.entry, test.queue))
			if message, ok := discord.UserMessage(err); !ok || message != test.want {
				t.Fatalf("error = %v", err)
			}
			if _, enabled, _ := modules.LoadSettings(context.Background(), registry, "internal", modules.Tickets, Defaults()); enabled || len(fake.panels) != 0 {
				t.Fatal("refused setup changed something")
			}
		})
	}
}

func TestEnablementCheckRefusesBrokenSettings(t *testing.T) {
	m, _, _ := setupModule(t)
	guild := &quack.Guild{ULIDModel: quack.ULIDModel{ID: "internal"}, DiscordGuildID: "guild"}
	for _, config := range []string{
		`{"entry_channel_discord_id":"entry"}`,
		`{"entry_channel_discord_id":"entry","queue_channel_discord_id":"entry"}`,
		`{"entry_channel_discord_id":"entry","queue_channel_discord_id":"public"}`,
		`not json`,
	} {
		if err := m.checkEnablement(context.Background(), guild, config); err == nil {
			t.Fatalf("accepted %s", config)
		}
	}
	if err := m.checkEnablement(context.Background(), guild, `{"entry_channel_discord_id":"entry","queue_channel_discord_id":"queue"}`); err != nil {
		t.Fatal(err)
	}
}

// staleStaffStore supplies the attribution writes live resolution makes.
type staleStaffStore struct{ quack.GuildStore }

func (staleStaffStore) UpsertGuild(context.Context, quack.UpsertGuildParams) (*quack.Guild, error) {
	return &quack.Guild{ULIDModel: quack.ULIDModel{ID: "internal-guild"}, DiscordGuildID: "guild"}, nil
}

func (staleStaffStore) UpsertStaffMember(context.Context, quack.UpsertStaffMemberParams) (*quack.StaffMember, error) {
	return &quack.StaffMember{DiscordUserID: "member"}, nil
}

// demotedDirectory reports a member whose Administrator role is gone,
// though the interaction still claims it.
type demotedDirectory struct {
	quack.GuildDirectory
	calls int
}

func (d *demotedDirectory) GuildAuthorization(context.Context, string, string, string) (*quack.DiscordGuildAuthorization, error) {
	d.calls++
	return &quack.DiscordGuildAuthorization{
		Guild: quack.DiscordBotGuild{ID: "guild", OwnerID: "owner"},
		Actor: quack.DiscordMemberAuthorization{DiscordUserID: "member", Present: true},
		Bot:   quack.DiscordMemberAuthorization{DiscordUserID: "bot", Present: true},
	}, nil
}

func TestInteractionActorUsesLiveAuthority(t *testing.T) {
	directory := &demotedDirectory{}
	m := &Module{staff: quack.NewGuildService(staleStaffStore{}, directory)}
	actor, err := m.actor(context.Background(), &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{GuildID: "guild", Member: &discordgo.Member{
			User: &discordgo.User{ID: "member"}, Permissions: discordgo.PermissionAdministrator,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if directory.calls != 1 || actor.CanManage || actor.CanModerate || actor.DiscordUserID != "member" {
		t.Fatalf("stale interaction granted authority: %+v after %d live lookups", actor, directory.calls)
	}
}

// TestGatewayJournalsOnlyTicketText records text posted in an open ticket
// thread, and nothing from other channels or without content.
func TestGatewayJournalsOnlyTicketText(t *testing.T) {
	m, _, _ := setupModule(t)
	ctx := context.Background()
	actor := modules.Actor{GuildID: "internal", DiscordUserID: "member"}
	token, err := m.store.reserveOpening(ctx, actor, m.service.now())
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := m.store.finishOpening(ctx, actor, token, "thread", m.service.now())
	if err != nil {
		t.Fatal(err)
	}
	m.service.rememberJournalThread(ticket)
	post := func(id, channel, content string) {
		m.onMessageCreate(nil, &discordgo.MessageCreate{Message: &discordgo.Message{
			ID: id, ChannelID: channel, GuildID: "guild", Content: content, Author: &discordgo.User{ID: "member"},
		}})
	}
	post("1", "thread", "hello")
	post("2", "thread", "")
	post("3", "elsewhere", "private")
	var bodies []string
	if err := m.store.db.Model(&journalRecord{}).Pluck("body", &bodies).Error; err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 1 || bodies[0] != "hello" {
		t.Fatalf("journal = %q", bodies)
	}
}
