package tickets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discord"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/quack"
)

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

func respond(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func reply(body string) *http.Response { return respond(http.StatusOK, body) }

// guildStore knows one active guild: "internal" in Quack, discordID in
// Discord, with moderatorRoleIDs as its moderator roles.
type guildStore struct {
	discordID        string
	moderatorRoleIDs []string
}

func (s guildStore) guild() *quack.Guild {
	return &quack.Guild{ULIDModel: quack.ULIDModel{ID: "internal"}, DiscordGuildID: s.discordID, IsActive: true}
}

func (s guildStore) GetGuildByID(_ context.Context, id string) (*quack.Guild, error) {
	if id != "internal" {
		return nil, nil
	}
	return s.guild(), nil
}

func (s guildStore) GetGuildByDiscordID(_ context.Context, id string) (*quack.Guild, error) {
	if id != s.discordID {
		return nil, nil
	}
	return s.guild(), nil
}

func (s guildStore) GetGuildSettings(_ context.Context, id string) (*quack.GuildSettings, error) {
	if id != "internal" {
		return nil, nil
	}
	return &quack.GuildSettings{GuildID: id, ModeratorRoleIDs: s.moderatorRoleIDs}, nil
}

// testChannels returns a client for the Discord guild "guild", whose
// moderator role is "mod-role".
func testChannels(session *discordgo.Session) channels {
	return channels{session: session, guilds: modules.NewGuilds(guildStore{discordID: "guild", moderatorRoleIDs: []string{"mod-role"}})}
}

func TestCreateThreadIsPrivateAndNotInvitable(t *testing.T) {
	created := false
	session := testSession(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet {
			return reply(`{"id":"entry","guild_id":"guild","type":0}`), nil
		}
		var payload struct {
			Type      discordgo.ChannelType `json:"type"`
			Invitable bool                  `json:"invitable"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(r.URL.Path, "/channels/entry/threads") || payload.Type != discordgo.ChannelTypeGuildPrivateThread || payload.Invitable {
			t.Fatalf("thread request %s %+v", r.URL.Path, payload)
		}
		created = true
		return reply(`{"id":"ticket"}`), nil
	})
	id, err := testChannels(session).CreateThread(context.Background(), "internal", "owner", Settings{EntryChannelDiscordID: "entry"})
	if err != nil || id != "ticket" || !created {
		t.Fatalf("thread = %q, %v", id, err)
	}
}

func TestThreadSyncKeepsCurrentStaffAndRemovesFormerStaff(t *testing.T) {
	var removed, added []string
	session := testSession(t, func(r *http.Request) (*http.Response, error) {
		switch r.Method {
		case http.MethodGet:
			switch {
			case strings.HasSuffix(r.URL.Path, "/guilds/guild"):
				return reply(fmt.Sprintf(`{"id":"guild","roles":[{"id":"staff-role","permissions":"%d"}]}`, discordgo.PermissionModerateMembers)), nil
			case strings.HasSuffix(r.URL.Path, "/members/current-staff"):
				return reply(`{"user":{"id":"current-staff"},"roles":["staff-role"]}`), nil
			case strings.HasSuffix(r.URL.Path, "/members/role-moderator"):
				return reply(`{"user":{"id":"role-moderator"},"roles":["mod-role"]}`), nil
			case strings.HasSuffix(r.URL.Path, "/members/former-staff"):
				return reply(`{"user":{"id":"former-staff"},"roles":[]}`), nil
			case strings.HasSuffix(r.URL.Path, "/members/departed"):
				return respond(http.StatusNotFound, `{"code":10007,"message":"Unknown Member"}`), nil
			case strings.Contains(r.URL.Path, "/channels/thread/thread-members"):
				return reply(`[{"user_id":"owner"},{"user_id":"bot"},{"user_id":"former-staff"},{"user_id":"current-staff"},{"user_id":"role-moderator"},{"user_id":"departed"}]`), nil
			}
		case http.MethodPut:
			added = append(added, r.URL.Path)
			return reply(``), nil
		case http.MethodDelete:
			removed = append(removed, r.URL.Path)
			return reply(``), nil
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		return nil, nil
	})
	if err := testChannels(session).syncThreadMembers(context.Background(), "guild", "thread", "owner"); err != nil {
		t.Fatal(err)
	}
	if len(removed) != 2 || !strings.HasSuffix(removed[0], "/former-staff") || !strings.HasSuffix(removed[1], "/departed") || len(added) != 0 {
		t.Fatalf("removed = %v, added = %v", removed, added)
	}
}

// TestWelcomeMentionsOnlyOwner keeps the greeting from pinging anyone else.
func TestWelcomeMentionsOnlyOwner(t *testing.T) {
	sent := false
	session := testSession(t, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/channels/thread/messages") {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var payload struct {
			Content         string                           `json:"content"`
			AllowedMentions discordgo.MessageAllowedMentions `json:"allowed_mentions"`
			Components      []json.RawMessage                `json:"components"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(payload.Content, "<@owner> A mod will be here soon. Feel free to tell us what's up while you wait.") || len(payload.Components) != 1 {
			t.Fatalf("greeting = %+v", payload)
		}
		if len(payload.AllowedMentions.Parse) != 0 || len(payload.AllowedMentions.Users) != 1 || payload.AllowedMentions.Users[0] != "owner" {
			t.Fatalf("mentions = %+v", payload.AllowedMentions)
		}
		sent = true
		return reply(`{"id":"welcome"}`), nil
	})
	if err := testChannels(session).SendWelcome(context.Background(), &Ticket{ID: "ticket", OwnerDiscordUserID: "owner", ThreadDiscordChannelID: "thread"}); err != nil || !sent {
		t.Fatalf("welcome sent = %v, %v", sent, err)
	}
}

func TestCaptureMessagesPagesAndKeepsAttachments(t *testing.T) {
	calls := 0
	stamp := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	session := testSession(t, func(r *http.Request) (*http.Response, error) {
		calls++
		var page []*discordgo.Message
		if calls == 1 {
			for id := 200; id > 100; id-- {
				page = append(page, &discordgo.Message{ID: fmt.Sprint(id), Timestamp: stamp, Content: fmt.Sprintf("message-%d", id), Author: &discordgo.User{ID: "member", Username: "Member"}})
			}
		} else {
			if r.URL.Query().Get("before") != "101" {
				t.Fatalf("cursor = %s", r.URL.RawQuery)
			}
			page = []*discordgo.Message{{ID: "100", Timestamp: stamp, Content: "first", Attachments: []*discordgo.MessageAttachment{nil, {Filename: "proof.png", Size: 42, URL: "https://cdn.discordapp.com/attachments/file"}}}}
		}
		body, _ := json.Marshal(page)
		return reply(string(body)), nil
	})
	messages, err := testChannels(session).CaptureMessages(context.Background(), "thread")
	if err != nil {
		t.Fatal(err)
	}
	transcript := FormatTranscript(messages)
	if calls != 2 || strings.Count(transcript, "message-101") != 1 || strings.Index(transcript, "first") > strings.Index(transcript, "message-101") ||
		!strings.Contains(transcript, "Member (member)") || !strings.Contains(transcript, "original attachment URL (may expire)") {
		t.Fatalf("transcript:\n%s", transcript)
	}
}

func TestCaptureMessagesRejectsRepeatedPage(t *testing.T) {
	calls := 0
	session := testSession(t, func(*http.Request) (*http.Response, error) {
		calls++
		if calls > 2 {
			t.Fatal("pagination did not stop")
		}
		page := make([]*discordgo.Message, messagePage)
		for i := range page {
			page[i] = &discordgo.Message{ID: fmt.Sprint(200 - i)}
		}
		body, _ := json.Marshal(page)
		return reply(string(body)), nil
	})
	if _, err := testChannels(session).CaptureMessages(context.Background(), "thread"); err == nil {
		t.Fatal("repeated page accepted")
	}
}

// TestEntryPanelReusesPanel posts a new panel only when Discord says the
// old one is gone, so a permission failure never duplicates it.
func TestEntryPanelReusesPanel(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
		sends  int
		fail   bool
	}{
		{"existing", 200, `{"id":"panel"}`, 0, false},
		{"deleted", 404, `{"code":10008,"message":"Unknown Message"}`, 1, false},
		{"forbidden", 403, `{"code":50013,"message":"Missing Permissions"}`, 0, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			edits, sends := 0, 0
			session := testSession(t, func(r *http.Request) (*http.Response, error) {
				switch r.Method {
				case http.MethodPatch:
					edits++
					return respond(test.status, test.body), nil
				case http.MethodPost:
					sends++
					var payload struct {
						Content    string            `json:"content"`
						Components []json.RawMessage `json:"components"`
					}
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Fatal(err)
					}
					if !strings.HasPrefix(payload.Content, "# Need a hand?") || len(payload.Components) != 1 {
						t.Fatalf("panel = %+v", payload)
					}
					return reply(`{"id":"replacement"}`), nil
				}
				t.Fatalf("unexpected method %s", r.Method)
				return nil, nil
			})
			id, err := testChannels(session).publishEntryPanel(context.Background(), Settings{EntryChannelDiscordID: "entry", EntryPanelChannelID: "entry", EntryPanelMessageID: "panel"})
			if (err != nil) != test.fail || edits != 1 || sends != test.sends {
				t.Fatalf("edits = %d, sends = %d, id = %q, err = %v", edits, sends, id, err)
			}
		})
	}
}

