package commands

import (
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/storage"
	"github.com/rs/zerolog/log"
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

	// find the case first
	c, err := storage.FindLatestCase(guild.ID)
	if err != nil {
		log.Error().AnErr("Failed to find latest case", err)
		services.CaptureError(err)
		return components.EmbedResponse(components.ErrorEmbed("Failed to find latest case."), true)
	}

	if c == nil {
		return components.EmbedResponse(components.ErrorEmbed("Latest case not found."), true)
	}

	_, err = storage.DeleteLatestCase(guild.ID)
	if err != nil {
		log.Error().AnErr("Failed to delete latest case", err)
		services.CaptureError(err)
		return components.EmbedResponse(components.ErrorEmbed("Failed to delete latest case."), true)
	}

	embed := components.NewEmbed().SetDescription("Deleted latest case.").SetColor("Main").MessageEmbed

	return components.EmbedResponse(embed, false)
}

func handleCasesRemoveID(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	guild, _ := s.Guild(i.GuildID)
	caseID := i.ApplicationCommandData().Options[0].Options[0].Options[0].StringValue()

	// find the case first
	c, err := storage.FindCaseByID(caseID, guild.ID)
	if err != nil {
		log.Error().AnErr("Failed to find case", err)
		services.CaptureError(err)
		return components.EmbedResponse(components.ErrorEmbed("Failed to find case."), true)
	}

	if c == nil {
		return components.EmbedResponse(components.ErrorEmbed("Case not found."), true)
	}

	_, err = storage.DeleteCaseByID(caseID, guild.ID)
	if err != nil {
		log.Error().AnErr("Failed to delete case", err)
		services.CaptureError(err)
		return components.EmbedResponse(components.ErrorEmbed("Failed to delete case."), true)
	}

	embed := components.NewEmbed().SetDescription("Deleted case `" + caseID + "`.").SetColor("Main").MessageEmbed

	return components.EmbedResponse(embed, false)
}

func handleCasesRemoveUser(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	guild, _ := s.Guild(i.GuildID)
	user := i.ApplicationCommandData().Options[0].Options[0].Options[0].UserValue(s)

	_, err := storage.DeleteCasesByUserID(user.ID, guild.ID)
	if err != nil {
		log.Error().AnErr("Failed to delete users cases", err)
		services.CaptureError(err)
		return components.EmbedResponse(components.ErrorEmbed("Failed to delete user's cases."), true)
	}

	embed := components.NewEmbed().SetDescription(" Deleted <@" + user.ID + ">'s cases.").SetColor("Main").MessageEmbed

	return components.EmbedResponse(embed, false)
}
