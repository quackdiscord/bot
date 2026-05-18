package config

import (
	"encoding/json"
	"os"

	"github.com/getsentry/sentry-go"
	"github.com/rs/zerolog/log"
)

type Config struct {
	GuildCountChannel string `json:"guild_count_channel"`
	DevGuildID        string `json:"dev_guild_id"`
	ErrMsgPrefix      string `json:"error_msg_prefix"`
	BotOwnerID        string `json:"bot_owner_id"`
	MessageCacheSize  int    `json:"message_cache_size"`
	EventQueueSize    int    `json:"event_queue_size"`
	EventQueueWorkers int    `json:"event_queue_workers"`
}

var Bot Config

func init() {
	// This can also be replaced with code to read from a file or environment variables
	Bot = Config{
		GuildCountChannel: "val",
		DevGuildID:        "val",
		ErrMsgPrefix:      "val",
		BotOwnerID:        "val",
		MessageCacheSize:  0,
		EventQueueSize:    0,
		EventQueueWorkers: 0,
	}

	// Optionally, load config from a JSON file
	file, err := os.Open("config.json")
	if err == nil {
		defer file.Close()
		decoder := json.NewDecoder(file)
		err = decoder.Decode(&Bot)
		if err != nil {
			log.Error().AnErr("Failed to decode config.json", err)
			sentry.CaptureException(err)
		}
		log.Info().Msg("Loaded config.json")
	} else {
		log.Error().Msg("Failed to open config.json")
		sentry.CaptureException(err)
	}
}