// TestEntryPanelMoveRetiresOldPanelFirst keeps the old reference on a
// failed retirement, and leaves the old panel without controls.
func TestEntryPanelMoveRetiresOldPanelFirst(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		status   int
		body     string
		wantSend bool
	}{
		{"retired", 200, `{"id":"old-panel"}`, true},
		{"message deleted", 404, `{"code":10008,"message":"Unknown Message"}`, true},
		{"channel deleted", 404, `{"code":10003,"message":"Unknown Channel"}`, true},
		{"permission lost", 403, `{"code":50013,"message":"Missing Permissions"}`, false},
		{"unavailable", 503, `{"message":"Unavailable"}`, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var requests []string
			session := testSession(t, func(r *http.Request) (*http.Response, error) {
				requests = append(requests, r.Method+" "+r.URL.Path)
				switch r.Method {
				case http.MethodPatch:
					if !strings.HasSuffix(r.URL.Path, "/channels/old-entry/messages/old-panel") {
						t.Fatal("edited the wrong panel")
					}
					var payload struct {
						Content    string            `json:"content"`
						Components []json.RawMessage `json:"components"`
					}
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Fatal(err)
					}
					if payload.Content != "Tickets have moved to <#new-entry>. Open a ticket there." || payload.Components == nil || len(payload.Components) != 0 {
						t.Fatalf("retired panel = %+v", payload)
					}
					return respond(scenario.status, scenario.body), nil
				case http.MethodPost:
					if len(requests) != 2 || !strings.HasSuffix(r.URL.Path, "/channels/new-entry/messages") {
						t.Fatal("posted before retiring the old panel")
					}
					return reply(`{"id":"new-panel"}`), nil
				}
				t.Fatalf("unexpected request %s", r.Method)
				return nil, nil
			})
			_, err := testChannels(session).publishEntryPanel(context.Background(), Settings{EntryChannelDiscordID: "new-entry", EntryPanelChannelID: "old-entry", EntryPanelMessageID: "old-panel"})
			if (err == nil) != scenario.wantSend || (len(requests) == 2) != scenario.wantSend {
				t.Fatalf("requests = %v, err = %v", requests, err)
			}
		})
	}
}

