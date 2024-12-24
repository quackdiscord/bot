package events

import (
	"github.com/quackdiscord/bot/log"
	"github.com/quackdiscord/bot/services"
)

var Events = []interface{}{}

// this is specifically for the events queue
func RegisterEvents() {
	services.EQ.RegisterHandler("message_delete", msgDeleteHandler)
	services.EQ.RegisterHandler("message_update", msgUpdateHandler)
	services.EQ.RegisterHandler("message_bulk_delete", msgBulkDeleteHandler)

	services.EQ.RegisterHandler("member_join", memberJoinHandler)
	services.EQ.RegisterHandler("member_leave", memberLeaveHandler)

	services.EQ.RegisterHandler("guild_ban_add", guildBanAddHandler)
	services.EQ.RegisterHandler("guild_ban_remove", guildBanRemoveHandler)

	log.Info().Msg("Events registered in events queue")
}
