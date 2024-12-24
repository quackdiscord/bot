package events

import (
	"fmt"
	"time"

	"github.com/bwmarrin/discordgo"
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
	if err != nil {
		return
	}

	services.EQ.Enqueue(services.Event{
		Type:    "member_join",
		Data:    member,
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

	member := e.Data.(*discordgo.Member)

	desc := fmt.Sprintf("**Member:** <@%s> (%s)", member.User.ID, member.User.Username)

	embed := structs.Embed{
		Title:       "Member joined",
		Color:       0xeb459e,
		Description: desc,
		Author: structs.EmbedAuthor{
			Name: member.User.Username,
			Icon: member.AvatarURL(""),
		},
		Footer: structs.EmbedFooter{
			Text: fmt.Sprintf("User ID: %s", member.User.ID),
		},
		Thumbnail: structs.EmbedThumbnail{
			URL: "https://cdn.discordapp.com/emojis/1064442704936828968.webp",
		},
		Timestamp: time.Now().Format(time.RFC3339),
	}

	// check the length of the description
	if len(embed.Description) > 4096 {
		embed.Description = embed.Description[:4096]
	}

	err = utils.SendWHEmbed(settings.MemberWebhookURL, embed)
	if err != nil {
		log.Error().Err(err).Msg("Failed to send member join webhook")
		return nil
	}

	return nil
}
