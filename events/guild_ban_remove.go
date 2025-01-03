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
	Events = append(Events, onGuildBanRemove)
}

func onGuildBanRemove(s *discordgo.Session, m *discordgo.GuildBanRemove) {
	user, err := s.User(m.User.ID)
	if err != nil {
		return
	}

	services.EQ.Enqueue(services.Event{
		Type:    "guild_ban_remove",
		Data:    user,
		GuildID: m.GuildID,
	})
}

func guildBanRemoveHandler(e services.Event) error {
	// wait 5 seconds to make sure the case as been saved
	time.Sleep(5 * time.Second)

	settings, err := storage.FindLogSettingsByID(e.GuildID)
	if err != nil {
		return err
	}

	if settings == nil || settings.MemberWebhookURL == "" {
		return nil
	}

	user := e.Data.(*discordgo.User)

	desc := ""

	// get the latest case from this server
	c, err := storage.FindLatestCase(e.GuildID)
	if err != nil {
		return err
	}

	// check that the case is for this user and is an unban
	if c.UserID != user.ID || c.Type != 3 {
		c = nil
	}

	if c != nil {
		desc += fmt.Sprintf("\n\n**Reason:** `%s`\n**Moderator:** <@%s> (%s)\n**Case ID:** %s", c.Reason, c.ModeratorID, c.ModeratorID, c.ID)
	}

	embed := structs.Embed{
		Title:       fmt.Sprintf("<:al_member_add:1064442704936828968> Member unbanned - <@%s> (%s)", user.ID, user.ID),
		Color:       0x2c2f33,
		Description: desc,
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
