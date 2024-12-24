package events

import (
	"fmt"
	"time"

	dgo "github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/log"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
	"github.com/quackdiscord/bot/utils"
)

func init() {
	Events = append(Events, onMsgUpdate)
}

func onMsgUpdate(s *dgo.Session, m *dgo.MessageUpdate) {
	if m.ID == "" || m.BeforeUpdate == nil || m.Author == nil {
		return
	}

	services.MsgCache.AddMessage(m.Message)

	services.EQ.Enqueue(services.Event{
		Type:    "message_update",
		Data:    m,
		GuildID: m.GuildID,
	})
}

func msgUpdateHandler(e services.Event) error {
	settings, err := storage.FindLogSettingsByID(e.GuildID)
	if err != nil {
		return err
	}

	if settings == nil || settings.MessageWebhookURL == "" {
		return nil
	}

	msg := e.Data.(*dgo.MessageUpdate)

	if len(msg.BeforeUpdate.Attachments) == 0 && msg.BeforeUpdate.Content == "" {
		return nil

	}

	desc := fmt.Sprintf("**Channel:** <#%s> (%s)\n**Author:** <@%s> (%s)", msg.ChannelID, msg.ChannelID, msg.Author.ID, msg.Author.Username)

	if msg.BeforeUpdate.Content != msg.Content {
		desc += fmt.Sprintf("\n\n**Content:** ```diff\n- %s\n+%s```", msg.BeforeUpdate.Content, msg.Content)
	}

	desc += fmt.Sprintf("\n[Jump to message](%s)", fmt.Sprintf("https://discord.com/channels/%s/%s/%s", msg.GuildID, msg.ChannelID, msg.ID))

	embed := structs.Embed{
		Title:       "Message edited",
		Color:       0x4ca99d,
		Description: desc,
		Author: structs.EmbedAuthor{
			Name: msg.Author.Username,
			Icon: msg.Author.AvatarURL(""),
		},
		Footer: structs.EmbedFooter{
			Text: fmt.Sprintf("Message ID: %s", msg.ID),
		},
		Thumbnail: structs.EmbedThumbnail{
			URL: "https://cdn.discordapp.com/emojis/1065110917962022922.webp",
		},
		Timestamp: time.Now().Format(time.RFC3339),
	}

	// check the length of the description
	if len(embed.Description) > 4096 {
		embed.Description = embed.Description[:4096]
	}

	err = utils.SendWHEmbed(settings.MessageWebhookURL, embed)
	if err != nil {
		log.Error().Err(err).Msg("Failed to send message edit webhook")
		return nil
	}

	return nil
}
