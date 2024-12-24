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
	Events = append(Events, onMsgDelete)
}

func onMsgDelete(s *dgo.Session, m *dgo.MessageDelete) {
	if m.ID == "" {
		return
	}

	// get the message from message cache
	message, exists := services.MsgCache.GetMessage(m.ID)
	if !exists {
		return
	}

	services.EQ.Enqueue(services.Event{
		Type:    "message_delete",
		Data:    message,
		GuildID: m.GuildID,
	})
}

func msgDeleteHandler(e services.Event) error {
	settings, err := storage.FindLogSettingsByID(e.GuildID)
	if err != nil {
		return err
	}

	if settings == nil || settings.MessageWebhookURL == "" {
		return nil
	}

	msg := e.Data.(*services.CachedMessage)

	if len(msg.Attachments) == 0 && msg.Content == "" {
		return nil
	}

	desc := fmt.Sprintf("**Channel:** <#%s> (%s)\n**Author:** <@%s> (%s)", msg.ChannelID, msg.ChannelID, msg.Author.ID, msg.Author.ID)
	if msg.Content != "" {
		desc += fmt.Sprintf("\n\n**Content:** ```%s```", msg.Content)
	}

	if len(msg.Attachments) > 0 {
		desc += "\n\n**Attachments:**"
		for _, attachment := range msg.Attachments {
			desc += fmt.Sprintf("\n- [%s](%s)", attachment.Filename, attachment.URL)
		}
	}

	embed := structs.Embed{
		Title:       "Message deleted",
		Color:       0x914444,
		Description: desc,
		Author: structs.EmbedAuthor{
			Name: msg.Author.Username,
			Icon: msg.Author.AvatarURL(""),
		},
		Footer: structs.EmbedFooter{
			Text: fmt.Sprintf("Message ID: %s", msg.ID),
		},
		Thumbnail: structs.EmbedThumbnail{
			URL: "https://cdn.discordapp.com/emojis/1064444110334861373.webp",
		},
		Timestamp: time.Now().Format(time.RFC3339),
	}

	// check the length of the description
	if len(embed.Description) > 4096 {
		embed.Description = embed.Description[:4096]
	}

	err = utils.SendWHEmbed(settings.MessageWebhookURL, embed)
	if err != nil {
		log.Error().Err(err).Msg("Failed to send message delete webhook")
		return nil
	}

	return nil
}
