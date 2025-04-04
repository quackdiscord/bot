package commands

import (
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/config"
	"github.com/quackdiscord/bot/lib"
	"github.com/quackdiscord/bot/log"
	"github.com/quackdiscord/bot/services"
)

// Permissions
var banMembers int64 = discordgo.PermissionBanMembers
var kickMembers int64 = discordgo.PermissionKickMembers
var moderateMembers int64 = discordgo.PermissionModerateMembers
var administrator int64 = discordgo.PermissionAdministrator

func OnInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// start a timer to track how long the command takes
	start := time.Now()

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
		log.Error().Msgf("Something went wrong while processing a command: %s", i.ApplicationCommandData().Name)
		errMessage := config.Bot.ErrMsgPrefix + "Something went wrong while processing the command"
		s.InteractionRespond(i.Interaction, ContentResponse(errMessage, true))
		return
	}

	// stop the timer
	end := time.Now()
	lib.CmdRun(s, i, end.Sub(start))
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
