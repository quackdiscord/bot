package storage

import (
	"time"

	"github.com/quackdiscord/bot/lib"
	"github.com/quackdiscord/bot/structs"
)

func prepareULIDModel(model *structs.ULIDModel, now time.Time) error {
	if model.ID == "" {
		id, err := lib.NewULID()
		if err != nil {
			return err
		}
		model.ID = id
	}

	if model.CreatedAt.IsZero() {
		model.CreatedAt = now
	}
	model.UpdatedAt = now

	return nil
}
