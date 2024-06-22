package events

import (
	"context"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/lib"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
	log "github.com/sirupsen/logrus"
)

func init() {
	Events = append(Events, onGuildBanRemove)
}

type MemberUnbanned struct {
	Type    string          `json:"type"`
	GuildID string          `json:"guild_id"`
	User    *discordgo.User `json:"user"`
	Case    *structs.Case   `json:"case"`
}

func onGuildBanRemove(s *discordgo.Session, m *discordgo.GuildBanRemove) {
	c, err := storage.FindLatestCase(m.GuildID)
	if err != nil {
		log.WithError(err).Error("Failed to fetch latest case")
		return
	}

	// if case is empty, set it to a default case
	if c == nil {
		c = &structs.Case{
			ID:          "",
			GuildID:     m.GuildID,
			UserID:      m.User.ID,
			ModeratorID: "",
			Type:        3,
			Reason:      "",
			CreatedAt:   "",
		}
	} else {
		// if the user is unbanned by something other than the bot, the latest case won't be the one for this event
		// or if for some reason, the event is dispatched before the case is created
		// or if the latest casae is related to this user, but its not an unban
		if c.UserID != m.User.ID || c.Type != 3 {
			c = &structs.Case{
				ID:          "",
				GuildID:     m.GuildID,
				UserID:      m.User.ID,
				ModeratorID: "",
				Type:        3,
				Reason:      "",
				CreatedAt:   "",
			}
		}
	}

	data := MemberUnbanned{
		Type:    "guild_ban_remove",
		GuildID: m.GuildID,
		User:    m.User,
		Case:    c,
	}

	// send kafka message
	json, err := lib.ToJSONByteArr(data)
	if err != nil {
		return
	}

	services.Kafka.Produce(context.Background(), []byte(data.Type), json)
}
