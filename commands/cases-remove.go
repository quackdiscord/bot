package commands

import (
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/storage"
	log "github.com/sirupsen/logrus"
)

var casesRemoveCmd = &discordgo.ApplicationCommandOption{
	Type:        discordgo.ApplicationCommandOptionSubCommandGroup,
	Name:        "remove",
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
	guild, _ := s.Guild(i.GuildID)

	go func() {
		// find the case first
		c, err := storage.FindLatestCase(guild.ID)
		if err != nil {
			log.WithError(err).Error("Failed to find latest case")
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("Failed to find latest case.")},
			})
			return
		}

		if c == nil {
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("Latest case not found.")},
			})
			return
		}

		_, err2 := storage.DeleteLatestCase(guild.ID)
		if err2 != nil {
			log.WithError(err2).Error("Failed to delete latest case")
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("Failed to delete latest case.")},
			})
			return
		}

		embed := components.NewEmbed().SetDescription("Deleted latest case.").SetColor("Main").MessageEmbed
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{embed},
		})
	}()

	return LoadingResponse()
}

func handleCasesRemoveID(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	guild, _ := s.Guild(i.GuildID)
	caseID := i.ApplicationCommandData().Options[0].Options[0].Options[0].StringValue()

	go func() {
		// find the case first
		c, err := storage.FindCaseByID(caseID, guild.ID)
		if err != nil {
			log.WithError(err).Error("Failed to find case")
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("Failed to find case.")},
			})
			return
		}

		if c == nil {
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("Case not found.")},
			})
			return
		}

		_, err2 := storage.DeleteCaseByID(caseID, guild.ID)
		if err2 != nil {
			log.WithError(err2).Error("Failed to delete case")
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("Failed to delete case.")},
			})
			return
		}

		embed := components.NewEmbed().SetDescription("Deleted case `" + caseID + "`.").SetColor("Main").MessageEmbed
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{embed},
		})
	}()

	return LoadingResponse()
}

func handleCasesRemoveUser(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	guild, _ := s.Guild(i.GuildID)
	user := i.ApplicationCommandData().Options[0].Options[0].Options[0].UserValue(s)

	go func() {
		_, err := storage.DeleteCasesByUserID(user.ID, guild.ID)
		if err != nil {
			log.WithError(err).Error("Failed to delete users cases")
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("Failed to delete user's cases.")},
			})
			return
		}

		embed := components.NewEmbed().SetDescription(" Deleted <@" + user.ID + ">'s cases.").SetColor("Main").MessageEmbed
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{embed},
		})
	}()

	return LoadingResponse()
}
