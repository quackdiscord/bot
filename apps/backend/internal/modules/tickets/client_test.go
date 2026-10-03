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
// Discord.
type guildStore struct{ discordID string }

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

// testChannels returns a client for the Discord guild "guild".
func testChannels(session *discordgo.Session) channels {
	return channels{session: session, guilds: modules.NewGuilds(guildStore{discordID: "guild"})}
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
			case strings.HasSuffix(r.URL.Path, "/members/former-staff"):
				return reply(`{"user":{"id":"former-staff"},"roles":[]}`), nil
			case strings.HasSuffix(r.URL.Path, "/members/departed"):
				return respond(http.StatusNotFound, `{"code":10007,"message":"Unknown Member"}`), nil
			case strings.Contains(r.URL.Path, "/channels/thread/thread-members"):
				return reply(`[{"user_id":"owner"},{"user_id":"bot"},{"user_id":"former-staff"},{"user_id":"current-staff"},{"user_id":"departed"}]`), nil
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
		case r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/channels/queue/messages/adopted"):
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
			return reply(fmt.Sprintf(`{"id":"adopted","attachments":[{"id":"prior-%d","filename":"ticket-ticket.txt"}]}`, updates)), nil
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		return nil, nil
	})
	ticket := &Ticket{ID: "ticket", GuildID: "internal", OwnerDiscordUserID: "owner", LogChannelDiscordID: "queue", LogMessageDiscordID: "adopted"}
	for range 3 {
		receipt, err := testChannels(session).PublishQueue(context.Background(), ticket, Settings{QueueChannelDiscordID: "queue"}, &Transcript{Content: "canonical transcript"})
		if err != nil || receipt.MessageID != "adopted" || receipt.URL != "https://discord.com/channels/guild/queue/adopted" {
			t.Fatalf("receipt = %+v, %v", receipt, err)
		}
	}
	if updates != 3 {
		t.Fatalf("updates = %d", updates)
	}
}

func TestQueueSendErrorMarksOnlyDefiniteRefusals(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 429, 500, 502} {
		err := queueSendError(&discordgo.RESTError{Response: &http.Response{StatusCode: status}})
		if errors.Is(err, ErrQueueNotSent) != (status < 500) {
			t.Fatalf("status %d: %v", status, err)
		}
	}
	if errors.Is(queueSendError(errors.New("connection lost")), ErrQueueNotSent) {
		t.Fatal("uncertain send marked refused")
	}
}

func TestQueueMessageExistsOnlyTrustsDefiniteAbsence(t *testing.T) {
	for _, test := range []struct {
		name, body     string
		status         int
		exists, failed bool
	}{
		{"present", `{"id":"message"}`, 200, true, false},
		{"deleted message", `{"code":10008}`, 404, false, false},
		{"deleted channel", `{"code":10003}`, 404, false, false},
		{"denied", `{"code":50013}`, 403, false, true},
		{"unavailable", `{"message":"unavailable"}`, 503, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			session := testSession(t, func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/channels/queue/messages/message") {
					t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
				}
				return respond(test.status, test.body), nil
			})
			exists, err := testChannels(session).QueueMessageExists(context.Background(), "queue", "message")
			if exists != test.exists || (err != nil) != test.failed {
				t.Fatalf("exists = %v, %v", exists, err)
			}
		})
	}
}

