package structs

import "time"

type BotStats struct {
	CPUPercent      float64
	MemoryUsage     float64
	HeapUsed        float64
	Uptime          time.Duration
	Servers         int
	MsgCacheSize    int
	MaxMsgCacheSize int
	MemberCount     int
	ChannelCount    int
	RoleCount       int
	EmoteCount      int
	EventQueueSize  int
	DiscordPingMS   int
	RedisPingMS     int
	DBPingMS        int
	RedisSizeMB     float64
	DBSizeMB        float64
	CommandsRun     int
	TotalCases      int
	TotalTickets    int
	TotalAppeals    int
}
