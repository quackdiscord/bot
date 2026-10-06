package discord

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

// setupTransport answers the session's REST calls from a test fixture.
type setupTransport func(*http.Request) (*http.Response, error)

func (f setupTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func setupSession(t *testing.T, serve setupTransport) *discordgo.Session {
	t.Helper()
	session, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.State.User = &discordgo.User{ID: "bot"}
	session.Client = &http.Client{Transport: serve}
	return session
}

func statusResponse(r *http.Request, status int, body any) *http.Response {
	encoded, _ := json.Marshal(body)
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(string(encoded))),
		Request:    r,
	}
}

// TestSetupChannelSelection keeps setup from editing an administrator's
// choice or creating a duplicate when it reruns or Discord is unavailable.
func TestSetupChannelSelection(t *testing.T) {
	for _, scenario := range []struct {
		name, specified, configured string
		status, wantCreates         int
	}{
		{"explicit", "chosen", "old", 200, 0},
		{"reuse", "", "old", 200, 0},
		{"first setup", "", "", 200, 1},
		{"deleted", "", "old", 404, 1},
		{"forbidden", "", "old", 403, 0},
		{"unavailable", "", "old", 503, 0},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			requests, creates := 0, 0
			session := setupSession(t, func(r *http.Request) (*http.Response, error) {
				requests++
				switch {
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/channels/old"):
					if scenario.status != 200 {
						return statusResponse(r, scenario.status, map[string]any{"code": 10003, "message": "missing or unavailable"}), nil
					}
					return statusResponse(r, 200, &discordgo.Channel{ID: "old", GuildID: "guild", Type: discordgo.ChannelTypeGuildText}), nil
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/guilds/guild"):
					return statusResponse(r, 200, &discordgo.Guild{ID: "guild"}), nil
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/guilds/guild/channels"):
					creates++
					var data discordgo.GuildChannelCreateData
					if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
						t.Fatal(err)
					}
					if data.Name != "appeals" || data.Topic == "" || len(data.PermissionOverwrites) != 2 {
						t.Fatalf("invalid creation: %+v", data)
					}
					return statusResponse(r, 200, &discordgo.Channel{ID: "new", GuildID: "guild"}), nil
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/channels/new/messages"):
					var message discordgo.MessageSend
					if err := json.NewDecoder(r.Body).Decode(&message); err != nil {
						t.Fatal(err)
					}
					if !strings.HasPrefix(message.Content, "# Appeals\n") || message.AllowedMentions == nil {
						t.Fatalf("invalid intro: %+v", message)
					}
					return statusResponse(r, 200, &discordgo.Message{ID: "intro"}), nil
				}
				t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
				return nil, nil
			})
			id, err := SetupChannel(context.Background(), session, "guild", scenario.specified, scenario.configured, "appeals", SetupStaffChannel, nil)
			wantErr := scenario.status == 403 || scenario.status == 503
			if (err != nil) != wantErr || creates != scenario.wantCreates {
				t.Fatalf("id=%s err=%v creates=%d", id, err, creates)
			}
			if _, ok := UserMessage(err); wantErr && !ok {
				t.Fatalf("error has no user copy: %v", err)
			}
			if scenario.specified != "" && (requests != 0 || id != scenario.specified) {
				t.Fatal("explicit choice was inspected or replaced")
			}
			if scenario.name == "reuse" && id != "old" {
				t.Fatal("configured channel replaced")
			}
		})
	}
}

