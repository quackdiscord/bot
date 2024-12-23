package commands

import (
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/utils"
)

func init() {
	services.Commands[purgeCmd.Name] = &services.Command{
		ApplicationCommand: purgeCmd,
		Handler:            handlePurge,
	}
}

var purgeCmd = &discordgo.ApplicationCommand{
	Type:                     discordgo.ChatApplicationCommand,
	Name:                     "purge",
	Description:              "Purge messages from the server",
	DefaultMemberPermissions: &moderateMembers,
	Options: []*discordgo.ApplicationCommandOption{
		purgeAllCmd,
		purgeUserCmd,
		purgeQuackCmd,
		purgeEmojiCmd,
		purgeContainsCmd,
		purgeBotsCmd,
		purgeEmbedsCmd,
		purgeAttachmentsCmd,
	},
}

func handlePurge(s *discordgo.Session, i *discordgo.InteractionCreate) (resp *discordgo.InteractionResponse) {
	if !utils.CheckPerms(i.Member, moderateMembers) {
		return EmbedResponse(components.ErrorEmbed("You do not have the permissions required to use this command."), true)
	}

	switch c := i.ApplicationCommandData().Options[0]; c.Name {
	case "all":
		return handlePurgeAll(s, i)
	case "user":
		return handlePurgeUser(s, i)
	case "quack":
		return handlePurgeQuack(s, i)
	case "emoji":
		return handlePurgeEmoji(s, i)
	case "contains":
		return handlePurgeContains(s, i)
	case "bots":
		return handlePurgeBots(s, i)
	case "embeds":
		return handlePurgeEmbeds(s, i)
	case "attachments":
		return handlePurgeAttachments(s, i)
	}

	return EmbedResponse(components.ErrorEmbed("Command does not exist"), true)
}
