package events

import (
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/components"
	"github.com/quackdiscord/bot/config"
	"github.com/quackdiscord/bot/log"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/storage"
	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/mem"
)

var startTime time.Time

func init() {
	Events = append(Events, onMessageCreate)
	startTime = time.Now()
}

func onMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	// access the message cache
	services.MsgCache.AddMessage(m.Message)

	if m.Author.ID != s.State.User.ID {
		// store the message in redis (this will check if the message is in a ticket automatically)
		storage.StoreTicketMessage(m.ChannelID, m.Content, m.Author.Username)
	}

	if m.Author.ID == config.Bot.BotOwnerID {
		prefix := "!!!"
		command := strings.Split(m.Content, " ")[0]
		if command == prefix+"stats" {
			statsCommand(s, m)
			log.Info().Msg("Owner stats command executed")
		} else if command == prefix+"guild" {
			guildCommand(s, m)
			log.Info().Msg("Owner guild command executed")
		} else if command == prefix+"cmdstats" {
			cmdStatsCommand(s, m)
			log.Info().Msg("Owner cmdstats command executed")
		}
	}
}

func statsCommand(s *discordgo.Session, m *discordgo.MessageCreate) {

	var heap runtime.MemStats
	runtime.ReadMemStats(&heap)

	cpuStat, _ := cpu.Times(true)
	totalDelta := float64(cpuStat[0].Total())
	idleDelta := float64(cpuStat[0].Idle)

	memStat, _ := mem.VirtualMemory()
	usedMemory := float64(memStat.Used)
	totalMemory := float64(memStat.Total)

	uptime := time.Since(startTime)

	Servers := fmt.Sprint(len(s.State.Guilds))
	CPUPercent := fmt.Sprintf("%.1f%%", (totalDelta-idleDelta)/totalDelta*100)
	MemoryUsage := fmt.Sprintf("%.1f%%", usedMemory/totalMemory*100)
	HeapUsed := fmt.Sprintf("%.1fMB", float64(heap.HeapInuse)/1024/1024)
	HeartbeatLatency := fmt.Sprint(s.HeartbeatLatency().Milliseconds())
	CmdsRun := fmt.Sprint(services.Redis.HGet(services.Redis.Context(), "seeds:cmds", "total").Val())

	// cache stats
	msgCacheSize := len(services.MsgCache.Messages)
	eventQueueSize := services.EQ.GetQueueSize()
	memberCount := 0
	channelCount := 0
	roleCount := 0
	emojiCount := 0
	for _, guild := range s.State.Guilds {
		memberCount += guild.MemberCount
		channelCount += len(guild.Channels)
		roleCount += len(guild.Roles)
		emojiCount += len(guild.Emojis)
	}

	// pings
	start := time.Now()
	// ping redis
	services.Redis.Ping(services.Redis.Context())
	end := time.Now()
	RedisPing := fmt.Sprint(end.Sub(start).Milliseconds())

	start = time.Now()
	services.DB.Ping()
	end = time.Now()
	DBPing := fmt.Sprint(end.Sub(start).Milliseconds())

	msg := fmt.Sprintf("```asciidoc\nQuack Stats\n\n"+
		"CPU            ::   %s      \n"+
		"RAM            ::   %s      \n"+
		"Heap           ::   %s      \n"+
		"Uptime         ::   %s      \n\n"+

		"Guilds         ::   %s      \n"+
		"Messages       ::   %d / 5000 \n"+
		"Members        ::   %d      \n"+
		"Channels       ::   %d      \n"+
		"Roles          ::   %d      \n"+
		"Emojis         ::   %d      \n"+
		"Events         ::   %d (in queue) \n\n"+

		"Discord Ping   ::   %sms    \n"+
		"Redis Ping     ::   %sms    \n"+
		"DB Ping        ::   %sms    \n\n"+

		"Commands Run   ::   %s      \n"+
		"```", CPUPercent, MemoryUsage, HeapUsed, uptime.Round(time.Second).String(), Servers, msgCacheSize, memberCount, channelCount, roleCount, emojiCount, eventQueueSize, HeartbeatLatency, RedisPing, DBPing, CmdsRun)

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
