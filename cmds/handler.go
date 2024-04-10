package cmds

import (
	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/lib"
)

var Commands = make(map[string]*Command)

type Command struct {
	*discordgo.ApplicationCommand
	Handler func(*discordgo.Session, *discordgo.InteractionCreate) *discordgo.InteractionResponse
}

// Permissions
var banMembers int64 = discordgo.PermissionBanMembers

func OnInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()
	cmd, ok := Commands[data.Name]
	if !ok {
		return
	}

	resp := cmd.Handler(s, i)
	if resp != nil {
		s.InteractionRespond(i.Interaction, resp)
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
