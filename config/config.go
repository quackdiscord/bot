package config

import (
	"encoding/json"
	"os"

	log "github.com/sirupsen/logrus"
)

type Config struct {
	GuildCountChannel string `json:"guild_count_channel"`
	DevGuildID        string `json:"dev_guild_id"`
	ErrMsgPrefix      string `json:"error_msg_prefix"`
}

var Bot Config

func init() {
	// This can also be replaced with code to read from a file or environment variables
	Bot = Config{
		GuildCountChannel: "val",
		DevGuildID:        "val",
		ErrMsgPrefix:      "val",
	}

	// Optionally, load config from a JSON file
	file, err := os.Open("config.json")
	if err == nil {
		defer file.Close()
		decoder := json.NewDecoder(file)
		err = decoder.Decode(&Bot)
		if err != nil {
			log.WithError(err).Fatal("Error decoding config.json")
		}
		log.Info("Loaded config.json")
	} else {
		log.Error("Could not open config.json, using default config")
	}
}
