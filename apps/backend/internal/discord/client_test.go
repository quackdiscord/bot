package discord

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

// roundTripper serves Discord REST fixtures without a network listener.
type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func jsonResponse(request *http.Request, body any) *http.Response {
	encoded, _ := json.Marshal(body)
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(encoded))), Request: request}
}

// testBot returns a bot whose REST calls are answered by serve.
func testBot(t *testing.T, serve func(*http.Request) (*http.Response, error)) *Bot {
	t.Helper()
	bot, err := New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	bot.Session.State.User = &discordgo.User{ID: "bot"}
	bot.Session.Client = &http.Client{Transport: roundTripper(serve)}
	return bot
}

func TestStatusTracksGateway(t *testing.T) {
	bot, err := New("token")
	if err != nil {
		t.Fatal(err)
	}
	bot.Session.State.User = &discordgo.User{ID: "bot-1", Username: "quack"}
	steps := []struct {
		event any
		want  bool
	}{
		{&discordgo.Ready{}, true},
		{&discordgo.Disconnect{}, false},
		{&discordgo.Resumed{}, true},
	}
	for _, step := range steps {
		bot.trackGateway(bot.Session, step.event)
		if connected, _, _ := bot.Status(); connected != step.want {
			t.Fatalf("after %T: connected=%v, want %v", step.event, connected, step.want)
		}
	}
}

func TestMemberAuthorizationCalculatesPermissionsAndHierarchy(t *testing.T) {
	guild := &discordgo.Guild{
		ID: "guild", OwnerID: "owner",
		Roles: []*discordgo.Role{
			{ID: "guild", Position: 0, Permissions: discordgo.PermissionViewChannel},
			{ID: "moderator", Position: 10, Permissions: discordgo.PermissionModerateMembers | discordgo.PermissionKickMembers},
			{ID: "administrator", Position: 5, Permissions: discordgo.PermissionAdministrator},
		},
	}
	moderator := memberAuthorization(guild, &discordgo.Member{User: &discordgo.User{ID: "mod", Username: "Moderator"}, Roles: []string{"moderator"}})
	want := uint64(discordgo.PermissionViewChannel | discordgo.PermissionModerateMembers | discordgo.PermissionKickMembers)
	if !moderator.Present || moderator.Bot || moderator.TopRolePosition != 10 || moderator.PermissionBits&want != want {
		t.Fatalf("moderator = %+v", moderator)
	}
	admin := memberAuthorization(guild, &discordgo.Member{User: &discordgo.User{ID: "admin", Bot: true}, Roles: []string{"administrator"}})
	if !admin.Bot || admin.PermissionBits&uint64(discordgo.PermissionBanMembers) == 0 {
		t.Fatalf("administrator was not expanded: %+v", admin)
	}
	owner := memberAuthorization(guild, &discordgo.Member{User: &discordgo.User{ID: "owner"}})
	if owner.PermissionBits&uint64(discordgo.PermissionAdministrator) == 0 {
		t.Fatalf("owner was not expanded: %+v", owner)
	}
}

func TestGuildAuthorizationMapsMissingGuild(t *testing.T) {
	for status, want := range map[int]error{
		http.StatusForbidden:           quack.ErrBotNotInGuild,
		http.StatusNotFound:            quack.ErrBotNotInGuild,
		http.StatusInternalServerError: quack.ErrAuthorizationUnavailable,
	} {
		bot := testBot(t, func(request *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: request}, nil
		})
		if _, err := bot.GuildAuthorization(context.Background(), "guild", "actor", ""); !errors.Is(err, want) {
			t.Errorf("status %d: got %v, want %v", status, err, want)
		}
	}
}

func TestClassifyRedactsAndProtectsIrreversibleOutcomes(t *testing.T) {
	tests := []struct {
		name                 string
		status               int
		irreversible         bool
		code                 string
		retryable, uncertain bool
	}{
		{"validation", 400, false, "validation_failed", false, false},
		{"permission", 403, false, "permission_or_hierarchy_denied", false, false},
		{"unknown", 404, false, "unknown_member_or_resource", false, false},
		{"rate", 429, true, "rate_limited", true, false},
		{"safe server", 500, false, "discord_server_error", true, false},
		{"uncertain ban", 500, true, "discord_server_error", false, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := &discordgo.RESTError{Response: &http.Response{StatusCode: test.status}}
			var classified quack.DiscordError
			if !errors.As(classify("ban", source, test.irreversible), &classified) {
				t.Fatal("not classified")
			}
			if classified.Retryable != test.retryable || classified.OutcomeUncertain != test.uncertain ||
				classified.Message == source.Error() || classified.Code != "ban_"+test.code {
				t.Fatalf("unexpected classification: %+v", classified)
			}
		})
	}
}

