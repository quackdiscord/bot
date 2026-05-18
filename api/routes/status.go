package routes

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/app"
	"github.com/quackdiscord/bot/discord"
)

type discordStatus struct {
	Connected bool   `json:"connected"`
	Username  string `json:"username,omitempty"`
	Latency   int64  `json:"latency,omitempty"`
}

type redisStatus struct {
	Connected bool  `json:"connected"`
	Latency   int64 `json:"latency,omitempty"`
}

type dbStatus struct {
	Connected bool  `json:"connected"`
	Latency   int64 `json:"latency,omitempty"`
}

func status(c *gin.Context, services *app.Services) {
	c.JSON(http.StatusOK, gin.H{
		"discord":  getDiscordStatus(),
		"redis":    getRedisStatus(services),
		"database": getDBStatus(services),
	})
}

func getDiscordStatus() discordStatus {
	if discord.Session == nil || discord.Session.State == nil {
		return discordStatus{Connected: false}
	}

	user := discord.Session.State.User
	if user != nil {
		return discordStatus{
			Connected: true,
			Username:  user.Username,
			Latency:   discord.Session.HeartbeatLatency().Milliseconds(),
		}
	}
	return discordStatus{
		Connected: false,
	}
}

func getRedisStatus(services *app.Services) redisStatus {
	if services == nil || services.Store == nil || services.Store.Redis() == nil {
		return redisStatus{Connected: false}
	}

	start := time.Now()
	_, err := services.Store.Redis().Ping(context.Background()).Result()
	if err != nil {
		return redisStatus{
			Connected: false,
		}
	}
	latency := time.Since(start).Milliseconds()
	return redisStatus{
		Connected: true,
		Latency:   int64(latency),
	}
}

func getDBStatus(services *app.Services) dbStatus {
	if services == nil || services.Store == nil || services.Store.DB() == nil {
		return dbStatus{Connected: false}
	}

	start := time.Now()
	sqlDB, err := services.Store.DB().DB()
	if err != nil {
		return dbStatus{
			Connected: false,
		}
	}

	err = sqlDB.Ping()
	if err != nil {
		return dbStatus{
			Connected: false,
		}
	}
	latency := time.Since(start).Milliseconds()
	return dbStatus{
		Connected: true,
		Latency:   int64(latency),
	}
}
