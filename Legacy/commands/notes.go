package commands

import (
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/lib"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/utils"
)

func init() {
	services.Commands[notesCmd.Name] = &services.Command{
		ApplicationCommand: notesCmd,
		Handler:            handleNotes,
	}
}

// notes add @user reason
// notes remove id :id
// notes remove user @user
// notes remove latest
// notes view user @user
// notes view latest
// notes view id :id

var notesCmd = &discordgo.ApplicationCommand{
	Type:                     discordgo.ChatApplicationCommand,
	Name:                     "notes",
	Description:              "Manage Notes",
	DefaultMemberPermissions: &lib.Permissions.ModerateMembers,
	Options: []*discordgo.ApplicationCommandOption{
		notesAddCmd,
		notesRemoveCmd,
		notesViewCmd,
	},
}

func handleNotes(s *discordgo.Session, i *discordgo.InteractionCreate) (resp *discordgo.InteractionResponse) {
	if !utils.CheckPerms(i.Member, lib.Permissions.ModerateMembers) {
		return components.EmbedResponse(components.ErrorEmbed("You do not have the permissions required to use this command."), true)
	}

	switch c := i.ApplicationCommandData().Options[0]; c.Name {
	case "add":
		return handleNotesAdd(s, i)
	case "remove":
		switch sc := c.Options[0]; sc.Name {
		case "latest":
			return handleNotesRemoveLatest(s, i)
		case "id":
			return handleNotesRemoveID(s, i)
		case "user":
			return handleNotesRemoveUser(s, i)
		}
	case "view":
		switch sc := c.Options[0]; sc.Name {
		case "latest":
			return handleNotesViewLatest(s, i)
		case "id":
			return handleNotesViewID(s, i)
		case "user":
			return handleNotesViewUser(s, i)
		}
	}

	return components.EmbedResponse(components.ErrorEmbed("Command does not exits"), true)
}
