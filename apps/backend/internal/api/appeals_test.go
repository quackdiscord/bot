package api

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/config"
	"github.com/quackdiscord/bot/internal/quack"
)

// sessionStore is storage that knows one live session, for tests that need
// authentication but no database.
type sessionStore struct {
	Storage
	session *quack.AuthSession
}

func (s sessionStore) GetSession(_ context.Context, id string) (*quack.AuthSession, error) {
	if id != s.session.ID {
		return nil, nil
	}
	copied := *s.session
	return &copied, nil
}

func (s sessionStore) RefreshSession(context.Context, *quack.AuthSession, time.Duration) (bool, error) {
	return true, nil
}

func TestAppealSubmissionReplaysOriginal(t *testing.T) {
	repository := newAppealRepository()
	session := testSession("target")
	server := newTestServer(t, config.Default(), Deps{
		Services: &quack.Services{
			Cases:   quack.NewCaseService(nil, nil, nil, nil),
			Appeals: quack.NewAppealService(repository),
		},
		Store: sessionStore{session: session},
	})
	body := `{"statement":"Please reconsider."}`
	for attempt := range 2 {
		response := send(t, server, http.MethodPost, "/members/me/cases/case-1/appeal", body, session.ID,
			idempotencyKeyHeader, "same-submission")
		expectStatus(t, response, http.StatusCreated)
		if replayed := response.Header().Get("Idempotency-Replayed") == "true"; replayed != (attempt == 1) {
			t.Fatalf("attempt %d replayed = %v", attempt+1, replayed)
		}
	}
	if repository.createCount != 1 {
		t.Fatalf("created %d appeals, want 1", repository.createCount)
	}
	expectStatus(t, send(t, server, http.MethodGet, "/members/me/appeals/appeal-1", "", session.ID), http.StatusOK)
	expectStatus(t, send(t, server, http.MethodPost, "/members/me/cases/case-1/appeal", body, session.ID,
		idempotencyKeyHeader, ""), http.StatusBadRequest)
}

type appealRepository struct {
	mu          sync.Mutex
	caseModel   quack.Case
	appeal      *quack.Appeal
	events      []quack.AppealEvent
	createCount int
}

func newAppealRepository() *appealRepository {
	return &appealRepository{caseModel: quack.Case{
		ULIDModel:            quack.ULIDModel{ID: "case-1", CreatedAt: time.Now().UTC()},
		GuildID:              "guild-1",
		CaseNumber:           1,
		TargetDiscordUserID:  "target",
		Reason:               "Official reason",
		Validity:             quack.CaseValidityValid,
		TemplateSnapshotJSON: `{"template":{"appealable":true}}`,
	}}
}

func (r *appealRepository) CreateAppeal(_ context.Context, params quack.CreateAppealParams) (*quack.Appeal, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.createCount++
	item := params.Appeal
	item.ID = "appeal-1"
	item.CreatedAt = time.Now().UTC()
	item.UpdatedAt = item.CreatedAt
	event := params.Event
	event.ID = "event-1"
	event.AppealID = item.ID
	event.CreatedAt = item.CreatedAt
	r.appeal = &item
	r.events = []quack.AppealEvent{event}
	return &item, nil
}

func (r *appealRepository) GetAppealByID(context.Context, string) (*quack.Appeal, error) {
	return r.appeal, nil
}

func (r *appealRepository) GetAppealByCaseID(context.Context, string) (*quack.Appeal, error) {
	return r.appeal, nil
}

func (r *appealRepository) ListAppeals(context.Context, quack.AppealListParams) (*quack.AppealListResult, error) {
	return &quack.AppealListResult{}, nil
}

func (r *appealRepository) ListAppealEvents(context.Context, string) ([]quack.AppealEvent, error) {
	return append([]quack.AppealEvent(nil), r.events...), nil
}

func (r *appealRepository) AppendAppealInformation(context.Context, quack.AppendAppealInformationParams) (*quack.Appeal, error) {
	return r.appeal, nil
}

func (r *appealRepository) TransitionAppeal(context.Context, quack.TransitionAppealParams) (*quack.Appeal, error) {
	return r.appeal, nil
}

func (r *appealRepository) ClaimPendingAppealNotifications(context.Context, int) ([]quack.AppealNotification, error) {
	return nil, nil
}

func (r *appealRepository) CompleteAppealNotification(context.Context, quack.CompleteAppealNotificationParams) error {
	return nil
}

func (r *appealRepository) GetCaseByID(_ context.Context, id string) (*quack.Case, error) {
	if id != r.caseModel.ID {
		return nil, nil
	}
	item := r.caseModel
	return &item, nil
}

func (r *appealRepository) ListCaseActionExecutions(context.Context, string) ([]quack.CaseActionExecution, error) {
	return nil, nil
}

func (r *appealRepository) GetGuildByDiscordID(context.Context, string) (*quack.Guild, error) {
	return nil, nil
}

func (r *appealRepository) GetGuildSettings(context.Context, string) (*quack.GuildSettings, error) {
	return nil, nil
}

func (r *appealRepository) CreateAuditLogEntry(context.Context, *quack.AuditLogEntry) error {
	return nil
}
