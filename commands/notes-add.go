package commands

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/lib"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
	log "github.com/sirupsen/logrus"
)

var notesAddCmd = &discordgo.ApplicationCommandOption{
	Type:        discordgo.ApplicationCommandOptionSubCommand,
	Name:        "add",
	Description: "Add a note to a user",
	Options: []*discordgo.ApplicationCommandOption{
		{
			Type:        discordgo.ApplicationCommandOptionUser,
			Name:        "user",
			Description: "The user to add the note to",
			Required:    true,
		},
		{
			Type:        discordgo.ApplicationCommandOptionString,
			Name:        "content",
			Description: "The content of the note",
			Required:    true,
		},
	},
}

func handleNotesAdd(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	userToNote := i.ApplicationCommandData().Options[0].Options[0].UserValue(s)
	content := i.ApplicationCommandData().Options[0].Options[1].StringValue()
	moderator := i.Member.User
	guild, _ := s.Guild(i.GuildID)

	go func() {
		// create the note
		id, _ := lib.GenID()
		noteData := &structs.Note{
			ID:          id,
			Content:     content,
			UserID:      userToNote.ID,
			ModeratorID: moderator.ID,
			GuildID:     guild.ID,
		}

		err := storage.CreateNote(noteData)
		if err != nil {
			log.WithError(err).Error("Failed to create note")
			embed := components.NewEmbed().SetDescription("<:error:1228053905590718596> **Error:** Failed to save note.\n```" + err.Error() + "```").SetColor("Error").MessageEmbed
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{embed},
			})
			return
		}

		// form the embed
		embed := components.NewEmbed().
			SetDescription(fmt.Sprintf("<:PepoG:1172051306026905620> Note taken for <@%s>.\n<:text:1229343822337802271>`%s`", userToNote.ID, content)).
			SetColor("DarkButNotBlack").
			SetAuthor(fmt.Sprintf("%s noted %s", moderator.Username, userToNote.Username), userToNote.AvatarURL((""))).
			SetFooter("Note ID: " + id).
			SetTimestamp().
			MessageEmbed

		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{embed},
		})
	}()

	return LoadingResponse()
}
