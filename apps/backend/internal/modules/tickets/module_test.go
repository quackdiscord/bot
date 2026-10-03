package tickets_test

import (
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discord"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/tickets"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/testutil"
)

func TestComponentsInstallBesideCoreRoutes(t *testing.T) {
	db := testutil.NewSQLiteDB(t)
	bot, err := discord.New("Bot test")
	if err != nil {
		t.Fatal(err)
	}
	services := quack.New(quack.Deps{})
	router := discord.NewRouter(bot, services, nil)
	// The router panics on a duplicate or empty route.
	tickets.New(db, modules.NewRegistry(db), nil, nil, bot.Session, services.Guilds).RegisterComponents(router)

	for _, components := range [][]discordgo.MessageComponent{
		tickets.EntryComponents(),
		tickets.TicketComponents("ticket-id"),
	} {
		for _, component := range components[0].(discordgo.ActionsRow).Components {
			id, err := discord.DecodeCustomID(component.(discordgo.Button).CustomID)
			if err != nil || id.Namespace != "ticket" {
				t.Errorf("button custom ID = %+v, %v; want the ticket namespace", id, err)
			}
		}
	}
}
