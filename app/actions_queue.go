package app

import (
	"context"
	"time"

	"github.com/quackdiscord/bot/services"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
	"github.com/rs/zerolog/log"
)

const actionQueueEventType = "case_action_execution"

type actionQueuePayload struct {
	CaseID string
}

func enqueueCaseActions(caseID string) {
	if services.EQ == nil || !services.EQ.IsActive() || caseID == "" {
		return
	}

	services.EQ.Enqueue(structs.QueueEvent{
		Type:    actionQueueEventType,
		Data:    actionQueuePayload{CaseID: caseID},
		Handler: processCaseActionQueueEvent,
	})
}

func scheduleCaseActions(caseID string, delay time.Duration) {
	if delay <= 0 {
		enqueueCaseActions(caseID)
		return
	}

	go func() {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		<-timer.C
		enqueueCaseActions(caseID)
	}()
}

func EnqueuePendingCaseActions(ctx context.Context, store *storage.Store, limit int) error {
	caseIDs, err := store.ListExecutableCaseIDs(ctx, limit)
	if err != nil {
		return err
	}
	for _, caseID := range caseIDs {
		enqueueCaseActions(caseID)
	}
	return nil
}

func processCaseActionQueueEvent(dataStore structs.DataStore, data any) {
	store, ok := dataStore.(*storage.Store)
	if !ok || store == nil {
		log.Error().Msg("Action queue received unsupported datastore")
		return
	}

	payload, ok := data.(actionQueuePayload)
	if !ok || payload.CaseID == "" {
		log.Error().Interface("payload", data).Msg("Action queue received invalid payload")
		return
	}

	if err := NewActionService(store, NewDiscordActionClient()).ProcessCaseActions(context.Background(), payload.CaseID); err != nil {
		log.Error().
			Err(err).
			Str("case_id", payload.CaseID).
			Msg("Failed to process case actions")
	}
}
