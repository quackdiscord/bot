package honeypot

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

func TestProjectionUsesCurrentMemberState(t *testing.T) {
	guild := &discordgo.Guild{
		ID: "guild", OwnerID: "owner",
		Roles: []*discordgo.Role{
			{ID: "guild", Permissions: discordgo.PermissionViewChannel},
			{ID: "staff", Permissions: discordgo.PermissionModerateMembers},
		},
	}
	channel := &discordgo.Channel{ID: "channel", GuildID: "guild"}
	member := &discordgo.Member{GuildID: "guild", User: &discordgo.User{ID: "author"}, Roles: []string{"staff"}}
	// The event claims a bot author; the live member is not one.
	event := &discordgo.MessageCreate{Message: &discordgo.Message{
		ID: "message", GuildID: "guild", ChannelID: "channel", Author: &discordgo.User{ID: "author", Bot: true},
	}}
	message, err := projectMessage("internal-guild", event, guild, channel, member, "quack")
	if err != nil {
		t.Fatal(err)
	}
	if message.GuildID != "internal-guild" || message.IsBot || !message.AuthorCanModerate ||
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

// Exemption follows guild-wide moderation authority, the same baseline as
// cases and appeals, whatever the trap channel's overwrites say.
func TestExemptionUsesGuildModerationAuthority(t *testing.T) {
	for _, test := range []struct {
		name                     string
		permissions, allow, deny int64
		owner, want              bool
	}{
		{name: "moderator", permissions: discordgo.PermissionModerateMembers, want: true},
		{name: "administrator", permissions: discordgo.PermissionAdministrator, want: true},
		{name: "owner", owner: true, want: true},
		{name: "manage server only", permissions: discordgo.PermissionManageGuild},
		{name: "ban only", permissions: discordgo.PermissionBanMembers},
		{name: "channel grant", allow: discordgo.PermissionModerateMembers},
		{name: "channel denial", permissions: discordgo.PermissionModerateMembers, deny: discordgo.PermissionModerateMembers, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			guild := &discordgo.Guild{ID: "guild", OwnerID: "owner", Roles: []*discordgo.Role{{ID: "guild"}, {ID: "role", Permissions: test.permissions}}}
			if test.owner {
				guild.OwnerID = "member"
			}
			channel := &discordgo.Channel{ID: "trap", GuildID: "guild", PermissionOverwrites: []*discordgo.PermissionOverwrite{
				{ID: "member", Type: discordgo.PermissionOverwriteTypeMember, Allow: test.allow, Deny: test.deny},
			}}
			member := &discordgo.Member{User: &discordgo.User{ID: "member"}, Roles: []string{"role"}}
			event := &discordgo.MessageCreate{Message: &discordgo.Message{ID: "message", GuildID: "guild", ChannelID: "trap", Author: member.User}}
			projection, err := projectMessage("internal", event, guild, channel, member, "bot")
			if err != nil || projection.AuthorCanModerate != test.want {
				t.Fatalf("exempt = %v, want %v (err %v)", projection.AuthorCanModerate, test.want, err)
			}
		})
	}
}

// warningPolicyStub supplies a template's outcomes.
type warningPolicyStub struct {
	actions []quack.ActionType
	err     error
}

func (s warningPolicyStub) UnattendedTemplateActions(context.Context, string, string) ([]quack.ActionType, error) {
	return s.actions, s.err
}

// The generated warning names the real punishment: never a ban for a
// timeout or a case-only level, and every outcome of an escalating rule.
func TestGeneratedWarningMatchesPolicy(t *testing.T) {
	for _, tc := range []struct {
		name    string
		actions []quack.ActionType
		want    string
	}{
		{"ban", []quack.ActionType{quack.ActionBanUser}, "will ban you from this server"},
		{"timeout", []quack.ActionType{quack.ActionTimeoutUser}, "will time you out"},
		{"kick", []quack.ActionType{quack.ActionKickUser}, "will kick you from this server"},
		{"warning", []quack.ActionType{quack.ActionSendDM}, "will send you a warning by DM"},
		{"case only", []quack.ActionType{""}, "will record a moderation case"},
		{"escalation", []quack.ActionType{quack.ActionTimeoutUser, quack.ActionBanUser}, "can time you out or ban you from this server"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, saved := range []string{"", legacyHoneypotWarning} {
				got, err := resolveHoneypotWarning(context.Background(), warningPolicyStub{actions: tc.actions}, "guild", Settings{WarningText: saved})
				if err != nil || !strings.Contains(got, tc.want) || !strings.HasPrefix(got, "# Do not post here\n") {
					t.Fatalf("got %q, %v", got, err)
				}
			}
		})
	}
}

// An admin's own warning survives a failed policy read; without one, Quack
// refuses to invent a punishment.
func TestCustomWarningSurvivesPolicyReadFailure(t *testing.T) {
	policy := warningPolicyStub{err: errors.New("unavailable")}
	custom := "# Custom warning\nPlease stay out."
	got, err := resolveHoneypotWarning(context.Background(), policy, "guild", Settings{WarningText: custom})
	if err != nil || got != custom {
		t.Fatalf("custom copy changed: %q, %v", got, err)
	}
	if _, err := resolveHoneypotWarning(context.Background(), policy, "guild", Settings{}); err == nil {
		t.Fatal("invented a punishment without the current policy")
	}
}

func TestWarningContentCountsIncidents(t *testing.T) {
	for count, want := range map[uint64]string{
		0: "# Warning\n\n-# 0 incidents caught.",
		1: "# Warning\n\n-# 1 incident caught.",
		7: "# Warning\n\n-# 7 incidents caught.",
	} {
		if got := honeypotWarningContent("# Warning", count); got != want {
			t.Errorf("count %d: got %q, want %q", count, got, want)
		}
	}
	if got := honeypotWarningContent("", 2); !strings.HasPrefix(got, "# Do not post here\n") {
		t.Errorf("empty warning: got %q", got)
	}
}

func TestGuildLocksSerializePerGuild(t *testing.T) {
	var locks guildLocks
	release, err := locks.lock(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	other, err := locks.lock(context.Background(), "b")
	if err != nil {
		t.Fatal("another guild waited:", err)
	}
	other()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := locks.lock(ctx, "a"); !errors.Is(err, context.Canceled) {
		t.Fatalf("held lock: err = %v, want context.Canceled", err)
	}
	release()
	again, err := locks.lock(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	again()
}