// TestValidateQueueMessage accepts only this bot's queue post for this
// ticket in its recorded channel, with fresh reads and no writes.
func TestValidateQueueMessage(t *testing.T) {
	row := func(ids ...string) discordgo.ActionsRow {
		var buttons []discordgo.MessageComponent
		for _, id := range ids {
			buttons = append(buttons, discord.Button(id, "Button", discordgo.SecondaryButton, false))
		}
		return discord.Row(buttons...)
	}
	tests := []struct {
		name         string
		link         string
		mutate       func(*discordgo.Message)
		channelGuild string
		failurePath  string
		wantOK       bool
		wantNoReads  bool
	}{
		{name: "open queue post", wantOK: true},
		{name: "closed queue post", mutate: func(m *discordgo.Message) {
			m.Components = []discordgo.MessageComponent{row("ticket:view:v1:ticket-id")}
		}, wantOK: true},
		{name: "wrong link guild", link: "https://discord.com/channels/99/22/33", wantNoReads: true},
		{name: "wrong destination", link: "https://discord.com/channels/11/99/33", wantNoReads: true},
		{name: "lookalike host", link: "https://discord.com.attacker.test/channels/guild/22/33", wantNoReads: true},
		{name: "credentials", link: "https://secret@discord.com/channels/guild/22/33", wantNoReads: true},
		{name: "query", link: "https://discord.com/channels/11/22/33?secret=value", wantNoReads: true},
		{name: "wrong returned message", mutate: func(m *discordgo.Message) { m.ID = "99" }},
		{name: "wrong channel guild", channelGuild: "99"},
		{name: "wrong author", mutate: func(m *discordgo.Message) { m.Author.ID = "99" }},
		{name: "webhook", mutate: func(m *discordgo.Message) { m.WebhookID = "99" }},
		{name: "other ticket", mutate: func(m *discordgo.Message) {
			m.Components = []discordgo.MessageComponent{row("ticket:view:v1:other-ticket")}
		}},
		{name: "unsupported action", mutate: func(m *discordgo.Message) {
			m.Components = append(m.Components, row("ticket:repair:v1:ticket-id"))
		}},
		{name: "no controls", mutate: func(m *discordgo.Message) { m.Components = nil }},
		{name: "message unreadable", failurePath: "/channels/22/messages/33"},
		{name: "identity unavailable", failurePath: "/users/@me"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			message := &discordgo.Message{ID: "33", ChannelID: "22", Author: &discordgo.User{ID: "44", Bot: true},
				Components: []discordgo.MessageComponent{row("ticket:view:v1:ticket-id", "ticket:close:v1:ticket-id")}}
			if tt.mutate != nil {
				tt.mutate(message)
			}
			calls := 0
			session := testSession(t, func(r *http.Request) (*http.Response, error) {
				if r.Method != http.MethodGet {
					t.Fatalf("validation changed Discord: %s %s", r.Method, r.URL.Path)
				}
				calls++
				if tt.failurePath != "" && strings.HasSuffix(r.URL.Path, tt.failurePath) {
					return respond(http.StatusForbidden, `{"code":50001,"message":"Missing Access"}`), nil
				}
				var value any
				switch {
				case strings.HasSuffix(r.URL.Path, "/channels/22/messages/33"):
					value = struct {
						*discordgo.Message
						Components []discordgo.MessageComponent `json:"components"`
					}{message, message.Components}
				case strings.HasSuffix(r.URL.Path, "/channels/22"):
					guild := tt.channelGuild
					if guild == "" {
						guild = "11"
					}
					value = &discordgo.Channel{ID: "22", GuildID: guild, Type: discordgo.ChannelTypeGuildText}
				case strings.HasSuffix(r.URL.Path, "/users/@me"):
					value = &discordgo.User{ID: "44", Bot: true}
				default:
					t.Fatalf("unexpected read %s", r.URL.Path)
				}
				encoded, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				return reply(string(encoded)), nil
			})
			session.State.User = &discordgo.User{ID: "stale-bot"}
			link := tt.link
			if link == "" {
				link = "https://discord.com/channels/11/22/33"
			}
			ticket := &Ticket{ID: "ticket-id", GuildID: "internal", LogChannelDiscordID: "22"}
			client := channels{session: session, guilds: modules.NewGuilds(guildStore{discordID: "11"})}
			receipt, err := client.ValidateQueueMessage(context.Background(), ticket, link)
			switch {
			case tt.wantOK:
				if err != nil || receipt.MessageID != "33" || receipt.URL != "https://discord.com/channels/11/22/33" {
					t.Fatalf("valid post rejected: %+v, %v", receipt, err)
				}
			case tt.failurePath != "":
				if receipt != nil || !errors.Is(err, errQueueUnverifiable) {
					t.Fatalf("unreadable post: %+v, %v", receipt, err)
				}
			default:
				if receipt != nil || !errors.Is(err, ErrInvalidQueueReceipt) {
					t.Fatalf("invalid post accepted: %+v, %v", receipt, err)
				}
			}
			if tt.wantNoReads && calls != 0 {
				t.Fatalf("invalid link reached Discord %d times", calls)
			}
		})
	}
}
