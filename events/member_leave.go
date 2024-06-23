package events

import (
	"context"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/lib"
	"github.com/quackdiscord/bot/services"
)

func init() {
	Events = append(Events, onMemberLeave)
}

type MemberLeave struct {
	Type    string            `json:"type"`
	GuildID string            `json:"guild_id"`
	Member  *discordgo.Member `json:"member"`
}

func onMemberLeave(s *discordgo.Session, m *discordgo.GuildMemberRemove) {
	data := MemberLeave{
		Type:    "member_leave",
		GuildID: m.GuildID,
		Member:  m.Member,
	}

	// send kafka message
	json, err := lib.ToJSONByteArr(data)
	if err != nil {
		return
	}

	services.Kafka.Produce(context.Background(), []byte(data.Type), json)
}
