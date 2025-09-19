package commands

import (
	"fmt"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
	"github.com/rs/zerolog/log"
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
	n, err := storage.FindLatestNote(i.GuildID)
	if err != nil {
		log.Error().AnErr("Failed to fetch latest note", err)
		return EmbedResponse(components.ErrorEmbed("Failed to fetch latest note."), true)
	}

	embed := generateNotesEmbed(s, n)

	return EmbedResponse(embed, false)
}

func handleNotesViewUser(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	user := i.ApplicationCommandData().Options[0].Options[0].Options[0].UserValue(s)

	notes, err := storage.FindNoteByUserID(user.ID, i.GuildID)
	if err != nil {
		log.Error().AnErr("Failed to fetch note by user id", err)
		return EmbedResponse(components.ErrorEmbed("Failed to fetch notes."), true)
	}

	if len(notes) == 0 {
		embed := components.NewEmbed().SetDescription("<:PepoG:1172051306026905620> <@" + user.ID + "> has no notes.").SetColor("DarkButNotBlack").MessageEmbed
		return EmbedResponse(embed, false)
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

	return EmbedResponse(embed, false)
}

func handleNotesViewID(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	caseID := i.ApplicationCommandData().Options[0].Options[0].Options[0].StringValue()

	n, err := storage.FindNoteByID(caseID, i.GuildID)
	if err != nil {
		log.Error().AnErr("Failed to fetch note by id", err)
		return EmbedResponse(components.ErrorEmbed("Failed to fetch note."), true)
	}

	embed := generateNotesEmbed(s, n)

	return EmbedResponse(embed, false)
}

// generate a case embed from a case
func generateNotesEmbed(s *discordgo.Session, n *structs.Note) *discordgo.MessageEmbed {
	if n == nil {
		return components.ErrorEmbed("Note not found.")
	}

	user, _ := s.User(n.UserID)
	moderator, _ := s.User(n.ModeratorID)

	if user == nil || moderator == nil {
		return components.ErrorEmbed("Failed to fetch user or moderator.")
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
