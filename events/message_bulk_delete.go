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
	Events = append(Events, onMsgBulkDelete)
}

func onMsgBulkDelete(s *dgo.Session, m *dgo.MessageDeleteBulk) {
	// get the messages from the cache
	msgs := make([]*services.CachedMessage, len(m.Messages))
	for i, id := range m.Messages {
		msg, exists := services.MsgCache.GetMessage(id)
		if !exists {
			continue
		}

		msgs[i] = msg
	}

	services.EQ.Enqueue(services.Event{
		Type:    "message_bulk_delete",
		Data:    msgs,
		GuildID: m.GuildID,
	})
}

func msgBulkDeleteHandler(e services.Event) error {
	settings, err := storage.FindLogSettingsByID(e.GuildID)
	if err != nil {
		return err
	}

	if settings == nil || settings.MessageWebhookURL == "" {
		return nil
	}

	msgs := e.Data.([]*services.CachedMessage)

	if len(msgs) == 0 {
		return nil
	}

	desc := fmt.Sprintf("**Channel:** <#%s> (%s)\n", msgs[0].ChannelID, msgs[0].ChannelID)

	for i, message := range msgs {
		if message.Content != "" || len(message.Attachments) > 0 {
			desc += fmt.Sprintf("\n%d. <@%s> (%s)", i, message.Author.ID, message.Author.Username)

			if message.Content != "" {
				desc += fmt.Sprintf(" - `%s`", message.Content)
			}

			if len(message.Attachments) > 0 {
				desc += "\n> **Attachments:**"
				for _, attachment := range message.Attachments {
					desc += fmt.Sprintf("\n> - [%s](%s)", attachment.Filename, attachment.URL)
				}
			}
		}
	}

	embed := structs.Embed{
		Title:       fmt.Sprintf("%d messages deleted", len(msgs)),
		Color:       0x373f69,
		Description: desc,
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
