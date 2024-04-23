package commands

import (
	"fmt"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
	log "github.com/sirupsen/logrus"
)

var notesViewCmd = &discordgo.ApplicationCommandOption{
	Type:        discordgo.ApplicationCommandOptionSubCommandGroup,
	Name:        "view",
	Description: "Ways to view various notes",
	Options: []*discordgo.ApplicationCommandOption{
		{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "latest",
			Description: "View the latest note in the server",
		},
		{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "user",
			Description: "View all notes for a user",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionUser,
					Name:        "user",
					Description: "The user to view notes for",
					Required:    true,
				},
			},
		},
		{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "id",
			Description: "View a specific note by ID",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "id",
					Description: "The ID of the note to view",
					Required:    true,
				},
			},
		},
	},
}

func handleNotesViewLatest(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	go func() {
		n, err := storage.FindLatestNote(i.GuildID)
		if err != nil {
			log.WithError(err).Error("Failed to fetch latest note")
			embed := components.NewEmbed().SetDescription("<:error:1228053905590718596> **Error:** Failed to fetch latest note.").SetColor("Error").MessageEmbed
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{embed},
			})
			return
		}

		embed := generateNotesEmbed(s, n)

		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{embed},
		})

	}()

	return LoadingResponse()
}

func handleNotesViewUser(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	user := i.ApplicationCommandData().Options[0].Options[0].Options[0].UserValue(s)

	go func(){
		notes, err := storage.FindNoteByUserID(user.ID, i.GuildID)
		if err != nil {
			log.WithError(err).Error("Failed to fetch note by user id")
			embed := components.NewEmbed().SetDescription("<:error:1228053905590718596> **Error:** Failed to fetch notes.").SetColor("Error").MessageEmbed
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{embed},
			})
			return
		}

		if len(notes) == 0 {
			embed := components.NewEmbed().SetDescription("<:PepoG:1172051306026905620> <@" + user.ID + "> has no notes.").SetColor("DarkButNotBlack").MessageEmbed
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{embed},
			})
			return
		}

		content := fmt.Sprintf("<@%s> has **%d** notes\n\n", user.ID, len(notes))

		for _, n := range notes {
			moderator, _ := s.User(n.ModeratorID)
			if moderator == nil {
				moderator = &discordgo.User{Username: "Unknown"}
			}

			content += *generateNoteDetails(n, moderator)
		}

		// if the content is > 2048 characters, cut it off and add "too many to show..."
		if len(content) > 2048 {
			content = content[:2000] + "\n\n*Too many notes to show them all.*"
		}

		embed := components.NewEmbed().
			SetDescription(content).
			SetTimestamp().
			SetAuthor("Notes for "+user.Username, user.AvatarURL("")).
			SetColor("DarkButNotBlack").MessageEmbed

		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{embed},
		})
	}()

	return LoadingResponse()
}

func handleNotesViewID(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	caseID := i.ApplicationCommandData().Options[0].Options[0].Options[0].StringValue()

	go func(){
		n, err := storage.FindNoteByID(caseID, i.GuildID)
		if err != nil {
			log.WithError(err).Error("Failed to fetch note by id")
			embed := components.NewEmbed().SetDescription("<:error:1228053905590718596> **Error:** Failed to fetch note.").SetColor("Error").MessageEmbed
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{embed},
			})
			return
		}

		embed := generateNotesEmbed(s, n)

		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{embed},
		})

	}()

	return LoadingResponse()
}


// generate a case embed from a case
func generateNotesEmbed(s *discordgo.Session, n *structs.Note) *discordgo.MessageEmbed {
	if n == nil {
		embed := components.NewEmbed().SetDescription("<:error:1228053905590718596> **Error:** Note not found.").SetColor("Error").MessageEmbed
		return embed
	}

	user, _ := s.User(n.UserID)
	moderator, _ := s.User(n.ModeratorID)

	if user == nil || moderator == nil {
		embed := components.NewEmbed().SetDescription("<:error:1228053905590718596> **Error:** Failed to fetch user or moderator.").SetColor("Error").MessageEmbed
		return embed
	}

	embed := components.NewEmbed().
		SetDescription(fmt.Sprintf("<@%s> (%s)'s Note \n\n", user.ID, user.Username)+*generateNoteDetails(n, moderator)).
		SetAuthor(fmt.Sprintf("Note %s", n.ID), user.AvatarURL("")).
		SetTimestamp().
		SetColor("DarkButNotBlack").MessageEmbed

	return embed
}

func generateNoteDetails(n *structs.Note, moderator *discordgo.User) *string {
	parsedTime, _ := time.Parse("2006-01-02 15:04:05", n.CreatedAt)
	unixTime := parsedTime.Unix()

	details := fmt.Sprintf(
		"<t:%d:R> by %s\n<:text2:1229344477131309136> *\"%s\"*\n<:text:1229343822337802271> `ID: %s`\n\n",
		unixTime, moderator.Username, n.Content, n.ID,
	)
	return &details
}
