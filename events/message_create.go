package events

import (
	"fmt"
	"runtime"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/config"
	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/storage"
	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/mem"
)

func init() {
	Events = append(Events, onMessageCreate)
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
		}
	}
}

func statsCommand(s *discordgo.Session, m *discordgo.MessageCreate) {
	var heap runtime.MemStats
	runtime.ReadMemStats(&heap)

	cpuStat, err := cpu.Percent(time.Second, false)
	if err != nil {
		s.ChannelMessageSend(m.ChannelID, "Error getting CPU stats: "+err.Error())
		return
	}
	CPUPercent := fmt.Sprintf("%.1f%%", cpuStat[0])

	memStat, err := mem.VirtualMemory()
	if err != nil {
		s.ChannelMessageSend(m.ChannelID, "Error getting memory stats: "+err.Error())
		return
	}
	MemoryUsage := fmt.Sprintf("%.1f%%", memStat.UsedPercent)

	Servers := fmt.Sprint(len(s.State.Guilds))
	HeapUsed := fmt.Sprintf("%.1fMB", float64(heap.HeapInuse)/1024/1024)
	HeartbeatLatency := fmt.Sprint(s.HeartbeatLatency().Milliseconds())

	// Cache stats
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

	// Pings
	ctx := services.Redis.Context()
	start := time.Now()
	err = services.Redis.Ping(ctx).Err()
	RedisPing := "Error"
	if err == nil {
		RedisPing = fmt.Sprint(time.Since(start).Milliseconds())
	}

	start = time.Now()
	err = services.DB.PingContext(ctx)
	DBPing := "Error"
	if err == nil {
		DBPing = fmt.Sprint(time.Since(start).Milliseconds())
	}

	CmdsRun, err := services.Redis.HGet(ctx, "seeds:cmds", "total").Result()
	if err != nil {
		CmdsRun = "Error"
	}

	msg := fmt.Sprintf("```asciidoc\nQuack Stats\n\n"+
		"CPU            ::   %s      \n"+
		"RAM            ::   %s      \n"+
		"Heap           ::   %s      \n\n"+
		"Guilds         ::   %s      \n"+
		"Members        ::   %d      \n"+
		"Channels       ::   %d      \n"+
		"Roles          ::   %d      \n"+
		"Emojis         ::   %d      \n\n"+
		"Discord Ping   ::   %sms    \n"+
		"Redis Ping     ::   %sms    \n"+
		"DB Ping        ::   %sms    \n\n"+
		"Commands Run   ::   %s      \n"+
		"```", CPUPercent, MemoryUsage, HeapUsed, Servers, memberCount, channelCount, roleCount, emojiCount, HeartbeatLatency, RedisPing, DBPing, CmdsRun)

	s.ChannelMessageSend(m.ChannelID, msg)
}
