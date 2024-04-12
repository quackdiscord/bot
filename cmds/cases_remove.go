package cmds

import (
	"github.com/bwmarrin/discordgo"
)

var casesRemoveCmd = &discordgo.ApplicationCommandOption{
	Type: 	  	discordgo.ApplicationCommandOptionSubCommandGroup,
	Name: 	  	"remove",
	Description: "Remove a case from a user",
	Options: []*discordgo.ApplicationCommandOption{
		{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "latest",
			Description: "Remove the latest case from a user",
		},
		{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "id",
			Description: "Remove a case by ID",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "id",
					Description: "The ID of the case to remove",
					Required:    true,
				},
			},
		},
		{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "user",
			Description: "Remove all cases from a user",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionUser,
					Name:        "user",
					Description: "The user to remove cases from",
					Required:    true,
				},
			},
		},
	},
}

func handleCasesRemoveLatest(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	// defer the response
	LoadingResponse()

	// TODO: this lol

	return ContentResponse("/cases remove latest", false)
}

func handleCasesRemoveID(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	// defer the response
	LoadingResponse()

	// TODO: this lol

	return ContentResponse("/cases remove id", false)
}

func handleCasesRemoveUser(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	// defer the response
	LoadingResponse()

	// TODO: this lol

	return ContentResponse("/cases remove user", false)
}
