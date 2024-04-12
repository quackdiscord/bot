package cmds

import (
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/services"
)

func init() {
	services.Commands[casesCmd.Name] = &services.Command{
		ApplicationCommand: casesCmd,
		Handler:            handleCases,
	}
}

// cases add @user reason
// cases remove id :id
// cases remove user @user
// cases remove latest
// cases view user @user
// cases view latest
// cases view id :id

var casesCmd = &discordgo.ApplicationCommand{
	Type:                     discordgo.ChatApplicationCommand,
	Name:                     "cases",
	Description:              "Manage cases",
	DefaultMemberPermissions: &moderateMembers,
	Options: []*discordgo.ApplicationCommandOption{
		casesAddCmd,
		casesViewCmd,
		casesRemoveCmd,
	},
}

func handleCases(s *discordgo.Session, i *discordgo.InteractionCreate) (resp *discordgo.InteractionResponse) {
	switch c := i.ApplicationCommandData().Options[0]; c.Name {
		case "add":
			return handleCasesAdd(s, i)
		case "remove":
			switch sc := c.Options[0]; sc.Name {
				case "latest":
					return handleCasesRemoveLatest(s, i)
				case "id":
					return handleCasesRemoveID(s, i)
				case "user":
					return handleCasesRemoveUser(s, i)
			}
		case "view":
			switch sc := c.Options[0]; sc.Name {
				case "latest":
					return handleCasesViewLatest(s, i)
				case "id":
					return handleCasesViewID(s, i)
				case "user":
					return handleCasesViewUser(s, i)
			}
	}

	return ContentResponse("Command does not exits", true)
}
