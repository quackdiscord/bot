package routes

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/config"
	"github.com/quackdiscord/bot/internal/httpapi/middleware"
	httpplatform "github.com/quackdiscord/bot/internal/httpapi/platform"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/redis/go-redis/v9"
)

func TestAppealMemberRoutesReplayOriginalSubmission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	repository := newAppealRouteRepository()
	appeals := quack.NewAppealService(repository)
	cfg := config.Default()
	cfg.Limits.MemberRead.Max = 20
	services := &Deps{Services: &quack.Services{Cases: quack.NewCaseService(nil, nil, nil, nil)}, Config: cfg}
	router := gin.New()
	group := router.Group("/members/me")
	group.Use(func(c *gin.Context) {
		c.Set(middleware.ContextSessionKey, &quack.AuthSession{DiscordUserID: "target"})
		c.Next()
	})
	primitives := httpplatform.Primitives{RateLimits: httpplatform.NewRateLimiter(client, "test:appeal:rate:"), Idempotency: httpplatform.NewIdempotencyStore(client, "test:appeal:idempotency:")}
	if err := RegisterAppealAndMemberRoutes(group, services, appeals, primitives); err != nil {
		t.Fatalf("register appeal member routes: %v", err)
	}

	body := []byte(`{"answers":[{"question_id":"reason","value":"Please reconsider."}]}`)
	for attempt := 0; attempt < 2; attempt++ {
		request := httptest.NewRequest(http.MethodPost, "/members/me/cases/case-1/appeal", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", "same-submission")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusCreated {
			t.Fatalf("attempt %d status=%d body=%s", attempt+1, response.Code, response.Body.String())
		}
		if attempt == 1 && response.Header().Get("Idempotency-Replayed") != "true" {
			t.Fatalf("second submission was not replayed: headers=%v", response.Header())
		}
	}
	if repository.createCount != 1 {
		t.Fatalf("idempotent replay created %d appeals", repository.createCount)
	}
}

func TestAppealMemberRoutesFailClosedWithoutRedis(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repository := newAppealRouteRepository()
	services := &Deps{Services: &quack.Services{Cases: quack.NewCaseService(nil, nil, nil, nil)}, Config: config.Default()}
	router := gin.New()
	group := router.Group("/members/me")
	group.Use(func(c *gin.Context) {
		c.Set(middleware.ContextSessionKey, &quack.AuthSession{DiscordUserID: "target"})
		c.Next()
	})
	primitives := httpplatform.Primitives{RateLimits: httpplatform.NewRateLimiter(nil, ""), Idempotency: httpplatform.NewIdempotencyStore(nil, "")}
	if err := RegisterAppealAndMemberRoutes(group, services, quack.NewAppealService(repository), primitives); err != nil {
		t.Fatalf("register routes: %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "/members/me/cases/case-1", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable limiter did not fail closed: status=%d body=%s", response.Code, response.Body.String())
	}
}

type appealRouteRepository struct {
	mu          sync.Mutex
	caseModel   quack.Case
	appeal      *quack.Appeal
	events      []quack.AppealEvent
	createCount int
}

func newAppealRouteRepository() *appealRouteRepository {
	return &appealRouteRepository{caseModel: quack.Case{ULIDModel: quack.ULIDModel{ID: "case-1", CreatedAt: time.Now().UTC()}, GuildID: "guild-1", CaseNumber: 1, TargetDiscordUserID: "target", Reason: "Official reason", Validity: quack.CaseValidityValid, TemplateSnapshotJSON: `{"template":{"appealable":true}}`}}
}

func (r *appealRouteRepository) GetGuildAppealSettings(context.Context, string) (*quack.GuildAppealSettings, error) {
	return nil, nil
}

func (r *appealRouteRepository) UpdateGuildAppealSettings(context.Context, quack.UpdateGuildAppealSettingsParams) (*quack.GuildAppealSettings, error) {
	return nil, nil
}

func (r *appealRouteRepository) CreateAppeal(_ context.Context, params quack.CreateAppealParams) (*quack.Appeal, error) {
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

func (r *appealRouteRepository) GetAppealByID(context.Context, string) (*quack.Appeal, error) {
	return r.appeal, nil
}

func (r *appealRouteRepository) GetAppealByCaseID(context.Context, string) (*quack.Appeal, error) {
	return r.appeal, nil
}

func (r *appealRouteRepository) ListAppeals(context.Context, quack.AppealListParams) (*quack.AppealListResult, error) {
	return &quack.AppealListResult{}, nil
}

func (r *appealRouteRepository) ListAppealEvents(context.Context, string) ([]quack.AppealEvent, error) {
	return append([]quack.AppealEvent(nil), r.events...), nil
}

func (r *appealRouteRepository) AppendAppealInformation(context.Context, quack.AppendAppealInformationParams) (*quack.Appeal, error) {
	return r.appeal, nil
}

func (r *appealRouteRepository) TransitionAppeal(context.Context, quack.TransitionAppealParams) (*quack.Appeal, error) {
	return r.appeal, nil
}

func (r *appealRouteRepository) ClaimPendingAppealNotifications(context.Context, int) ([]quack.AppealNotification, error) {
	return nil, nil
}

func (r *appealRouteRepository) CompleteAppealNotification(context.Context, quack.CompleteAppealNotificationParams) error {
	return nil
}

func (r *appealRouteRepository) GetCaseByID(_ context.Context, id string) (*quack.Case, error) {
	if id != r.caseModel.ID {
		return nil, nil
	}
	item := r.caseModel
	return &item, nil
}

func (r *appealRouteRepository) ListCaseActionExecutions(context.Context, string) ([]quack.CaseActionExecution, error) {
	return nil, nil
}

func (r *appealRouteRepository) CreateAuditLogEntry(context.Context, *quack.AuditLogEntry) error {
	return nil
}
