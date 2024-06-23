package events

import (
	"context"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/lib"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/structs"
)

func init() {
	Events = append(Events, onMessageDelete)
}

type MsgDelete struct {
	Type        string                         `json:"type"`
	ID          string                         `json:"id"`
	Author      structs.LogUser                `json:"author"`
	GuildID     string                         `json:"guild_id"`
	ChannelID   string                         `json:"channel"`
	Content     string                         `json:"content"`
	Attachments []*discordgo.MessageAttachment `json:"attachments"`
}

func onMessageDelete(s *discordgo.Session, m *discordgo.MessageDelete) {
	if m.ID == "" {
		return
	}

	// get the message from message cache
	services.CacheMutex.Lock()
	message, exists := services.MessageCache[m.ID]
	if !exists {
		services.CacheMutex.Unlock()
		return
	}

	data := MsgDelete{
		Type:        "message_delete",
		ID:          m.ID,
		Author:      structs.LogUser{ID: message.Author.ID, Username: message.Author.Username},
		GuildID:     message.GuildID,
		ChannelID:   message.ChannelID,
		Content:     message.Content,
		Attachments: message.Attachments,
	}

	// send the kafka message
	json, err := lib.ToJSONByteArr(data)
	if err != nil {
		return
	}

	services.Kafka.Produce(context.Background(), []byte(data.Type), json)

	// remove the message from the cache
	delete(services.MessageCache, m.ID)
	// remove the message id from the order slice
	// Also remove the ID from the order slice
	for i, id := range services.CacheOrder {
		if id == m.ID {
			services.CacheOrder = append(services.CacheOrder[:i], services.CacheOrder[:i+1]...)
			break
		}
	}
	services.CacheMutex.Unlock()
}
