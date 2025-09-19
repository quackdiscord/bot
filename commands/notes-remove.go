package commands

import (
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/storage"
	"github.com/rs/zerolog/log"
)

var notesRemoveCmd = &discordgo.ApplicationCommandOption{
	Type:        discordgo.ApplicationCommandOptionSubCommandGroup,
	Name:        "remove",
	Description: "Remove a note from a user",
	Options: []*discordgo.ApplicationCommandOption{
		{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "latest",
			Description: "Remove the latest note from a user",
		},
		{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "id",
			Description: "Remove a note by ID",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "id",
					Description: "The ID of the note to remove",
					Required:    true,
				},
			},
		},
		{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "user",
			Description: "Remove all notes from a user",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionUser,
					Name:        "user",
					Description: "The user to remove notes from",
					Required:    true,
				},
			},
		},
	},
}

func handleNotesRemoveLatest(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	guild, _ := s.Guild(i.GuildID)

	// find the note first
	n, err := storage.FindLatestNote(guild.ID)
	if err != nil {
		log.Error().AnErr("Failed to find latest note", err)
		return EmbedResponse(components.ErrorEmbed("Failed to find latest note."), true)
	}

	if n == nil {
		return EmbedResponse(components.ErrorEmbed("Latest note not found."), true)
	}

	_, err = storage.DeleteLatestNote(guild.ID)
	if err != nil {
		log.Error().AnErr("Failed to delete latest note", err)
		return EmbedResponse(components.ErrorEmbed("Failed to delete latest note."), true)
	}

	embed := components.NewEmbed().
		SetDescription("<:PepoG:1172051306026905620> Deleted latest note").
		SetColor("DarkButNotBlack").
		MessageEmbed

	return EmbedResponse(embed, false)
}

func handleNotesRemoveUser(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	guild, _ := s.Guild(i.GuildID)
	user := i.ApplicationCommandData().Options[0].Options[0].Options[0].UserValue(s)

	_, err := storage.DeleteNoteByUserID(user.ID, guild.ID)
	if err != nil {
		log.Error().AnErr("Failed to delete users notes", err)
		return EmbedResponse(components.ErrorEmbed("Failed to delete user's notes."), true)
	}

	embed := components.NewEmbed().SetDescription("<:PepoG:1172051306026905620> Deleted <@" + user.ID + ">'s notes.").SetColor("DarkButNotBlack").MessageEmbed

	return EmbedResponse(embed, false)
}

func handleNotesRemoveID(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	guild, _ := s.Guild(i.GuildID)
	noteID := i.ApplicationCommandData().Options[0].Options[0].Options[0].StringValue()

	// find the case first
	n, err := storage.FindNoteByID(noteID, guild.ID)
	if err != nil {
		log.Error().AnErr("Failed to find note", err)
		return EmbedResponse(components.ErrorEmbed("Failed to find note."), true)
	}

	if n == nil {
		return EmbedResponse(components.ErrorEmbed("Note not found."), true)
	}

	_, err = storage.DeleteNoteByID(noteID, guild.ID)
	if err != nil {
		log.Error().AnErr("Failed to delete note", err)
		return EmbedResponse(components.ErrorEmbed("Failed to delete note."), true)
	}

	embed := components.NewEmbed().SetDescription("<:PepoG:1172051306026905620> Deleted note `" + noteID + "`.").SetColor("DarkButNotBlack").MessageEmbed

	return EmbedResponse(embed, false)
}
