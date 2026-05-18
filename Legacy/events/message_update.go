package events

import (
	"fmt"
	"time"

	dgo "github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
	"github.com/quackdiscord/bot/utils"
	"github.com/rs/zerolog/log"
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

	desc := fmt.Sprintf("**Author:** <@%s> (%s)", msg.Author.ID, msg.Author.ID)

	if msg.BeforeUpdate.Content != msg.Content {
		desc += fmt.Sprintf("\n\n**Before:**\n> \"*%s*\"\n\n**After:**\n> \"*%s*\"\n", msg.BeforeUpdate.Content, msg.Content)
	}

	msgLink := fmt.Sprintf("https://discord.com/channels/%s/%s/%s", msg.GuildID, msg.ChannelID, msg.ID)

	embed := structs.Embed{
		Title:       fmt.Sprintf("<:al_message_update:1065110917962022922> Message Edited in %s", msgLink),
		Color:       0x4ca99d,
		Description: desc,
		Author: structs.EmbedAuthor{
			Name: msg.Author.Username,
			Icon: msg.Author.AvatarURL(""),
		},
		Footer: structs.EmbedFooter{
			Text: fmt.Sprintf("Message ID: %s", msg.ID),
		},
		Timestamp: time.Now().Format(time.RFC3339),
	}

	// check the length of the description
	if len(embed.Description) > 4096 {
		embed.Description = embed.Description[:4096]
	}

	err = utils.SendWHEmbed(settings.MessageWebhookURL, embed)
	if err != nil {
		// if the error is 404, then accept the error and don't requeue the event
		if err.Error() == "unexpected response status: 404 Not Found" {
			return nil
		}

		log.Warn().AnErr("Failed to send message update webhook, requeueing event after delay", err)
		go func(ev services.Event) {
			time.Sleep(60 * time.Second)
			services.EQ.Enqueue(ev)
		}(e)
		return err
	}

	return nil
}
