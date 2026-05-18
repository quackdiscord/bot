package events

import (
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/discord"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/structs"
)

func init() {
	discord.Events["message_delete"] = &structs.DiscordEvent{
		Handler: msgDelete,
	}
}

func msgDelete(_ *discordgo.Session, data *discordgo.MessageDelete) {
	services.EQ.Enqueue(structs.QueueEvent{
		Type:    "message_delete",
		Data:    data,
		Handler: msgDeleteHandler,
	})
}

func msgDeleteHandler(s structs.DataStore, data any) {
	// msg := data.(*discordgo.MessageDelete)
	// do something here
	_ = s
}
