package storage

import (
	"time"

	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/structs"
)

func SaveStats(stats structs.BotStats) error {
	// prepare the statement
	stmtIns, err := services.DB.Prepare("INSERT INTO bot_stats (cpu_percent, memory_usage, heap_used, uptime, servers, msg_cache_size, max_msg_cache_size, member_count, channel_count, role_count, emote_count, event_queue_size, discord_ping_ms, redis_ping_ms, db_ping_ms, redis_size_mb, db_size_mb, commands_run, total_cases, total_tickets, total_appeals) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
	if err != nil {
		return err
	}

	// execute the statement - convert Uptime to nanoseconds for storage
	_, err = stmtIns.Exec(stats.CPUPercent, stats.MemoryUsage, stats.HeapUsed, stats.Uptime.Nanoseconds(), stats.Servers, stats.MsgCacheSize, stats.MaxMsgCacheSize, stats.MemberCount, stats.ChannelCount, stats.RoleCount, stats.EmoteCount, stats.EventQueueSize, stats.DiscordPingMS, stats.RedisPingMS, stats.DBPingMS, stats.RedisSizeMB, stats.DBSizeMB, stats.CommandsRun, stats.TotalCases, stats.TotalTickets, stats.TotalAppeals)
	if err != nil {
		return err
	}
	return nil
}

func GetLatestStats() (*structs.BotStats, error) {
	stmt, err := services.DB.Prepare("SELECT cpu_percent, memory_usage, heap_used, uptime, servers, msg_cache_size, max_msg_cache_size, member_count, channel_count, role_count, emote_count, event_queue_size, discord_ping_ms, redis_ping_ms, db_ping_ms, redis_size_mb, db_size_mb, commands_run, total_cases, total_tickets, total_appeals FROM bot_stats ORDER BY created_at DESC LIMIT 1")
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	var stats structs.BotStats
	var uptimeNanos int64
	err = stmt.QueryRow().Scan(&stats.CPUPercent, &stats.MemoryUsage, &stats.HeapUsed, &uptimeNanos, &stats.Servers, &stats.MsgCacheSize, &stats.MaxMsgCacheSize, &stats.MemberCount, &stats.ChannelCount, &stats.RoleCount, &stats.EmoteCount, &stats.EventQueueSize, &stats.DiscordPingMS, &stats.RedisPingMS, &stats.DBPingMS, &stats.RedisSizeMB, &stats.DBSizeMB, &stats.CommandsRun, &stats.TotalCases, &stats.TotalTickets, &stats.TotalAppeals)
	if err != nil {
		return nil, err
	}
	stats.Uptime = time.Duration(uptimeNanos)
	return &stats, nil
}
