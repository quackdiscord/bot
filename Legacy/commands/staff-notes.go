package commands

import (
	"fmt"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/lib"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
	"github.com/rs/zerolog/log"
)

var staffNotesCmd = &discordgo.ApplicationCommandOption{
	Type:        discordgo.ApplicationCommandOptionSubCommandGroup,
	Name:        "notes",
	Description: "Manage admin notes about staff members",
	Options: []*discordgo.ApplicationCommandOption{
		{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "view",
			Description: "View admin notes about a staff member",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionUser,
					Name:        "mod",
					Description: "The staff member to view notes for",
					Required:    true,
				},
			},
		},
		{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "add",
			Description: "Add an admin note about a staff member",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionUser,
					Name:        "mod",
					Description: "The staff member to add a note for",
					Required:    true,
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "content",
					Description: "The content of the note",
					Required:    true,
				},
			},
		},
		{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "remove",
			Description: "Remove an admin note by ID",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "id",
					Description: "The ID of the note to remove",
					Required:    true,
				},
			},
		},
	},
}

func handleStaffNotes(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	subCmd := i.ApplicationCommandData().Options[0].Options[0]

	switch subCmd.Name {
	case "view":
		return handleStaffNotesView(s, i)
	case "add":
		return handleStaffNotesAdd(s, i)
	case "remove":
		return handleStaffNotesRemove(s, i)
	}

	return components.EmbedResponse(components.ErrorEmbed("Command does not exist"), true)
}

func handleStaffNotesView(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	mod := i.ApplicationCommandData().Options[0].Options[0].Options[0].UserValue(s)

	go func() {
		notes, err := storage.FindModNotesByModID(mod.ID, i.GuildID)
		if err != nil {
			log.Error().Err(err).Msg("Failed to fetch mod notes")
			services.CaptureError(err)
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{components.ErrorEmbed("Failed to fetch notes.")},
			})
			return
		}

		if len(notes) == 0 {
			embed := components.NewEmbed().
				SetDescription(fmt.Sprintf("<@%s> has no admin notes.", mod.ID)).
				SetColor("Main").
				MessageEmbed
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{embed},
			})
			return
		}

		content := fmt.Sprintf("**%d** admin notes about <@%s>\n\n", len(notes), mod.ID)

		for _, n := range notes {
			author, _ := s.User(n.ModeratorID)
			authorName := "Unknown"
			if author != nil {
				authorName = author.Username
			}

			// Parse timestamp
			parsedTime, _ := time.Parse("2006-01-02 15:04:05", n.CreatedAt)
			unixTime := parsedTime.Unix()

			// Truncate content if too long
			noteContent := n.Content
			if len(noteContent) > 100 {
				noteContent = noteContent[:97] + "..."
			}

			content += fmt.Sprintf(
				"<:text3:1229350410293350471> `%s`\n<:text2:1229344477131309136> <t:%d:R> by **%s**\n<:text:1229343822337802271> *\"%s\"*\n\n",
				n.ID, unixTime, authorName, noteContent,
			)
		}

		// Truncate if too long
		if len(content) > 2048 {
			content = content[:2000] + "\n\n*Too many notes to show them all.*"
		}

		embed := components.NewEmbed().
			SetAuthor(fmt.Sprintf("Admin Notes for %s", mod.Username), mod.AvatarURL("")).
			SetDescription(content).
			SetColor("Main").
			SetTimestamp().
			MessageEmbed

		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{embed},
		})
	}()

	return components.LoadingResponse()
}

func handleStaffNotesAdd(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	mod := i.ApplicationCommandData().Options[0].Options[0].Options[0].UserValue(s)
	content := i.ApplicationCommandData().Options[0].Options[0].Options[1].StringValue()

	if i.Member == nil {
		return components.EmbedResponse(components.ErrorEmbed("You must be in a server to use this command."), true)
	}

	author := i.Member.User

	// Generate note ID
	id, _ := lib.GenID()
	noteData := &structs.Note{
		ID:          id,
		UserID:      mod.ID,    // The mod being noted about
		ModeratorID: author.ID, // The admin creating the note
		GuildID:     i.GuildID,
		Content:     content,
	}

	err := storage.CreateModNote(noteData)
	if err != nil {
		log.Error().Err(err).Msg("Failed to create mod note")
		services.CaptureError(err)
		return components.EmbedResponse(components.ErrorEmbed("Failed to save note.\n```"+err.Error()+"```"), true)
	}

	embed := components.NewEmbed().
		SetDescription(fmt.Sprintf("Note added for <@%s>\n\n> *\"%s\"*", mod.ID, content)).
		SetColor("Main").
		SetAuthor(fmt.Sprintf("%s noted %s", author.Username, mod.Username), mod.AvatarURL("")).
		SetFooter("Note ID: " + id).
		SetTimestamp().
		MessageEmbed

	return components.EmbedResponse(embed, false)
}

func handleStaffNotesRemove(_ *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	noteID := i.ApplicationCommandData().Options[0].Options[0].Options[0].StringValue()

	// First check if note exists
	note, err := storage.FindModNoteByID(noteID, i.GuildID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to find mod note")
		services.CaptureError(err)
		return components.EmbedResponse(components.ErrorEmbed("Failed to find note."), true)
	}

	if note == nil {
		return components.EmbedResponse(components.ErrorEmbed("Note not found."), true)
	}

	// Delete the note
	deleted, err := storage.DeleteModNoteByID(noteID, i.GuildID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to delete mod note")
		services.CaptureError(err)
		return components.EmbedResponse(components.ErrorEmbed("Failed to delete note."), true)
	}

	if !deleted {
		return components.EmbedResponse(components.ErrorEmbed("Note could not be deleted."), true)
	}

	embed := components.NewEmbed().
		SetDescription(fmt.Sprintf("Deleted note `%s` about <@%s>.", noteID, note.UserID)).
		SetColor("Main").
		SetTimestamp().
		MessageEmbed

	return components.EmbedResponse(embed, false)
}
