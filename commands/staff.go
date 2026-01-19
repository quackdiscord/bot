package commands

import (
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/lib"
	"github.com/quackdiscord/bot/services"
)

func init() {
	services.Commands[staffCmd.Name] = &services.Command{
		ApplicationCommand: staffCmd,
		Handler:            handleStaff,
	}
}

// /staff view @moderator 		- view a mods profile/stats
// /staff list 					- list all mods with summary stats
// /staff notes view @mod 		- view admin notes about a mod
// /staff notes add @mod 		- add an admin note about a mod
// /staff notes remove <id>		- remove an admin note about a mod
// /staff activity @mod			- activity heatmap (hours/days most active)
// /staff activity server		- server-wide activity overview
// /staff timeline @mod [days]	- view recent actions timeline (last 7/30 days)
// /staff targets @mod			- users this mod actions most frequently

var staffCmd = &discordgo.ApplicationCommand{
	Type:                     discordgo.ChatApplicationCommand,
	Name:                     "staff",
	Description:              "Staff management commands",
	DefaultMemberPermissions: &lib.Permissions.Administrator,
	Options: []*discordgo.ApplicationCommandOption{
		staffViewCmd,
		staffListCmd,
		staffNotesCmd,
		staffActivityCmd,
		staffTimelineCmd,
		staffTargetsCmd,
	},
}

func handleStaff(s *discordgo.Session, i *discordgo.InteractionCreate) *discordgo.InteractionResponse {
	switch c := i.ApplicationCommandData().Options[0]; c.Name {
	case "view":
		return handleStaffView(s, i)
	case "list":
		return handleStaffList(s, i)
	case "notes":
		return handleStaffNotes(s, i)
	case "activity":
		return handleStaffActivity(s, i)
	case "timeline":
		return handleStaffTimeline(s, i)
	case "targets":
		return handleStaffTargets(s, i)
	}

	return components.EmbedResponse(components.ErrorEmbed("Command does not exist"), true)
}
