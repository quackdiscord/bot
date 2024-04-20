package commands

import (
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/lib"
	"github.com/quackdiscord/bot/structs"
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
	}()

	return LoadingResponse()
}
