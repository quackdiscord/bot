package moduleintegration

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/modules/tickets"
	"github.com/quackdiscord/bot/internal/quack"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// ticketAuthorityStore keeps the regression independent of gateway state and
// supplies only the durable attribution writes performed by live resolution.
type ticketAuthorityStore struct{ quack.GuildStore }

func (ticketAuthorityStore) UpsertGuild(context.Context, quack.UpsertGuildParams) (*quack.Guild, error) {
	return &quack.Guild{ULIDModel: quack.ULIDModel{ID: "internal-guild"}, DiscordGuildID: "guild"}, nil
}
func (ticketAuthorityStore) UpsertStaffMember(context.Context, quack.UpsertStaffMemberParams) (*quack.StaffMember, error) {
	return &quack.StaffMember{DiscordUserID: "member"}, nil
}

// ticketAuthorityDiscord exposes a demotion which has not reached gateway cache.
type ticketAuthorityDiscord struct {
	quack.GuildDirectory
	calls int
}

func (d *ticketAuthorityDiscord) GuildAuthorization(context.Context, string, string, string) (*quack.DiscordGuildAuthorization, error) {
	d.calls++
	return &quack.DiscordGuildAuthorization{
		Guild: quack.DiscordBotGuild{ID: "guild", OwnerID: "owner"},
		Actor: quack.DiscordMemberAuthorization{DiscordUserID: "member", Present: true},
		Bot:   quack.DiscordMemberAuthorization{DiscordUserID: "bot", Present: true},
	}, nil
}

func TestTicketActorUsesLiveGuildAuthority(t *testing.T) {
	discord := &ticketAuthorityDiscord{}
	r := &Runtime{services: &quack.Services{Guilds: quack.NewGuildService(ticketAuthorityStore{}, discord)}}
	actor, err := r.ticketActor(ui.Context{Context: context.Background(), Interaction: &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{GuildID: "guild", Member: &discordgo.Member{
			User: &discordgo.User{ID: "member"}, Permissions: discordgo.PermissionAdministrator,
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if discord.calls != 1 || actor.CanManage || actor.CanModerate {
		t.Fatalf("stale interaction granted authority: %+v, live calls=%d", actor, discord.calls)
	}
}

// ticketRoundTripper exercises the Discord transport without binding a socket.
type ticketRoundTripper func(*http.Request) (*http.Response, error)

func (f ticketRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestTicketThreadRepairPreservesCurrentStaffAndRemovesFormerStaff(t *testing.T) {
	session, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.State.User = &discordgo.User{ID: "bot"}
	var removed, added []string
	session.Client = &http.Client{Transport: ticketRoundTripper(func(request *http.Request) (*http.Response, error) {
		body := ""
		switch request.Method {
		case http.MethodGet:
			if strings.Contains(request.URL.Path, "/guilds/") {
				body = `[{"user":{"id":"current-staff"},"roles":["staff-role"]},{"user":{"id":"new-staff"},"roles":["staff-role"]},{"user":{"id":"former-staff"},"roles":[]}]`
			} else {
				body = `[{"user_id":"owner"},{"user_id":"bot"},{"user_id":"former-staff"},{"user_id":"current-staff"}]`
			}
		case http.MethodPut:
			added = append(added, request.URL.Path)
		case http.MethodDelete:
			removed = append(removed, request.URL.Path)
		default:
			t.Fatalf("unexpected request %s", request.Method)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	client := ticketDiscordClient{session: session}
	if err := client.syncTicketThreadMembers(context.Background(), "guild", "thread", "owner", []string{"staff-role"}); err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || !strings.HasSuffix(removed[0], "/former-staff") || len(added) != 1 || !strings.HasSuffix(added[0], "/new-staff") {
		t.Fatalf("unexpected invitation removals: %v", removed)
	}
}

type appealDestinationStore struct{}

func (appealDestinationStore) GetGuildSettings(context.Context, string) (*quack.GuildSettings, error) {
	return &quack.GuildSettings{AuditMirrorChannelDiscordID: "channel"}, nil
}
func (appealDestinationStore) GetGuildByID(context.Context, string) (*quack.Guild, error) {
	return &quack.Guild{DiscordGuildID: "discord-guild"}, nil
}

type rejectingAppealDestination struct{ guildID, channelID string }

func (v *rejectingAppealDestination) ValidateStaffChannel(_ context.Context, guildID, channelID string) error {
	v.guildID, v.channelID = guildID, channelID
	return errors.New("destination is public")
}

func TestAppealStaffDestinationRevalidatesPrivacy(t *testing.T) {
	validator := &rejectingAppealDestination{}
	resolver := appealStaffChannelResolver{repository: appealDestinationStore{}, validator: validator}
	if channel, err := resolver.AppealStaffChannel(context.Background(), "internal-guild"); err == nil || channel != "" {
		t.Fatalf("unsafe appeal destination accepted: %q, %v", channel, err)
	}
	if validator.guildID != "discord-guild" || validator.channelID != "channel" {
		t.Fatalf("incorrect destination identity: %+v", validator)
	}
}

func TestTicketCreationHonorsPrivateThreadSetting(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:ticket-thread-setting?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&quack.Guild{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&quack.Guild{ULIDModel: quack.ULIDModel{ID: "internal-guild"}, DiscordGuildID: "guild", IsActive: true}).Error; err != nil {
		t.Fatal(err)
	}
	session, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	session.State.User = &discordgo.User{ID: "bot"}
	for _, useThreads := range []bool{true, false} {
		created := false
		session.Client = &http.Client{Transport: ticketRoundTripper(func(request *http.Request) (*http.Response, error) {
			body := `{"id":"entry","guild_id":"guild","parent_id":"category"}`
			if request.Method == http.MethodPost {
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
				body = `{"id":"ticket"}`
			}
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
		})}
		client := ticketDiscordClient{session: session, resolver: guildResolver{db: db}}
		id, err := client.CreatePrivateTicketChannel(context.Background(), "internal-guild", "owner", tickets.Settings{EntryChannelDiscordID: "entry", UsePrivateThreads: useThreads})
		if err != nil || id != "ticket" || !created {
			t.Fatalf("ticket creation: %s %v", id, err)
		}
	}
}
