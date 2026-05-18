package services

import (
	"os"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/rs/zerolog/log"
)

func InitSentry() {
	// env := os.Getenv("ENVIORNMENT")
	// if env == "dev" {
	// 	log.Debug().Msg("Sentry is disabled in development mode")
	// 	return nil
	// }

	err := sentry.Init(sentry.ClientOptions{
		Dsn: os.Getenv("SENTRY_DSN"),
		// Set TracesSampleRate to 1.0 to capture 100%
		// of transactions for tracing.
		// We recommend adjusting this value in production,
		TracesSampleRate: 1.0,
	})

	if err != nil {
		log.Fatal().AnErr("Failed to initialize Sentry", err)
		os.Exit(1)
	}
	defer sentry.Flush(2 * time.Second)

	log.Info().Msg("Sentry initialized")
}

func CaptureError(err error) {
	if err != nil {
		sentry.CaptureException(err)
	}
}

func CaptureMessage(message string) {
	if message != "" {
		sentry.CaptureMessage(message)
	}
}

func CaptureEvent(event *sentry.Event) {
	if event != nil {
		sentry.CaptureEvent(event)
	}
}