// TestBotPermissionsUseLiveChannelState catches a channel overwrite even
// when the gateway cache still says Quack is an administrator.
func TestBotPermissionsUseLiveChannelState(t *testing.T) {
	all := int64(discordgo.PermissionViewChannel | discordgo.PermissionSendMessages | discordgo.PermissionReadMessageHistory |
		discordgo.PermissionCreatePrivateThreads | discordgo.PermissionSendMessagesInThreads | discordgo.PermissionManageThreads | discordgo.PermissionAttachFiles)
	for _, test := range []struct {
		name, channel, want string
		deny                int64
	}{
		{name: "ready"},
		{name: "cannot close threads", channel: "entry", deny: discordgo.PermissionManageThreads, want: "Manage Threads"},
		{name: "cannot attach transcripts", channel: "queue", deny: discordgo.PermissionAttachFiles, want: "Attach Files"},
	} {
		t.Run(test.name, func(t *testing.T) {
			session := testSession(t, func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodGet {
					t.Fatalf("permission check changed Discord: %s", r.Method)
				}
				switch {
				case strings.HasSuffix(r.URL.Path, "/guilds/guild"):
					return reply(fmt.Sprintf(`{"id":"guild","owner_id":"owner","roles":[{"id":"guild","permissions":"%d"}]}`, all)), nil
				case strings.HasSuffix(r.URL.Path, "/members/bot"):
					return reply(`{"user":{"id":"bot"},"roles":[]}`), nil
				case strings.Contains(r.URL.Path, "/channels/"):
					id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
					deny := int64(0)
					if id == test.channel {
						deny = test.deny
					}
					return reply(fmt.Sprintf(`{"id":"%s","guild_id":"guild","type":0,"permission_overwrites":[{"id":"bot","type":1,"deny":"%d","allow":"0"}]}`, id, deny)), nil
				}
				t.Fatalf("unexpected request %s", r.URL.Path)
				return nil, nil
			})
			if err := session.State.GuildAdd(&discordgo.Guild{ID: "guild", Roles: []*discordgo.Role{{ID: "guild", Permissions: discordgo.PermissionAdministrator}}}); err != nil {
				t.Fatal(err)
			}
			err := testChannels(session).checkBotPermissions(context.Background(), "guild", "entry", "queue")
			if test.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			message, ok := discord.UserMessage(err)
			if !ok || !strings.Contains(message, test.want) || !strings.Contains(message, "<#"+test.channel+">") {
				t.Fatalf("guidance = %v", err)
			}
		})
	}
}