// TestCreatedChannelEffectivePermissions checks what members, moderators,
// and Quack can do in each kind of new channel, and that a new staff
// channel passes the staff-only rule.
func TestCreatedChannelEffectivePermissions(t *testing.T) {
	read := int64(discordgo.PermissionViewChannel | discordgo.PermissionReadMessageHistory)
	guild := &discordgo.Guild{
		ID: "guild",
		Roles: []*discordgo.Role{
			{ID: "guild"},
			{ID: "mod", Permissions: discordgo.PermissionModerateMembers},
			{ID: "manager", Permissions: discordgo.PermissionManageGuild},
		},
		Members: []*discordgo.Member{
			{User: &discordgo.User{ID: "member"}},
			{User: &discordgo.User{ID: "moderator"}, Roles: []string{"mod"}},
			{User: &discordgo.User{ID: "bot"}},
		},
	}
	for _, kind := range []SetupChannelKind{SetupStaffChannel, SetupTicketEntry, SetupHoneypotChannel} {
		overwrites := setupChannelPermissions(guild, "bot", kind, nil)
		channel := &discordgo.Channel{ID: "channel", GuildID: guild.ID, Type: discordgo.ChannelTypeGuildText, PermissionOverwrites: overwrites}
		guild.Channels = []*discordgo.Channel{channel}
		state := discordgo.NewState()
		if err := state.GuildAdd(guild); err != nil {
			t.Fatal(err)
		}
		permissions := func(id string) int64 {
			p, err := state.UserChannelPermissions(id, channel.ID)
			if err != nil {
				t.Fatal(err)
			}
			return p
		}
		member, mod, bot := permissions("member"), permissions("moderator"), permissions("bot")
		if bot&read != read || bot&discordgo.PermissionSendMessages == 0 {
			t.Fatal("bot cannot deliver")
		}
		switch kind {
		case SetupStaffChannel:
			if member&discordgo.PermissionViewChannel != 0 || mod&read != read {
				t.Fatal("staff destination permissions incorrect")
			}
			for _, overwrite := range overwrites {
				if overwrite.ID == "manager" {
					t.Fatal("a Manage Server role without moderation was let into a staff channel")
				}
			}
		case SetupTicketEntry:
			required := int64(discordgo.PermissionCreatePrivateThreads | discordgo.PermissionManageThreads | discordgo.PermissionSendMessagesInThreads)
			if member&read != read || member&discordgo.PermissionSendMessages != 0 ||
				member&discordgo.PermissionSendMessagesInThreads == 0 || bot&required != required {
				t.Fatal("ticket entry cannot support private conversations")
			}
		case SetupHoneypotChannel:
			if member&discordgo.PermissionSendMessages == 0 || bot&discordgo.PermissionManageMessages == 0 {
				t.Fatal("trap cannot receive and remove messages")
			}
		}
	}
}

// TestCreatedStaffChannelAdmitsModeratorRoles checks that a guild with
// configured moderator roles gets those roles in a new staff channel, and
// no longer every role with Moderate Members.
func TestCreatedStaffChannelAdmitsModeratorRoles(t *testing.T) {
	guild := &discordgo.Guild{
		ID: "guild",
		Roles: []*discordgo.Role{
			{ID: "guild"},
			{ID: "timeouts", Permissions: discordgo.PermissionModerateMembers},
			{ID: "mods"},
		},
	}
	admitted := map[string]bool{}
	for _, overwrite := range setupChannelPermissions(guild, "bot", SetupStaffChannel, []string{"mods", "gone"}) {
		if overwrite.Allow&discordgo.PermissionViewChannel != 0 {
			admitted[overwrite.ID] = true
		}
	}
	if !admitted["mods"] || admitted["timeouts"] || admitted["gone"] || !admitted["bot"] {
		t.Fatalf("admitted %v, want the bot and the configured moderator role only", admitted)
	}
}

// TestSetupChannelIntroFailureKeepsCreatedChannel makes sure a failed
// introduction cannot turn into a second channel when setup is retried.
func TestSetupChannelIntroFailureKeepsCreatedChannel(t *testing.T) {
	creates, intros := 0, 0
	session := setupSession(t, func(r *http.Request) (*http.Response, error) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/guilds/guild/channels"):
			creates++
			return statusResponse(r, 200, map[string]any{"id": "created", "guild_id": "guild", "type": 0}), nil
		case strings.HasSuffix(r.URL.Path, "/channels/created/messages"):
			intros++
			return statusResponse(r, 503, map[string]any{"code": 0, "message": "Unavailable"}), nil
		case strings.HasSuffix(r.URL.Path, "/channels/created"):
			return statusResponse(r, 200, map[string]any{"id": "created", "guild_id": "guild", "type": 0}), nil
		case strings.HasSuffix(r.URL.Path, "/guilds/guild"):
			return statusResponse(r, 200, map[string]any{"id": "guild"}), nil
		}
		t.Fatalf("unexpected request: %s", r.URL.Path)
		return nil, nil
	})
	id, err := SetupChannel(context.Background(), session, "guild", "", "", "appeals", SetupStaffChannel, nil)
	if err != nil || id != "created" {
		t.Fatalf("lost the created channel: %s, %v", id, err)
	}
	if _, err := SetupChannel(context.Background(), session, "guild", "", id, "appeals", SetupStaffChannel, nil); err != nil {
		t.Fatal(err)
	}
	if creates != 1 || intros != 1 {
		t.Fatalf("repeated creation or intro: %d, %d", creates, intros)
	}
}

// TestPanelChannelsSupplyTheirOwnWelcome keeps the ticket panel and the
// honeypot warning as the only first post in their channels.
func TestPanelChannelsSupplyTheirOwnWelcome(t *testing.T) {
	for _, kind := range []SetupChannelKind{SetupTicketEntry, SetupHoneypotChannel} {
		if topic, intro := setupChannelPresentation("channel", kind); topic == "" || intro != "" {
			t.Fatalf("panel channel presentation: %q %q", topic, intro)
		}
	}
}
