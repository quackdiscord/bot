package events

import (
	"fmt"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/lib"
	"github.com/quackdiscord/bot/log"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
	"github.com/quackdiscord/bot/utils"
)

func init() {
	Events = append(Events, onMemberJoin)
}

func onMemberJoin(s *discordgo.Session, m *discordgo.GuildMemberAdd) {
	member, err := s.GuildMember(m.GuildID, m.User.ID)
	guild, _ := s.GuildWithCounts(m.GuildID)
	if err != nil {
		return
	}

	services.EQ.Enqueue(services.Event{
		Type:    "member_join",
		Data:    []interface{}{member, guild},
		GuildID: m.GuildID,
	})
}

func memberJoinHandler(e services.Event) error {
	settings, err := storage.FindLogSettingsByID(e.GuildID)
	if err != nil {
		return err
	}

	if settings == nil || settings.MemberWebhookURL == "" {
		return nil
	}

	// Get the data array
	data := e.Data.([]interface{})

	// Assert the individual elements
	member := data[0].(*discordgo.Member)
	guild := data[1].(*discordgo.Guild)

	memberCreatedAt, err := lib.GetUserCreationTime(member.User.ID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get user creation time")
		return nil
	}

	desc := fmt.Sprintf("<@%s> (%s), member **#%d**\n-# Account created <t:%d:R>", member.User.ID, member.User.Username, guild.ApproximateMemberCount, memberCreatedAt.Unix())

	embed := structs.Embed{
		Title:       "<:al_member_add:1064442704936828968> Member joined",
		Color:       0xeb459e,
		Description: desc,
		Author: structs.EmbedAuthor{
			Name: member.User.Username,
			Icon: member.AvatarURL(""),
		},
		Footer: structs.EmbedFooter{
			Text: fmt.Sprintf("User ID: %s", member.User.ID),
		},
		Timestamp: time.Now().Format(time.RFC3339),
	}

	// check the length of the description
	if len(embed.Description) > 4096 {
		embed.Description = embed.Description[:4096]
	}

	err = utils.SendWHEmbed(settings.MemberWebhookURL, embed)
	if err != nil {
		// if the error is 404, then accept the error and don't requeue the event
		if err.Error() == "unexpected response status: 404 Not Found" {
			return nil
		}

		log.Error().Err(err).Msg("Failed to send memeber leave webhook, requeueing event after delay")
		go func(ev services.Event) {
			time.Sleep(60 * time.Second)
			services.EQ.Enqueue(ev)
		}(e)
		return err
	}

	return nil
}
