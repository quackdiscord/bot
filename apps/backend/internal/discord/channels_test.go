package discord

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

func TestStaffChannelValidation(t *testing.T) {
	staffOnly := func() []*discordgo.PermissionOverwrite {
		return []*discordgo.PermissionOverwrite{
			{ID: "guild", Type: discordgo.PermissionOverwriteTypeRole, Deny: discordgo.PermissionViewChannel},
			{ID: "staff", Type: discordgo.PermissionOverwriteTypeRole, Allow: discordgo.PermissionViewChannel},
		}
	}
	memberView := func(userID string) *discordgo.PermissionOverwrite {
		return &discordgo.PermissionOverwrite{
			ID:    userID,
			Type:  discordgo.PermissionOverwriteTypeMember,
			Allow: discordgo.PermissionViewChannel,
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
			c.PermissionOverwrites = append(c.PermissionOverwrites, memberView("bot"))
		}},
		{name: "current moderator member", ok: true, member: &discordgo.Member{User: &discordgo.User{ID: "member"}, Roles: []string{"staff"}},
			change: func(c *discordgo.Channel, _ *discordgo.Guild) {
				c.PermissionOverwrites[1] = memberView("member")
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
				c.PermissionOverwrites[1] = memberView("member")
			}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			channel := &discordgo.Channel{
				ID:                   "channel",
				GuildID:              "guild",
				Type:                 discordgo.ChannelTypeGuildText,
				PermissionOverwrites: staffOnly(),
			}
			guild := &discordgo.Guild{ID: "guild", OwnerID: "owner", Roles: []*discordgo.Role{
				{ID: "guild"},
				{ID: "staff", Permissions: discordgo.PermissionModerateMembers},
			}}
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
			channel.PermissionOverwrites = []*discordgo.PermissionOverwrite{{
				ID:   "guild",
				Type: discordgo.PermissionOverwriteTypeRole,
				Deny: discordgo.PermissionViewChannel,
			}}
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
		_, err := bot.SendAuditMirror(context.Background(), quack.AuditMirrorMessage{
			DiscordGuildID:   "guild",
			ChannelDiscordID: "channel",
			Result:           quack.AuditResultSuccess,
		})
		if private && (err != nil || sends != 1) {
			t.Fatalf("private destination denied: sends=%d err=%v", sends, err)
		}
		if !private && (!errors.Is(err, quack.ErrAuditMirrorChannelUnavailable) || sends != 0) {
			t.Fatalf("public destination delivered: sends=%d err=%v", sends, err)
		}
	}
}
