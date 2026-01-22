package structs

import "time"

type BotStats struct {
	CPUPercent      float64       `json:"cpu_percent"`
	MemoryUsage     float64       `json:"memory_usage"`
	HeapUsed        float64       `json:"heap_used"`
	Uptime          time.Duration `json:"uptime"`
	Servers         int           `json:"servers"`
	MsgCacheSize    int           `json:"msg_cache_size"`
	MaxMsgCacheSize int           `json:"max_msg_cache_size"`
	MemberCount     int           `json:"member_count"`
	ChannelCount    int           `json:"channel_count"`
	RoleCount       int           `json:"role_count"`
	EmoteCount      int           `json:"emote_count"`
	EventQueueSize  int           `json:"event_queue_size"`
	DiscordPingMS   int           `json:"discord_ping_ms"`
	RedisPingMS     int           `json:"redis_ping_ms"`
	DBPingMS        int           `json:"db_ping_ms"`
	RedisSizeMB     float64       `json:"redis_size_mb"`
	DBSizeMB        float64       `json:"db_size_mb"`
	CommandsRun     int           `json:"commands_run"`
	TotalCases      int           `json:"total_cases"`
	TotalTickets    int           `json:"total_tickets"`
	TotalAppeals    int           `json:"total_appeals"`
}
