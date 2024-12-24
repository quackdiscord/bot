package events

import (
	"fmt"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
	"github.com/quackdiscord/bot/utils"
)

func init() {
	Events = append(Events, onMemberLeave)
}

func onMemberLeave(s *discordgo.Session, m *discordgo.GuildMemberRemove) {
	member, err := s.GuildMember(m.GuildID, m.User.ID)
	if err != nil {
		return
	}

	services.EQ.Enqueue(services.Event{
		Type:    "member_leave",
		Data:    member,
		GuildID: m.GuildID,
	})
}

func memberLeaveHandler(e services.Event) error {
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
		Title:       "Member left",
		Color:       0x5865f2,
		Description: desc,
		Author: structs.EmbedAuthor{
			Name: member.User.Username,
			Icon: "https://cdn.discordapp.com/avatars/" + member.User.ID + member.Avatar + ".png",
		},
		Footer: structs.EmbedFooter{
			Text: fmt.Sprintf("User ID: %s", member.User.ID),
		},
		Thumbnail: structs.EmbedThumbnail{
			URL: "https://cdn.discordapp.com/emojis/1064442673806704672.webp",
		},
		Timestamp: time.Now().Format(time.RFC3339),
	}

	if len(embed.Description) > 4096 {
		embed.Description = embed.Description[:4096]
	}

	err = utils.SendWHEmbed(settings.MemberWebhookURL, embed)
	if err != nil {
		return err
	}

	return nil
}
