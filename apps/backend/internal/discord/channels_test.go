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
		// moderatorRoles are the guild's configured moderator roles.
		moderatorRoles []string
		ok             bool
	}{
		{name: "private", ok: true},
		{name: "moderator role without permissions", ok: true, moderatorRoles: []string{"staff"},
			change: func(_ *discordgo.Channel, g *discordgo.Guild) { g.Roles[1].Permissions = 0 }},
		{name: "moderate members beside moderator roles", ok: true, moderatorRoles: []string{"other"}},
		{name: "member with moderator role", ok: true, moderatorRoles: []string{"mods"},
			member: &discordgo.Member{User: &discordgo.User{ID: "member"}, Roles: []string{"mods"}},
			change: func(c *discordgo.Channel, g *discordgo.Guild) {
				c.PermissionOverwrites[1] = memberView("member")
				g.Roles = append(g.Roles, &discordgo.Role{ID: "mods"})
			}},
		{name: "member holding a deleted moderator role", moderatorRoles: []string{"mods"},
			member: &discordgo.Member{User: &discordgo.User{ID: "member"}, Roles: []string{"mods"}},
			change: func(c *discordgo.Channel, _ *discordgo.Guild) {
				c.PermissionOverwrites[1] = memberView("member")
			}},
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
			bot.StaffRoles = fixedStaffRoles{ModeratorRoleIDs: test.moderatorRoles}
			err := bot.ValidateStaffChannel(context.Background(), "guild", "channel")
			if (err == nil) != test.ok {
				t.Fatalf("ValidateStaffChannel = %v, want ok=%v", err, test.ok)
			}
		})
	}
}

// fixedStaffRoles is a StaffRoleSource with the same roles in every guild.
type fixedStaffRoles quack.StaffRoles

func (r fixedStaffRoles) GuildStaffRoles(context.Context, string) (quack.StaffRoles, error) {
	return quack.StaffRoles(r), nil
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

func TestGuildRolesForPickers(t *testing.T) {
	guild := &discordgo.Guild{ID: "guild", OwnerID: "owner", MfaLevel: discordgo.MfaLevelElevated, Roles: []*discordgo.Role{
		{ID: "guild", Name: "@everyone"},
		{ID: "30", Name: "Members", Position: 1},
		{ID: "20", Name: "Moderators", Position: 3, Color: 0xff0000},
		{ID: "21", Name: "Quack", Position: 2, Managed: true},
		{ID: "100", Name: "Helpers", Position: 2},
	}}
	bot := testBot(t, func(request *http.Request) (*http.Response, error) {
		if strings.HasSuffix(request.URL.Path, "/guilds/guild") {
			return jsonResponse(request, guild), nil
		}
		t.Fatalf("unexpected request %s", request.URL.Path)
		return nil, nil
	})
	roles, err := bot.GuildRoles(context.Background(), "guild")
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, role := range roles {
		order = append(order, role.ID)
	}
	if strings.Join(order, ",") != "20,21,100,30" || roles[0].Color != 0xff0000 || !roles[1].Managed {
		t.Fatalf("roles = %+v, want highest first without @everyone", roles)
	}
	if !botGuild(guild).MFARequired {
		t.Fatal("elevated MFA level not reported")
	}
}

// TestMemberRolesSkipDeletedRoles checks that a role deleted from the guild
// but still listed on a member, as state or REST may briefly report it,
// grants nothing and is not reported.
func TestMemberRolesSkipDeletedRoles(t *testing.T) {
	guild := &discordgo.Guild{ID: "guild", OwnerID: "owner", Roles: []*discordgo.Role{
		{ID: "guild"},
		{ID: "mods", Position: 2},
	}}
	member := &discordgo.Member{User: &discordgo.User{ID: "member"}, Roles: []string{"mods", "deleted"}}
	got := memberAuthorization(guild, member)
	if strings.Join(got.RoleIDs, ",") != "mods" || got.TopRolePosition != 2 {
		t.Fatalf("member = %+v, want only the role the guild still has", got)
	}
}
