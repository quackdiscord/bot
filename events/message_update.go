package events

import (
	"context"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/lib"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/structs"
)

func init() {
	Events = append(Events, onMessageUpdate)
}

type MsgUpdate struct {
	Type           string                         `json:"type"`
	ID             string                         `json:"id"`
	Author         structs.LogUser                `json:"author"`
	GuildID        string                         `json:"guild_id"`
	ChannelID      string                         `json:"channel"`
	OldContent     string                         `json:"old_content"`
	NewContent     string                         `json:"new_content"`
	OldAttachments []*discordgo.MessageAttachment `json:"old_attachments"`
	NewAttachments []*discordgo.MessageAttachment `json:"new_attachments"`
}

func onMessageUpdate(s *discordgo.Session, m *discordgo.MessageUpdate) {
	if m.ID == "" || m.BeforeUpdate == nil || m.Author == nil {
		return
	}

	// access the message cache
	services.MsgCache.AddMessage(m.Message)

	data := MsgUpdate{
		Type:           "message_update",
		ID:             m.ID,
		Author:         structs.LogUser{ID: m.Author.ID, Username: m.Author.Username},
		GuildID:        m.GuildID,
		ChannelID:      m.ChannelID,
		OldContent:     m.BeforeUpdate.Content,
		NewContent:     m.Content,
		OldAttachments: m.BeforeUpdate.Attachments,
		NewAttachments: m.Attachments,
	}

	// send the kafka message
	json, err := lib.ToJSONByteArr(data)
	if err != nil {
		return
	}

	services.Kafka.Produce(context.Background(), []byte(data.Type), json)
}
