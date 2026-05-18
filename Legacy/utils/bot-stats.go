package utils

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/config"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
	"github.com/rs/zerolog/log"
	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/mem"
)

var startTime time.Time

func init() {
	startTime = time.Now()
}

// GetBotStats gathers comprehensive bot statistics
func GetBotStats(s *discordgo.Session) structs.BotStats {
	stats := structs.BotStats{}

	// System stats
	var heap runtime.MemStats
	runtime.ReadMemStats(&heap)

	cpuStat, _ := cpu.Times(true)
	if len(cpuStat) > 0 {
		totalDelta := float64(cpuStat[0].Total())
		idleDelta := float64(cpuStat[0].Idle)
		stats.CPUPercent = (totalDelta - idleDelta) / totalDelta * 100
	}

	memStat, _ := mem.VirtualMemory()
	if memStat != nil {
		stats.MemoryUsage = float64(memStat.Used) / float64(memStat.Total) * 100
	}

	stats.HeapUsed = float64(heap.HeapInuse) / 1024 / 1024
	stats.Uptime = time.Since(startTime)

	// Discord stats
	stats.Servers = len(s.State.Guilds)
	stats.MsgCacheSize = len(services.MsgCache.Messages)
	stats.MaxMsgCacheSize = config.Bot.MessageCacheSize
	stats.EventQueueSize = services.EQ.GetQueueSize()

	// Guild aggregation
	for _, guild := range s.State.Guilds {
		stats.MemberCount += guild.MemberCount
		stats.ChannelCount += len(guild.Channels)
		stats.RoleCount += len(guild.Roles)
		stats.EmoteCount += len(guild.Emojis)
	}

	// Latency measurements
	stats.DiscordPingMS = int(s.HeartbeatLatency().Milliseconds())
	stats.RedisPingMS = measureRedisPing()
	stats.DBPingMS = measureDBPing()

	// Storage sizes
	stats.DBSizeMB = getDBSize()
	stats.RedisSizeMB = getRedisSizeMB()

	// Command stats
	commandsRunStr := services.Redis.HGet(services.Redis.Context(), "seeds:cmds", "total").Val()
	if commandsRun, err := strconv.Atoi(commandsRunStr); err == nil {
		stats.CommandsRun = commandsRun
	}

	// Database counts
	stats.TotalCases = getCaseCount()
	stats.TotalTickets = getTicketCount()
	stats.TotalAppeals = getAppealCount()

	return stats
}

// Helper functions for measurements and data gathering
func measureRedisPing() int {
	start := time.Now()
	services.Redis.Ping(services.Redis.Context())
	return int(time.Since(start).Milliseconds())
}

func measureDBPing() int {
	start := time.Now()
	services.DB.Ping()
	return int(time.Since(start).Milliseconds())
}

func getDBSize() float64 {
	row := services.DB.QueryRow("SELECT SUM(data_length + index_length) / 1024 / 1024 AS size_mb FROM information_schema.tables WHERE table_schema = 'default'")
	var size float64
	if err := row.Scan(&size); err != nil {
		log.Error().Err(err).Msg("Failed to get DB size")
		services.CaptureError(err)
		return 0
	}
	return size
}

func getRedisSizeMB() float64 {
	redisInfo := services.Redis.Info(services.Redis.Context(), "memory").Val()
	if strings.Contains(redisInfo, "used_memory:") {
		lines := strings.SplitSeq(redisInfo, "\n")
		for line := range lines {
			if after, ok := strings.CutPrefix(line, "used_memory:"); ok {
				sizeStr := after
				sizeStr = strings.TrimSpace(sizeStr)
				if sizeBytes, err := strconv.ParseFloat(sizeStr, 64); err == nil {
					return sizeBytes / 1024 / 1024 // Convert to MB
				}
				break
			}
		}
	}
	return 0
}

func getCaseCount() int {
	row := services.DB.QueryRow("SELECT COUNT(*) FROM cases")
	var count int
	if err := row.Scan(&count); err != nil {
		log.Error().Err(err).Msg("Failed to get case count")
		services.CaptureError(err)
		return 0
	}
	return count
}

func getTicketCount() int {
	row := services.DB.QueryRow("SELECT COUNT(*) FROM tickets")
	var count int
	if err := row.Scan(&count); err != nil {
		log.Error().Err(err).Msg("Failed to get ticket count")
		services.CaptureError(err)
		return 0
	}
	return count
}

func getAppealCount() int {
	row := services.DB.QueryRow("SELECT COUNT(*) FROM appeals")
	var count int
	if err := row.Scan(&count); err != nil {
		log.Error().Err(err).Msg("Failed to get appeal count")
		services.CaptureError(err)
		return 0
	}
	return count
}

// FormatStatsAsCodeBlock formats the stats as an ASCII-doc codeblock
func FormatStatsAsCodeBlock(stats structs.BotStats) string {
	return fmt.Sprintf("```asciidoc\n"+
		"CPU            ::   %.1f%%     \n"+
		"RAM            ::   %.1f%%     \n"+
		"Heap           ::   %.1fMB     \n"+
		"Uptime         ::   %s      \n\n"+

		"Guilds         ::   %d      \n"+
		"Messages       ::   %d / %d \n"+
		"Members        ::   %d      \n"+
		"Channels       ::   %d      \n"+
		"Roles          ::   %d      \n"+
		"Emojis         ::   %d      \n"+
		"Events         ::   %d (in queue) \n\n"+

		"Discord Ping   ::   %dms    \n"+
		"Redis Ping     ::   %dms    \n"+
		"DB Ping        ::   %dms    \n\n"+
		"DB Size        ::   %.1fMB  \n"+
		"Redis Size     ::   %.1fMB  \n\n"+

		"Commands Run   ::   %d      \n\n"+

		"Total Cases    ::   %d      \n"+
		"Total Tickets  ::   %d      \n"+
		"Total Appeals  ::   %d      \n"+
		"```",
		stats.CPUPercent, stats.MemoryUsage, stats.HeapUsed, stats.Uptime.Round(time.Second).String(),
		stats.Servers, stats.MsgCacheSize, stats.MaxMsgCacheSize, stats.MemberCount, stats.ChannelCount, stats.RoleCount, stats.EmoteCount, stats.EventQueueSize,
		stats.DiscordPingMS, stats.RedisPingMS, stats.DBPingMS, stats.DBSizeMB, stats.RedisSizeMB,
		stats.CommandsRun, stats.TotalCases, stats.TotalTickets, stats.TotalAppeals)
}

// CollectAndSaveStats gathers bot stats and saves them to storage
func CollectAndSaveStats(session *discordgo.Session) {
	log.Info().Msg("Collecting and saving bot stats...")

	stats := GetBotStats(session)

	if err := storage.SaveStats(stats); err != nil {
		log.Error().Err(err).Msg("Failed to save bot stats")
		services.CaptureError(err)
		return
	}

	log.Info().Msg("Bot stats successfully saved")
}
