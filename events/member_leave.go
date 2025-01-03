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
	user, err := s.User(m.User.ID)
	if err != nil {
		return
	}

	services.EQ.Enqueue(services.Event{
		Type:    "member_leave",
		Data:    user,
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

	user := e.Data.(*discordgo.User)

	embed := structs.Embed{
		Title:       "<:al_member_leave:1064442673806704672> Member left",
		Color:       0x5865f2,
		Description: fmt.Sprintf("<@%s> (%s)", user.ID, user.Username),
		Author: structs.EmbedAuthor{
			Name: user.Username,
			Icon: user.AvatarURL(""),
		},
		Footer: structs.EmbedFooter{
			Text: fmt.Sprintf("User ID: %s", user.ID),
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
