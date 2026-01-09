package events

import (
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/config"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/utils"
	"github.com/rs/zerolog/log"
)

func init() {
	Events = append(Events, onMessageCreate)
}

func onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	// access the message cache
	if m.Message != nil {
		services.MsgCache.AddMessage(m.Message)
	}

	isHoneypot := false
	if m.Author != nil && m.Author.ID != s.State.User.ID {
		// store the message in redis (this will check if the message is in a ticket automatically)
		storage.StoreTicketMessage(m.ChannelID, m.Content, m.Author.Username)
		isHoneypot = storage.IsHoneypotChannel(m.ChannelID)
	}

	if isHoneypot {
		HandleHoneypotMessage(s, m)
	}

	if m.Author != nil && m.Author.ID == config.Bot.BotOwnerID {
		prefix := "!!!"
		command := strings.Split(m.Content, " ")[0]
		switch command {
		case prefix + "stats":
			statsCommand(s, m)
			log.Info().Msg("Owner stats command executed")
		case prefix + "guild":
			guildCommand(s, m)
			log.Info().Msg("Owner guild command executed")
		case prefix + "cmdstats":
			cmdStatsCommand(s, m)
			log.Info().Msg("Owner cmdstats command executed")
		case prefix + "modcmdstats":
			modCmdStatsCommand(s, m)
			log.Info().Msg("Owner modcmdstats command executed")
		case prefix + "savestats":
			utils.CollectAndSaveStats(s)
			s.ChannelMessageSend(m.ChannelID, "Stats saved")
			log.Info().Msg("Owner savestats command executed")
		}
	}
}

func statsCommand(s *discordgo.Session, m *discordgo.MessageCreate) {
	stats := utils.GetBotStats(s)
	msg := utils.FormatStatsAsCodeBlock(stats)
	s.ChannelMessageSend(m.ChannelID, msg)
}

func guildCommand(s *discordgo.Session, m *discordgo.MessageCreate) {
	var guild *discordgo.Guild
	// get the guild id by splitting the message at the space
	if !strings.Contains(m.Content, " ") {
		s.ChannelMessageSend(m.ChannelID, "Please provide a guild ID")
		return
	}

	guildID := strings.Split(m.Content, " ")[1]

	if guildID == "" {
		s.ChannelMessageSend(m.ChannelID, "Please provide a guild ID")
		return
	}

	// get the guild
	guild, err := s.GuildWithCounts(guildID)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get guild")
		services.CaptureError(err)
		s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Failed to get guild %s ```%s```", guildID, err))
		return
	}

	desc := fmt.Sprintf(
		"**Description:**\n<:text:1229343822337802271>%s\n\n**Numbers**\n<:text2:1229344477131309136>**Members:** %d / %d\n<:text2:1229344477131309136>**Boost Tier:** %d\n<:text:1229343822337802271>**Emojis:** %d\n\n**Owner:**\n<:text:1229343822337802271> <:owner:1230302954683367436> <@%s>",
		guild.Description, guild.ApproximateMemberCount, guild.MaxMembers, guild.PremiumTier, len(guild.Emojis), guild.OwnerID,
	)

	if guild.VanityURLCode != "" {
		desc += "\n\nhttps://discord.gg/" + guild.VanityURLCode
	} else {
		inv, err := s.GuildInvites(guild.ID)
		if err != nil {
			log.Error().Err(err).Msg("Failed to get guild invites")
			services.CaptureError(err)
			desc += "\n\nFailed to get guild invites"
		} else {
			if len(inv) > 0 {
				desc += "\n\nhttps://discord.gg/" + inv[0].Code
			}
		}
	}

	embed := components.NewEmbed().
		SetColor("DarkButNotBlack").
		SetAuthor(guild.Name, guild.IconURL("")).
		SetDescription(desc).
		SetFooter(guild.ID).
		SetTimestamp().
		MessageEmbed

	s.ChannelMessageSendEmbed(m.ChannelID, embed)

}

func cmdStatsCommand(s *discordgo.Session, m *discordgo.MessageCreate) {
	// the command to get stats for is <prefix>cmdstats <command>
	// get the command count from redis

	command := strings.Split(m.Content, " ")[1]

	if command == "" {
		s.ChannelMessageSend(m.ChannelID, "Please provide a command")
		return
	}

	count := services.Redis.HGet(services.Redis.Context(), "seeds:cmds", command).Val()

	s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Command `%s` has been run `%s` times", command, count))
}

func modCmdStatsCommand(s *discordgo.Session, m *discordgo.MessageCreate) {
	cmd := strings.Split(m.Content, " ")[1]
	user := strings.Split(m.Content, " ")[2]
	count := 0
	typeint := 0

	switch cmd {
	case "ban":
		typeint = 1
	case "warn":
		typeint = 0
	case "kick":
		typeint = 2
	case "timeout":
		typeint = 4
	default:
		s.ChannelMessageSend(m.ChannelID, "Invalid command")
		return
	}

	user = strings.TrimPrefix(user, "<@")
	user = strings.TrimSuffix(user, ">")

	row := services.DB.QueryRow("SELECT COUNT(*) FROM cases WHERE moderator_id = ? AND type = ? AND guild_id = ?", user, typeint, m.GuildID)
	err := row.Scan(&count)
	if err != nil {
		log.Error().Err(err).Msg("Failed to get mod cmd stats")
		services.CaptureError(err)
		s.ChannelMessageSend(m.ChannelID, "Failed to get mod cmd stats")
		return
	}

	s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Command `%s` has been run `%d` times by <@%s> in this server.", cmd, count, user))
}
