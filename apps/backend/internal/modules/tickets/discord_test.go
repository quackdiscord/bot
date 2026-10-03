package tickets

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/quack"
)

func TestPrivateChannelACLRequiresEveryoneDenialAndStaffVisibility(t *testing.T) {
	channel := &discordgo.Channel{GuildID: "guild", PermissionOverwrites: ticketPermissionOverwrites("guild", "owner", "bot", []string{"staff"})}
	if err := validateTicketACL(channel, "guild", "owner", "bot", []string{"staff"}); err != nil {
		t.Fatalf("valid private ACL rejected: %v", err)
	}
	channel.PermissionOverwrites = channel.PermissionOverwrites[1:]
	if err := validateTicketACL(channel, "guild", "owner", "bot", []string{"staff"}); err == nil {
		t.Fatal("public ticket ACL was accepted")
	}
}

// fakeDiscord answers Discord REST calls without a network.
type fakeDiscord func(*http.Request) (*http.Response, error)

func (f fakeDiscord) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func testSession(t *testing.T, serve fakeDiscord) *discordgo.Session {
	t.Helper()
	session, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.State.User = &discordgo.User{ID: "bot"}
	session.Client = &http.Client{Transport: serve}
	return session
}

func reply(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestThreadSyncKeepsCurrentStaffAndRemovesFormerStaff(t *testing.T) {
	var removed, added []string
	session := testSession(t, func(request *http.Request) (*http.Response, error) {
		switch request.Method {
		case http.MethodGet:
			if strings.Contains(request.URL.Path, "/guilds/") {
				return reply(`[{"user":{"id":"current-staff"},"roles":["staff-role"]},{"user":{"id":"new-staff"},"roles":["staff-role"]},{"user":{"id":"former-staff"},"roles":[]}]`), nil
			}
			return reply(`[{"user_id":"owner"},{"user_id":"bot"},{"user_id":"former-staff"},{"user_id":"current-staff"}]`), nil
		case http.MethodPut:
			added = append(added, request.URL.Path)
		case http.MethodDelete:
			removed = append(removed, request.URL.Path)
		default:
			t.Fatalf("unexpected request %s", request.Method)
		}
		return reply(""), nil
	})
	if err := (channels{session: session}).syncThreadMembers(context.Background(), "guild", "thread", "owner", []string{"staff-role"}); err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || !strings.HasSuffix(removed[0], "/former-staff") || len(added) != 1 || !strings.HasSuffix(added[0], "/new-staff") {
		t.Fatalf("removed %v, added %v", removed, added)
	}
}

// oneGuild is a GuildStore holding one active guild.
type oneGuild struct{}

func (oneGuild) GetGuildByID(context.Context, string) (*quack.Guild, error) {
	return &quack.Guild{ULIDModel: quack.ULIDModel{ID: "internal-guild"}, DiscordGuildID: "guild", IsActive: true}, nil
}

func (g oneGuild) GetGuildByDiscordID(ctx context.Context, _ string) (*quack.Guild, error) {
	return g.GetGuildByID(ctx, "")
}

func TestCreationHonorsPrivateThreadSetting(t *testing.T) {
	for _, useThreads := range []bool{true, false} {
		created := false
		session := testSession(t, func(request *http.Request) (*http.Response, error) {
			if request.Method != http.MethodPost {
				return reply(`{"id":"entry","guild_id":"guild","parent_id":"category"}`), nil
			}
			created = true
			var payload struct {
				Type      discordgo.ChannelType `json:"type"`
				Invitable bool                  `json:"invitable"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if useThreads {
				if !strings.HasSuffix(request.URL.Path, "/channels/entry/threads") || payload.Type != discordgo.ChannelTypeGuildPrivateThread || payload.Invitable {
					t.Fatalf("unexpected thread creation: %s %+v", request.URL.Path, payload)
				}
			} else if !strings.HasSuffix(request.URL.Path, "/guilds/guild/channels") || payload.Type != discordgo.ChannelTypeGuildText {
				t.Fatalf("unexpected text creation: %s %+v", request.URL.Path, payload)
			}
			return reply(`{"id":"ticket"}`), nil
		})
		c := channels{session: session, guilds: modules.NewGuilds(oneGuild{})}
		id, err := c.CreateChannel(context.Background(), "internal-guild", "owner", Settings{EntryChannelDiscordID: "entry", UsePrivateThreads: useThreads})
		if err != nil || id != "ticket" || !created {
			t.Fatalf("threads=%v: created %q, %v", useThreads, id, err)
		}
	}
}

func TestChannelErrorRecognizesDeletedChannel(t *testing.T) {
	unknown := &discordgo.RESTError{Message: &discordgo.APIErrorMessage{Code: discordgo.ErrCodeUnknownChannel}}
	if err := channelError("read", unknown); !errors.Is(err, ErrChannelMissing) {
		t.Fatalf("unknown channel: got %v, want ErrChannelMissing", err)
	}
	other := &discordgo.RESTError{Message: &discordgo.APIErrorMessage{Code: discordgo.ErrCodeMissingAccess}}
	if err := channelError("read", other); errors.Is(err, ErrChannelMissing) || !errors.Is(err, other) {
		t.Fatalf("missing access: got %v, want the original error", err)
	}
}
