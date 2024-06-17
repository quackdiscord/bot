package commands

import (
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/storage"
	log "github.com/sirupsen/logrus"
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

	go func() {
		// find the note first
		n, err := storage.FindLatestNote(guild.ID)
		if err != nil {
			log.WithError(err).Error("Failed to find latest note")
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("Failed to find latest note.")},
			})
			return
		}

		if n == nil {
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("Latest note not found.")},
			})
			return
		}

		_, err2 := storage.DeleteLatestNote(guild.ID)
		if err2 != nil {
			log.WithError(err2).Error("Failed to delete latest note")
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("Failed to delete latest note.")},
			})
			return
		}

		embed := components.NewEmbed().
			SetDescription("<:PepoG:1172051306026905620> Deleted latest note").
			SetColor("DarkButNotBlack").
			MessageEmbed

		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{embed},
		})
	}()

	return LoadingResponse()
}

func handleNotesRemoveUser(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	guild, _ := s.Guild(i.GuildID)
	user := i.ApplicationCommandData().Options[0].Options[0].Options[0].UserValue(s)

	go func() {
		_, err := storage.DeleteNoteByUserID(user.ID, guild.ID)
		if err != nil {
			log.WithError(err).Error("Failed to delete users notes")
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("Failed to delete user's notes.")},
			})
			return
		}

		embed := components.NewEmbed().SetDescription("<:PepoG:1172051306026905620> Deleted <@" + user.ID + ">'s notes.").SetColor("DarkButNotBlack").MessageEmbed
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{embed},
		})
	}()

	return LoadingResponse()
}

func handleNotesRemoveID(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	guild, _ := s.Guild(i.GuildID)
	noteID := i.ApplicationCommandData().Options[0].Options[0].Options[0].StringValue()

	go func() {
		// find the case first
		n, err := storage.FindNoteByID(noteID, guild.ID)
		if err != nil {
			log.WithError(err).Error("Failed to find note")
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("Failed to find note.")},
			})
			return
		}

		if n == nil {
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("Note not found.")},
			})
			return
		}

		_, err2 := storage.DeleteNoteByID(noteID, guild.ID)
		if err2 != nil {
			log.WithError(err2).Error("Failed to delete note")
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("Failed to delete note.")},
			})
			return
		}

		embed := components.NewEmbed().SetDescription("<:PepoG:1172051306026905620> Deleted note `" + noteID + "`.").SetColor("DarkButNotBlack").MessageEmbed
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{embed},
		})
	}()

	return LoadingResponse()
}
