package cmds

import (
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/storage"
	log "github.com/sirupsen/logrus"
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
	user := i.ApplicationCommandData().Options[0].Options[0].Options[0].UserValue(s)
	guild, _ := s.Guild(i.GuildID)

	go func() {
		cases, err := storage.FindCasesByUserID(user.ID, guild.ID)
		if err != nil {
			log.WithError(err).Error("Failed to fetch user cases")
			embed := components.NewEmbed().SetDescription("<:error:1228053905590718596> **Error:** Failed to fetch user's cases.").SetColor("Error").MessageEmbed
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{embed},
			})
			return
		}

		if len(cases) == 0 {
			embed := components.NewEmbed().SetDescription("<@" + user.ID + "> has no cases.").SetColor("Main").MessageEmbed
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{embed},
			})
			return
		}

		// if the lengh of the cases is greater than 10, we need to paginate
		if len(cases) > 10 {
			// TODO: paginate
			// for now just remove anything after the first 10
			cases = cases[:10]
		}

		embed := components.NewEmbed().
			SetDescription("User cases").SetColor("Main").MessageEmbed

		for _, c := range cases {
			embed.Description += c.Reason + "\n"
		}

		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{embed},
		})
	}()

	return LoadingResponse()
}
