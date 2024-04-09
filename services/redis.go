package services

import (
	"os"

	r "github.com/go-redis/redis/v8"
	"github.com/sirupsen/logrus"
)

var Redis *r.Client

func ConnectRedis() {
	opt, _ := r.ParseURL(os.Getenv("REDIS_URL"))
	Redis = r.NewClient(opt)

	_, err := Redis.Ping(Redis.Context()).Result()
	if err != nil {
		logrus.WithError(err).Fatal("Error connecting to Redis")
	}

	logrus.Info("Connected to Redis")
}

