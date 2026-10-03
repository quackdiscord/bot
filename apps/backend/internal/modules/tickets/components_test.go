package tickets

import (
	"context"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

// staleStaffStore supplies the attribution writes live resolution makes.
type staleStaffStore struct{ quack.GuildStore }

func (staleStaffStore) UpsertGuild(context.Context, quack.UpsertGuildParams) (*quack.Guild, error) {
	return &quack.Guild{ULIDModel: quack.ULIDModel{ID: "internal-guild"}, DiscordGuildID: "guild"}, nil
}

func (staleStaffStore) UpsertStaffMember(context.Context, quack.UpsertStaffMemberParams) (*quack.StaffMember, error) {
	return &quack.StaffMember{DiscordUserID: "member"}, nil
}

// demotedDirectory reports a member whose Administrator role is gone,
// though the interaction still claims it.
type demotedDirectory struct {
	quack.GuildDirectory
	calls int
}

func (d *demotedDirectory) GuildAuthorization(context.Context, string, string, string) (*quack.DiscordGuildAuthorization, error) {
	d.calls++
	return &quack.DiscordGuildAuthorization{
		Guild: quack.DiscordBotGuild{ID: "guild", OwnerID: "owner"},
		Actor: quack.DiscordMemberAuthorization{DiscordUserID: "member", Present: true},
		Bot:   quack.DiscordMemberAuthorization{DiscordUserID: "bot", Present: true},
	}, nil
}

func TestInteractionActorUsesLiveAuthority(t *testing.T) {
	directory := &demotedDirectory{}
	m := &Module{staff: quack.NewGuildService(staleStaffStore{}, directory)}
	actor, err := m.actor(context.Background(), &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{GuildID: "guild", Member: &discordgo.Member{
			User: &discordgo.User{ID: "member"}, Permissions: discordgo.PermissionAdministrator,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if directory.calls != 1 || actor.CanManage || actor.CanModerate || actor.DiscordUserID != "member" {
		t.Fatalf("stale interaction granted authority: %+v after %d live lookups", actor, directory.calls)
	}
}
