package routes

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// DiscordStatusProvider supplies discord status data without coupling callers to its source.
type DiscordStatusProvider interface {
	Status() (connected bool, username string, latencyMS int64)
}

// discordStatus identifies the supported discord status values stored and exchanged by Quack.
type discordStatus struct {
	Connected bool   `json:"connected"`
	Username  string `json:"username,omitempty"`
	Latency   int64  `json:"latency,omitempty"`
}

// redisStatus identifies the supported redis status values stored and exchanged by Quack.
type redisStatus struct {
	Connected bool  `json:"connected"`
	Latency   int64 `json:"latency,omitempty"`
}

// dbStatus identifies the supported db status values stored and exchanged by Quack.
type dbStatus struct {
	Connected bool  `json:"connected"`
	Latency   int64 `json:"latency,omitempty"`
}

// status encapsulates the status rule so callers share one consistent package implementation.
// @Summary Report service status
// @Tags Health
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /status [get]
func status(c *gin.Context, services *Deps, discord DiscordStatusProvider) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	c.JSON(http.StatusOK, gin.H{
		"discord":  getDiscordStatus(discord),
		"redis":    getRedisStatus(ctx, services),
		"database": getDBStatus(ctx, services),
	})
}

// getDiscordStatus retrieves discord status without exposing the underlying adapter implementation.
func getDiscordStatus(provider DiscordStatusProvider) discordStatus {
	if provider == nil {
		return discordStatus{Connected: false}
	}
	connected, username, latency := provider.Status()
	return discordStatus{Connected: connected, Username: username, Latency: latency}
}

// getRedisStatus retrieves redis status without exposing the underlying adapter implementation.
func getRedisStatus(ctx context.Context, services *Deps) redisStatus {
	if services == nil || services.Store == nil {
		return redisStatus{Connected: false}
	}

	start := time.Now()
	err := services.Store.PingRedis(ctx)
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

// getDBStatus retrieves dbstatus without exposing the underlying adapter implementation.
func getDBStatus(ctx context.Context, services *Deps) dbStatus {
	if services == nil || services.Store == nil {
		return dbStatus{Connected: false}
	}

	start := time.Now()
	err := services.Store.PingDatabase(ctx)
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
