package cmds

import (
	"github.com/bwmarrin/discordgo"
)

var casesViewCmd = &discordgo.ApplicationCommandOption{
	Type:        discordgo.ApplicationCommandOptionSubCommandGroup,
	Name:        "view",
	Description: "Ways to view various cases",
	Options: []*discordgo.ApplicationCommandOption{
		{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:		"latest",
			Description: "View the latest case in the server",
		},
		{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:		"user",
			Description: "View all cases for a user",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionUser,
					Name:        "user",
					Description: "The user to view cases for",
					Required:    true,
				},
			},
		},
		{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:		"id",
			Description: "View a specific case by ID",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "id",
					Description: "The ID of the case to view",
					Required:    true,
				},
			},
		},
	},
}

func handleCasesViewLatest(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	// defer the response
	LoadingResponse()

	// TODO: this lol

	return ContentResponse("/cases view latest", false)
}

func handleCasesViewID(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	// defer the response
	LoadingResponse()

	// TODO: this lol

	return ContentResponse("/cases view id", false)
}

func handleCasesViewUser(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	// defer the response
	LoadingResponse()

	// TODO: this lol

	return ContentResponse("/cases view user", false)
}
