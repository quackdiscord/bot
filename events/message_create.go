package events

import (
	"fmt"
	"runtime"
	"time"

	"github.com/bwmarrin/discordgo"
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
		if m.Content == prefix+"stats" {
			statsCommand(s, m)
			log.Info().Msg("Owner stats command executed")
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
		"Emojis         ::   %d      \n\n"+

		"Discord Ping   ::   %sms    \n"+
		"Redis Ping     ::   %sms    \n"+
		"DB Ping        ::   %sms    \n\n"+

		"Commands Run   ::   %s      \n"+
		"```", CPUPercent, MemoryUsage, HeapUsed, uptime.Round(time.Second).String(), Servers, msgCacheSize, memberCount, channelCount, roleCount, emojiCount, HeartbeatLatency, RedisPing, DBPing, CmdsRun)

	s.ChannelMessageSend(m.ChannelID, msg)
}
