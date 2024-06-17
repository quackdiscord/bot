package commands

import (
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/config"
	"github.com/quackdiscord/bot/lib"
	"github.com/quackdiscord/bot/services"
)

// Permissions
var banMembers int64 = discordgo.PermissionBanMembers
var kickMembers int64 = discordgo.PermissionKickMembers
var moderateMembers int64 = discordgo.PermissionModerateMembers

func OnInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()
	cmd, ok := services.Commands[data.Name]
	if !ok {
		s.InteractionRespond(i.Interaction, ContentResponse(config.Bot.ErrMsgPrefix+"Command does not exist", true))
		return
	}

	resp := cmd.Handler(s, i)
	if resp != nil {
		s.InteractionRespond(i.Interaction, resp)
	} else {
		errMessage := config.Bot.ErrMsgPrefix + "Something went wrong while processing the command"
		s.InteractionRespond(i.Interaction, ContentResponse(errMessage, true))
		return
	}

	lib.CmdRun(s, i)
}

func LoadingResponse() *discordgo.InteractionResponse {
	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	}
}

func ContentResponse(c string, e bool) *discordgo.InteractionResponse {
	d := &discordgo.InteractionResponseData{
		Content:         c,
		AllowedMentions: new(discordgo.MessageAllowedMentions),
	}
	if e {
		d.Flags = discordgo.MessageFlagsEphemeral
	}

	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: d,
	}
}

func EmbedResponse(e *discordgo.MessageEmbed, f bool) *discordgo.InteractionResponse {
	d := &discordgo.InteractionResponseData{
		Embeds:          []*discordgo.MessageEmbed{e},
		AllowedMentions: new(discordgo.MessageAllowedMentions),
	}
	if f {
		d.Flags = discordgo.MessageFlagsEphemeral
	}

	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: d,
	}
}
