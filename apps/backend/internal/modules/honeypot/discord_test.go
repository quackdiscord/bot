package honeypot

import (
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestProjectionUsesCurrentMemberRolesAndPermissions(t *testing.T) {
	guild := &discordgo.Guild{
		ID: "guild", OwnerID: "owner",
		Roles: []*discordgo.Role{
			{ID: "guild", Permissions: discordgo.PermissionViewChannel},
			{ID: "staff", Permissions: discordgo.PermissionModerateMembers},
		},
	}
	channel := &discordgo.Channel{ID: "channel", GuildID: "guild"}
	member := &discordgo.Member{GuildID: "guild", User: &discordgo.User{ID: "author"}, Roles: []string{"staff", "exempt"}}
	// The event claims a bot author; the live member is not one.
	event := &discordgo.MessageCreate{Message: &discordgo.Message{
		ID: "message", GuildID: "guild", ChannelID: "channel", Author: &discordgo.User{ID: "author", Bot: true},
	}}
	message, err := projectMessage("internal-guild", event, guild, channel, member, "quack")
	if err != nil {
		t.Fatal(err)
	}
	if message.GuildID != "internal-guild" || message.IsBot || !message.AuthorCanModerate ||
		len(message.AuthorRoleDiscordIDs) != 2 || message.AuthorRoleDiscordIDs[1] != "exempt" ||
		message.MessageURL != "https://discord.com/channels/guild/channel/message" {
		t.Fatalf("projection trusted the event over live state: %+v", message)
	}
	member.User.Bot = true
	message, _ = projectMessage("internal-guild", event, guild, channel, member, "author")
	if !message.IsBot || !message.IsQuack {
		t.Fatalf("live bot identity ignored: %+v", message)
	}
	if _, err := projectMessage("", event, guild, channel, member, "author"); err == nil {
		t.Fatal("projected a message without an internal guild")
	}
}