func TestEnforcementCarriesContextAndDoesNotRetry(t *testing.T) {
	type key struct{}
	ctx := context.WithValue(context.Background(), key{}, "trace")
	calls := 0
	bot := testBot(t, func(request *http.Request) (*http.Response, error) {
		calls++
		if request.Context().Value(key{}) != "trace" {
			t.Error("request lost its context")
		}
		return &http.Response{StatusCode: http.StatusBadGateway, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"message":"upstream unavailable"}`)), Request: request}, nil
	})
	_, err := bot.BanMember(ctx, "guild", "member", 0, "case")
	var classified quack.DiscordError
	if calls != 1 || !errors.As(err, &classified) || !classified.OutcomeUncertain || classified.Retryable {
		t.Fatalf("expected one uncertain attempt: calls=%d error=%+v", calls, err)
	}
}

func TestEvidenceDownloadRejectsUnsafeSourcesBeforeUpload(t *testing.T) {
	calls := 0
	bot := testBot(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("an upload was attempted")
		return nil, nil
	})
	bot.httpClient = &http.Client{Transport: roundTripper(func(request *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("too many bytes")), Request: request}, nil
	})}
	ctx := context.Background()
	_, err := bot.PreserveEvidenceAttachment(ctx, "guild", "channel", quack.DiscordAttachmentSnapshot{URL: "https://cdn.discordapp.com/attachments/1/2/file.png", SizeBytes: 3})
	if err == nil || calls != 1 {
		t.Fatalf("size mismatch was accepted: calls=%d err=%v", calls, err)
	}
	for _, raw := range []string{
		"http://cdn.discordapp.com/attachments/1/2/x",
		"https://localhost/attachments/1/2/x",
		"https://cdn.discordapp.com.evil.test/attachments/x",
		"https://user:secret@cdn.discordapp.com/attachments/x",
	} {
		if _, err := bot.PreserveEvidenceAttachment(ctx, "guild", "channel", quack.DiscordAttachmentSnapshot{URL: raw, SizeBytes: 3}); err == nil || calls != 1 {
			t.Fatalf("unsafe download: %s", raw)
		}
	}
}

func TestEvidenceChecksModeratorAccessBeforeBotRead(t *testing.T) {
	allowed, reads := false, 0
	guild := &discordgo.Guild{ID: "guild", OwnerID: "owner", Roles: []*discordgo.Role{
		{ID: "guild", Permissions: discordgo.PermissionViewChannel | discordgo.PermissionReadMessageHistory},
		{ID: "staff", Permissions: discordgo.PermissionModerateMembers},
	}}
	bot := testBot(t, func(request *http.Request) (*http.Response, error) {
		var body any
		switch path := request.URL.Path; {
		case strings.HasSuffix(path, "/channels/channel"):
			channel := &discordgo.Channel{ID: "channel", GuildID: "guild", Type: discordgo.ChannelTypeGuildText}
			if !allowed {
				channel.PermissionOverwrites = []*discordgo.PermissionOverwrite{{ID: "moderator", Type: discordgo.PermissionOverwriteTypeMember, Deny: discordgo.PermissionViewChannel}}
			}
			body = channel
		case strings.HasSuffix(path, "/guilds/guild"):
			body = guild
		case strings.HasSuffix(path, "/members/moderator"):
			body = &discordgo.Member{User: &discordgo.User{ID: "moderator"}, Roles: []string{"staff"}}
		case strings.HasSuffix(path, "/messages/message"):
			reads++
			body = &discordgo.Message{ID: "message", ChannelID: "channel", GuildID: "guild", Author: &discordgo.User{ID: "target"}}
		default:
			t.Fatalf("unexpected request %s", path)
		}
		return jsonResponse(request, body), nil
	})
	// Permissive gateway data must not grant access.
	_ = bot.Session.State.GuildAdd(&discordgo.Guild{ID: "guild", OwnerID: "moderator"})
	ref := quack.DiscordMessageReference{GuildID: "guild", ChannelID: "channel", MessageID: "message", ActorDiscordUserID: "moderator"}
	if _, err := bot.FetchMessageEvidence(context.Background(), ref); err == nil || reads != 0 {
		t.Fatal("bot read inaccessible evidence")
	}
	allowed = true
	if _, err := bot.FetchMessageEvidence(context.Background(), ref); err != nil || reads != 1 {
		t.Fatalf("readable evidence failed: %v", err)
	}
}

func TestStaffChannelValidation(t *testing.T) {
	staffOnly := func() []*discordgo.PermissionOverwrite {
		return []*discordgo.PermissionOverwrite{
			{ID: "guild", Type: discordgo.PermissionOverwriteTypeRole, Deny: discordgo.PermissionViewChannel},
			{ID: "staff", Type: discordgo.PermissionOverwriteTypeRole, Allow: discordgo.PermissionViewChannel},
		}
	}
	tests := []struct {
		name   string
		change func(*discordgo.Channel, *discordgo.Guild)
		member *discordgo.Member
		ok     bool
	}{
		{name: "private", ok: true},
		{name: "bot member", ok: true, change: func(c *discordgo.Channel, _ *discordgo.Guild) {
			c.PermissionOverwrites = append(c.PermissionOverwrites, &discordgo.PermissionOverwrite{ID: "bot", Type: discordgo.PermissionOverwriteTypeMember, Allow: discordgo.PermissionViewChannel})
		}},
		{name: "current moderator member", ok: true, member: &discordgo.Member{User: &discordgo.User{ID: "member"}, Roles: []string{"staff"}},
			change: func(c *discordgo.Channel, _ *discordgo.Guild) {
				c.PermissionOverwrites[1] = &discordgo.PermissionOverwrite{ID: "member", Type: discordgo.PermissionOverwriteTypeMember, Allow: discordgo.PermissionViewChannel}
			}},
		{name: "cross guild", change: func(c *discordgo.Channel, _ *discordgo.Guild) { c.GuildID = "other" }},
		{name: "voice channel", change: func(c *discordgo.Channel, _ *discordgo.Guild) { c.Type = discordgo.ChannelTypeGuildVoice }},
		{name: "public", change: func(c *discordgo.Channel, _ *discordgo.Guild) { c.PermissionOverwrites = nil }},
		{name: "everyone allowed", change: func(c *discordgo.Channel, _ *discordgo.Guild) {
			c.PermissionOverwrites[0].Allow = discordgo.PermissionViewChannel
		}},
		{name: "non-staff role", change: func(_ *discordgo.Channel, g *discordgo.Guild) { g.Roles[1].Permissions = 0 }},
		{name: "manage guild only role", change: func(_ *discordgo.Channel, g *discordgo.Guild) {
			g.Roles[1].Permissions = discordgo.PermissionManageGuild
		}},
		{name: "demoted member", member: &discordgo.Member{User: &discordgo.User{ID: "member"}},
			change: func(c *discordgo.Channel, _ *discordgo.Guild) {
				c.PermissionOverwrites[1] = &discordgo.PermissionOverwrite{ID: "member", Type: discordgo.PermissionOverwriteTypeMember, Allow: discordgo.PermissionViewChannel}
			}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			channel := &discordgo.Channel{ID: "channel", GuildID: "guild", Type: discordgo.ChannelTypeGuildText, PermissionOverwrites: staffOnly()}
			guild := &discordgo.Guild{ID: "guild", OwnerID: "owner", Roles: []*discordgo.Role{{ID: "guild"}, {ID: "staff", Permissions: discordgo.PermissionModerateMembers}}}
			if test.change != nil {
				test.change(channel, guild)
			}
			bot := testBot(t, func(request *http.Request) (*http.Response, error) {
				switch path := request.URL.Path; {
				case strings.HasSuffix(path, "/channels/channel"):
					return jsonResponse(request, channel), nil
				case strings.HasSuffix(path, "/guilds/guild"):
					return jsonResponse(request, guild), nil
				case strings.Contains(path, "/members/") && test.member != nil:
					return jsonResponse(request, test.member), nil
				}
				t.Fatalf("unexpected request %s", request.URL.Path)
				return nil, nil
			})
			err := bot.ValidateStaffChannel(context.Background(), "guild", "channel")
			if (err == nil) != test.ok {
				t.Fatalf("ValidateStaffChannel = %v, want ok=%v", err, test.ok)
			}
		})
	}
}

func TestAuditMirrorSendsOnlyToStaffChannels(t *testing.T) {
	for _, private := range []bool{true, false} {
		sends := 0
		channel := &discordgo.Channel{ID: "channel", GuildID: "guild", Type: discordgo.ChannelTypeGuildText}
		if private {
			channel.PermissionOverwrites = []*discordgo.PermissionOverwrite{{ID: "guild", Type: discordgo.PermissionOverwriteTypeRole, Deny: discordgo.PermissionViewChannel}}
		}
		bot := testBot(t, func(request *http.Request) (*http.Response, error) {
			switch {
			case request.Method == http.MethodPost:
				sends++
				return jsonResponse(request, map[string]string{"id": "message"}), nil
			case strings.HasSuffix(request.URL.Path, "/channels/channel"):
				return jsonResponse(request, channel), nil
			default:
				return jsonResponse(request, &discordgo.Guild{ID: "guild"}), nil
			}
		})
		err := bot.SendAuditMirror(context.Background(), quack.AuditMirrorMessage{DiscordGuildID: "guild", ChannelDiscordID: "channel", Result: quack.AuditResultSuccess})
		if private && (err != nil || sends != 1) {
			t.Fatalf("private destination denied: %v", err)
		}
		if !private && (!errors.Is(err, quack.ErrAuditMirrorChannelUnavailable) || sends != 0) {
			t.Fatalf("public destination delivered: sends=%d err=%v", sends, err)
		}
	}
}
