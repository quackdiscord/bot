package honeypot

import (
	"context"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

type caseCreatorFake struct {
	guildID string
	input   quack.CaseInput
}

func (f *caseCreatorFake) CreateSystemHoneypot(_ context.Context, guildID string, input quack.CaseInput) (*quack.CaseResponse, error) {
	f.guildID, f.input = guildID, input
	return &quack.CaseResponse{ID: "case-1"}, nil
}

func TestCaseApplierKeepsTheNormalCaseEnvelope(t *testing.T) {
	creator := &caseCreatorFake{}
	applier := caseApplier{cases: creator}
	request := ApplyRequest{
		GuildID: "guild", TemplateID: "template", TargetDiscordUserID: "target",
		ContextChannelDiscordID: "channel", ContextMessageDiscordID: "message",
		ContextURL: "https://discord.com/channels/1/2/3", IdempotencyKey: "honeypot:guild:message",
		Source: SourceHoneypot, ActorType: ActorTypeSystem,
	}
	result, err := applier.ApplyHoneypotCase(context.Background(), request)
	if err != nil || result.CaseID != "case-1" {
		t.Fatalf("apply = %+v, %v", result, err)
	}
	want := quack.CaseInput{
		TemplateID: "template", TargetDiscordUserID: "target", Source: quack.CaseSourceHoneypot,
		ContextChannelDiscordID: "channel", ContextMessageDiscordID: "message",
		ContextURL: request.ContextURL, IdempotencyKey: request.IdempotencyKey,
	}
	if creator.guildID != "guild" || creator.input.TemplateID != want.TemplateID ||
		creator.input.TargetDiscordUserID != want.TargetDiscordUserID || creator.input.Source != want.Source ||
		creator.input.ContextChannelDiscordID != want.ContextChannelDiscordID ||
		creator.input.ContextMessageDiscordID != want.ContextMessageDiscordID ||
		creator.input.ContextURL != want.ContextURL || creator.input.IdempotencyKey != want.IdempotencyKey {
		t.Fatalf("case input = %q %+v, want %+v", creator.guildID, creator.input, want)
	}
	request.ActorDiscordUserID = "fabricated-staff"
	if _, err := applier.ApplyHoneypotCase(context.Background(), request); err == nil {
		t.Fatal("accepted a staff attribution")
	}
}

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
