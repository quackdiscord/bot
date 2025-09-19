package services

import (
	"os"

	r "github.com/go-redis/redis/v8"
	"github.com/rs/zerolog/log"
)

var Redis *r.Client

func ConnectRedis() {
	opt, _ := r.ParseURL(os.Getenv("REDIS_URL"))
	Redis = r.NewClient(opt)

	_, err := Redis.Ping(Redis.Context()).Result()
	if err != nil {
		log.Error().AnErr("Error connecting to Redis", err)
	}

	log.Info().Msg("Connected to Redis")
}

func DisconnectRedis() {
	Redis.Close()
	log.Info().Msg("Disconnected from Redis")
}