// TestPublishQueueEditKeepsOnlyTheNewTranscript checks each transcript
// edit lists only its own upload, so retries never pile up files.
func TestPublishQueueEditKeepsOnlyTheNewTranscript(t *testing.T) {
	updates := 0
	session := testSession(t, func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/channels/queue"):
			return reply(fmt.Sprintf(`{"id":"queue","guild_id":"guild","type":0,"permission_overwrites":[{"id":"guild","type":0,"deny":"%d","allow":"0"}]}`, discordgo.PermissionViewChannel)), nil
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/guilds/guild"):
			return reply(`{"id":"guild","roles":[]}`), nil
		case r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/channels/queue/messages/posted"):
			updates++
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = r.MultipartForm.RemoveAll() }()
			var payload struct {
				Content     string                           `json:"content"`
				Attachments *[]struct{ ID, Filename string } `json:"attachments"`
			}
			if err := json.Unmarshal([]byte(r.FormValue("payload_json")), &payload); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(payload.Content, "The ticket for <@owner> was closed. The transcript is attached.") {
				t.Fatalf("content = %q", payload.Content)
			}
			if payload.Attachments == nil || len(*payload.Attachments) != 1 || (*payload.Attachments)[0].ID != "0" || (*payload.Attachments)[0].Filename != "ticket-ticket.txt" {
				t.Fatalf("attachments = %s", r.FormValue("payload_json"))
			}
			files := r.MultipartForm.File["files[0]"]
			if len(files) != 1 || len(r.MultipartForm.File) != 1 {
				t.Fatalf("files = %v", r.MultipartForm.File)
			}
			file, err := files[0].Open()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = file.Close() }()
			if content, err := io.ReadAll(file); err != nil || string(content) != "canonical transcript" {
				t.Fatalf("upload = %q, %v", content, err)
			}
			return reply(fmt.Sprintf(`{"id":"posted","attachments":[{"id":"prior-%d","filename":"ticket-ticket.txt"}]}`, updates)), nil
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		return nil, nil
	})
	ticket := &Ticket{ID: "ticket", GuildID: "internal", OwnerDiscordUserID: "owner", LogChannelDiscordID: "queue", LogMessageDiscordID: "posted"}
	for range 3 {
		receipt, err := testChannels(session).PublishQueue(context.Background(), ticket, Settings{QueueChannelDiscordID: "queue"}, &Transcript{Content: "canonical transcript"})
		if err != nil || receipt.MessageID != "posted" || receipt.URL != "https://discord.com/channels/guild/queue/posted" {
			t.Fatalf("receipt = %+v, %v", receipt, err)
		}
	}
	if updates != 3 {
		t.Fatalf("updates = %d", updates)
	}
}

func TestDefinitelyRefusedOnlyMarksDefiniteRefusals(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 429, 500, 502} {
		if definitelyRefused(&discordgo.RESTError{Response: &http.Response{StatusCode: status}}) != (status < 500) {
			t.Fatalf("status %d misclassified", status)
		}
	}
	if definitelyRefused(errors.New("connection lost")) {
		t.Fatal("uncertain send marked refused")
	}
}
