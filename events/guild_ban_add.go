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
	Events = append(Events, onGuildBanAdd)
}

type MemberBanned struct {
	Type    string          `json:"type"`
	GuildID string          `json:"guild_id"`
	User    *discordgo.User `json:"user"`
	Case    *structs.Case   `json:"case"`
}

func onGuildBanAdd(s *discordgo.Session, m *discordgo.GuildBanAdd) {
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
			Type:        1,
			Reason:      "",
			CreatedAt:   "",
		}
	} else {
		// if the user is banned by something other than the bot, the latest case won't be the one for this event
		// or if for some reason, the event is dispatched before the case is created
		// or if the latest casae is related to this user, but its not a ban
		if c.UserID != m.User.ID || c.Type != 1 {
			c = &structs.Case{
				ID:          "",
				GuildID:     m.GuildID,
				UserID:      m.User.ID,
				ModeratorID: "",
				Type:        1,
				Reason:      "",
				CreatedAt:   "",
			}
		}
	}

	data := MemberBanned{
		Type:    "guild_ban_add",
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
