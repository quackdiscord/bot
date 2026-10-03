package store

import (
	"time"

	"github.com/quackdiscord/bot/internal/quack"
)

// prepareULIDModel assigns a new record its ID and timestamps.
func prepareULIDModel(record *quack.ULIDModel, now time.Time) error {
	if record.ID == "" {
		record.ID = quack.NewID()
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	record.UpdatedAt = now
	return nil
}
