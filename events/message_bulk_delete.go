package events

import (
	"context"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/lib"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/structs"
)

func init() {
	Events = append(Events, onMessageBulkDelete)
}

type MsgBulkDelete struct {
	Type      string        `json:"type"`
	GuildID   string        `json:"guild_id"`
	ChannelID string        `json:"channel"`
	Messages  []BulkMessage `json:"messages"`
}

type BulkMessage struct {
	ID          string                         `json:"id"`
	Author      structs.LogUser                `json:"author"`
	Content     string                         `json:"content"`
	Attachments []*discordgo.MessageAttachment `json:"attachments"`
}

func onMessageBulkDelete(s *discordgo.Session, m *discordgo.MessageDeleteBulk) {
	// lock the cache
	services.CacheMutex.Lock()

	data := MsgBulkDelete{
		Type:      "message_bulk_delete",
		GuildID:   m.GuildID,
		ChannelID: m.ChannelID,
		Messages:  []BulkMessage{},
	}

	// get the messages from message cache
	for _, id := range m.Messages {
		message, exists := services.MessageCache[id]
		if !exists {
			continue
		}

		data.Messages = append(data.Messages, BulkMessage{
			ID:          message.ID,
			Author:      structs.LogUser{ID: message.Author.ID, Username: message.Author.Username, AvatarURL: message.Author.AvatarURL("")},
			Content:     message.Content,
			Attachments: message.Attachments,
		})

		// remove the message from the cache and the order slice
		delete(services.MessageCache, id)
		for i, id := range services.CacheOrder {
			if id == message.ID {
				services.CacheOrder = append(services.CacheOrder[:i], services.CacheOrder[:i+1]...)
				break
			}
		}
	}

	// send the kafka message
	json, err := lib.ToJSONByteArr(data)
	if err != nil {
		return
	}

	services.Kafka.Produce(context.Background(), []byte(data.Type), json)

	// unlock the cache
	services.CacheMutex.Unlock()
}
