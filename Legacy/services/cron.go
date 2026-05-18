package services

import (
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/robfig/cron/v3"
	"github.com/rs/zerolog/log"
)

var CronScheduler *cron.Cron
var statsCollector func(*discordgo.Session)

// StartCron initializes and starts the cron scheduler
func StartCron(session *discordgo.Session) {
	CronScheduler = cron.New(cron.WithLocation(time.UTC))

	// Schedule hourly stats collection
	_, err := CronScheduler.AddFunc("0 * * * *", func() {
		if statsCollector != nil {
			statsCollector(session)
		}
	})
	if err != nil {
		log.Error().Err(err).Msg("Failed to schedule stats collection cron job")
		CaptureError(err)
		return
	}

	CronScheduler.Start()
	log.Info().Msg("Cron scheduler started - stats collection scheduled hourly")
}

// StopCron gracefully stops the cron scheduler
func StopCron() {
	if CronScheduler != nil {
		ctx := CronScheduler.Stop()
		<-ctx.Done()
		log.Info().Msg("Cron scheduler stopped")
	}
}

// RegisterStatsCollector sets the function to be called for stats collection
func RegisterStatsCollector(collector func(*discordgo.Session)) {
	statsCollector = collector
}
