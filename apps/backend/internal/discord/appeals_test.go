package discord

import (
	"context"
	"errors"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

func TestAppealReversalRejectsInvalidControls(t *testing.T) {
	handler := appealReversal(quack.New(quack.Deps{}))
	member := &discordgo.Member{User: &discordgo.User{ID: "mod"}}
	tests := []struct {
		name     string
		customID string
		member   *discordgo.Member
		want     string
	}{
		{"no member", "appeal:reverse:v1:appeal,exec,unban_user", nil, "This reversal control is unavailable."},
		{"short payload", "appeal:reverse:v1:appeal,exec", member, "This reversal control is invalid."},
		{"blank appeal", "appeal:reverse:v1: ,exec,unban_user", member, "This reversal control is invalid."},
		{"not a reversal", "appeal:reverse:v1:appeal,exec,ban_user", member, "This reversal type is invalid."},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			i := componentInteraction("interaction-1", test.customID)
			i.Member = test.member
			result := handler(context.Background(), i)
			if result.Task != nil || len(result.Response.Data.Embeds) != 1 || result.Response.Data.Embeds[0].Description != test.want {
				t.Fatalf("got %+v", result.Response.Data)
			}
		})
	}
	valid := componentInteraction("interaction-1", "appeal:reverse:v1:appeal,exec,unban_user")
	valid.Member = member
	if result := handler(context.Background(), valid); result.Task == nil || result.Response.Data.Flags&discordgo.MessageFlagsEphemeral == 0 {
		t.Fatalf("valid control was not deferred privately: %+v", result)
	}
}

type appealSettingsStore struct{}

func (appealSettingsStore) GetGuildSettings(context.Context, string) (*quack.GuildSettings, error) {
	return &quack.GuildSettings{AuditMirrorChannelDiscordID: "channel"}, nil
}

func (appealSettingsStore) GetGuildByID(context.Context, string) (*quack.Guild, error) {
	return &quack.Guild{DiscordGuildID: "discord-guild"}, nil
}

type rejectingStaffChannel struct{ guildID, channelID string }

func (v *rejectingStaffChannel) ValidateStaffChannel(_ context.Context, guildID, channelID string) error {
	v.guildID, v.channelID = guildID, channelID
	return errors.New("destination is public")
}

func TestAppealStaffChannelIsRevalidated(t *testing.T) {
	validator := &rejectingStaffChannel{}
	channels := appealChannels{store: appealSettingsStore{}, validator: validator}
	if channel, err := channels.staffChannel(context.Background(), "internal-guild"); err == nil || channel != "" {
		t.Fatalf("public appeal destination accepted: %q, %v", channel, err)
	}
	if validator.guildID != "discord-guild" || validator.channelID != "channel" {
		t.Fatalf("validated the wrong destination: %+v", validator)
	}
}
