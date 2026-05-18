package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/app"
	"github.com/quackdiscord/bot/internal/testutil"
	"github.com/quackdiscord/bot/storage"
	"github.com/quackdiscord/bot/structs"
)

func TestSetupRoutesStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	SetupRoutes(router, app.New(nil))

	request := httptest.NewRequest(http.MethodGet, "/status", nil)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, response.Code)
	}

	var body map[string]map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode status response: %v", err)
	}

	if body["discord"]["connected"] != false {
		t.Fatalf("expected discord to be disconnected in route smoke test")
	}
	if body["redis"]["connected"] != false {
		t.Fatalf("expected redis to be disconnected in route smoke test")
	}
	if body["database"]["connected"] != false {
		t.Fatalf("expected database to be disconnected in route smoke test")
	}
}

func TestGuildMeRouteAuthenticated(t *testing.T) {
	testutil.SetTestConfig(t)
	gin.SetMode(gin.TestMode)

	store := testutil.NewSQLiteRedisStore(t)
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate schema: %v", err)
	}

	services := app.NewWithDiscordClient(store, routeFakeDiscordClient{
		userGuilds: []app.DiscordUserGuild{{
			ID:          "guild-1",
			Permissions: uint64(discordgo.PermissionModerateMembers),
		}},
		botGuild: &app.DiscordBotGuild{ID: "guild-1", Name: "Guild", Icon: "icon", OwnerID: "owner-1"},
	})

	session := routeTestSession("user-1")
	if err := store.SaveSession(context.Background(), session, time.Hour); err != nil {
		t.Fatalf("save session: %v", err)
	}

	router := gin.New()
	SetupRoutes(router, services)

	request := httptest.NewRequest(http.MethodGet, "/guilds/guild-1/me", nil)
	request.Header.Set("Authorization", "Bearer "+session.ID)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, response.Code, response.Body.String())
	}

	var body struct {
		Guild struct {
			ID               string `json:"id"`
			DiscordGuildID   string `json:"discord_guild_id"`
			Name             string `json:"name"`
			OwnerDiscordUser string `json:"owner_discord_user_id"`
		} `json:"guild"`
		Staff struct {
			ID             string `json:"id"`
			DiscordUserID  string `json:"discord_user_id"`
			PermissionBits string `json:"permission_bits"`
		} `json:"staff"`
		Permissions map[string]bool `json:"permissions"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode guild me response: %v", err)
	}

	if body.Guild.ID == "" || body.Guild.DiscordGuildID != "guild-1" || body.Guild.Name != "Guild" {
		t.Fatalf("unexpected guild payload: %+v", body.Guild)
	}
	if body.Staff.ID == "" || body.Staff.DiscordUserID != "user-1" {
		t.Fatalf("unexpected staff payload: %+v", body.Staff)
	}
	if body.Permissions[string(structs.PermissionActionCaseCreate)] != true {
		t.Fatalf("expected case.create permission")
	}
	if body.Permissions[string(structs.PermissionActionCaseTemplateWrite)] != false {
		t.Fatalf("did not expect case_template.write permission")
	}
}

func TestGuildMeRouteUnauthenticated(t *testing.T) {
	testutil.SetTestConfig(t)
	gin.SetMode(gin.TestMode)

	router := gin.New()
	SetupRoutes(router, app.New(nil))

	request := httptest.NewRequest(http.MethodGet, "/guilds/guild-1/me", nil)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, response.Code)
	}
}

func TestListUserGuildsRouteAuthenticated(t *testing.T) {
	testutil.SetTestConfig(t)
	gin.SetMode(gin.TestMode)

	store := testutil.NewSQLiteRedisStore(t)
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate schema: %v", err)
	}

	services := app.NewWithDiscordClient(store, routeFakeDiscordClient{
		userGuilds: []app.DiscordUserGuild{
			{ID: "guild-1", Name: "Guild One", Owner: true},
			{ID: "guild-2", Name: "Guild Two", Permissions: uint64(discordgo.PermissionManageGuild)},
			{ID: "guild-3", Name: "Guild Three", Permissions: uint64(discordgo.PermissionSendMessages)},
		},
		botGuilds: []app.DiscordBotGuild{{ID: "guild-2", Name: "Guild Two"}},
	})

	session := routeTestSession("user-1")
	if err := store.SaveSession(context.Background(), session, time.Hour); err != nil {
		t.Fatalf("save session: %v", err)
	}

	router := gin.New()
	SetupRoutes(router, services)

	request := httptest.NewRequest(http.MethodGet, "/guilds", nil)
	request.Header.Set("Authorization", "Bearer "+session.ID)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, response.Code, response.Body.String())
	}

	var body struct {
		Guilds []struct {
			DiscordGuildID string `json:"discord_guild_id"`
			Name           string `json:"name"`
			CanManageGuild bool   `json:"can_manage_guild"`
			QuackInGuild   bool   `json:"quack_in_guild"`
		} `json:"guilds"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode guild list response: %v", err)
	}

	if len(body.Guilds) != 2 {
		t.Fatalf("expected only manageable guilds, got %+v", body.Guilds)
	}
	if body.Guilds[0].DiscordGuildID != "guild-1" || !body.Guilds[0].CanManageGuild || body.Guilds[0].QuackInGuild {
		t.Fatalf("unexpected first guild: %+v", body.Guilds[0])
	}
	if body.Guilds[1].DiscordGuildID != "guild-2" || !body.Guilds[1].QuackInGuild {
		t.Fatalf("unexpected second guild: %+v", body.Guilds[1])
	}
}

func TestListUserGuildsRouteUnauthenticated(t *testing.T) {
	testutil.SetTestConfig(t)
	gin.SetMode(gin.TestMode)

	router := gin.New()
	SetupRoutes(router, app.New(nil))

	request := httptest.NewRequest(http.MethodGet, "/guilds", nil)
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, response.Code)
	}
}

func TestTemplateRoutesReadAndWritePermissions(t *testing.T) {
	router, sessionID := newTemplateRouteHarness(t, uint64(discordgo.PermissionModerateMembers))

	listRequest := httptest.NewRequest(http.MethodGet, "/guilds/guild-1/templates", nil)
	listRequest.Header.Set("Authorization", "Bearer "+sessionID)
	listResponse := httptest.NewRecorder()
	router.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("expected read status %d, got %d body=%s", http.StatusOK, listResponse.Code, listResponse.Body.String())
	}

	createRequest := httptest.NewRequest(http.MethodPost, "/guilds/guild-1/templates", bytes.NewBufferString(templateRoutePayload("spam")))
	createRequest.Header.Set("Authorization", "Bearer "+sessionID)
	createRequest.Header.Set("Content-Type", "application/json")
	createResponse := httptest.NewRecorder()
	router.ServeHTTP(createResponse, createRequest)
	if createResponse.Code != http.StatusForbidden {
		t.Fatalf("expected write status %d, got %d body=%s", http.StatusForbidden, createResponse.Code, createResponse.Body.String())
	}

	patchRequest := httptest.NewRequest(http.MethodPatch, "/guilds/guild-1/templates/template-1", bytes.NewBufferString(templateRoutePayload("spam")))
	patchRequest.Header.Set("Authorization", "Bearer "+sessionID)
	patchRequest.Header.Set("Content-Type", "application/json")
	patchResponse := httptest.NewRecorder()
	router.ServeHTTP(patchResponse, patchRequest)
	if patchResponse.Code != http.StatusForbidden {
		t.Fatalf("expected patch status %d, got %d body=%s", http.StatusForbidden, patchResponse.Code, patchResponse.Body.String())
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, "/guilds/guild-1/templates/template-1", nil)
	deleteRequest.Header.Set("Authorization", "Bearer "+sessionID)
	deleteResponse := httptest.NewRecorder()
	router.ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusForbidden {
		t.Fatalf("expected delete status %d, got %d body=%s", http.StatusForbidden, deleteResponse.Code, deleteResponse.Body.String())
	}
}

func TestTemplateRoutesCreateUpdateDelete(t *testing.T) {
	router, sessionID := newTemplateRouteHarness(t, uint64(discordgo.PermissionManageGuild))

	createRequest := httptest.NewRequest(http.MethodPost, "/guilds/guild-1/templates", bytes.NewBufferString(templateRoutePayload("spam")))
	createRequest.Header.Set("Authorization", "Bearer "+sessionID)
	createRequest.Header.Set("Content-Type", "application/json")
	createResponse := httptest.NewRecorder()
	router.ServeHTTP(createResponse, createRequest)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("expected create status %d, got %d body=%s", http.StatusCreated, createResponse.Code, createResponse.Body.String())
	}

	var createBody struct {
		Template struct {
			ID      string `json:"id"`
			Slug    string `json:"slug"`
			Version uint   `json:"version"`
		} `json:"template"`
	}
	if err := json.Unmarshal(createResponse.Body.Bytes(), &createBody); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if createBody.Template.ID == "" || createBody.Template.Slug != "spam" {
		t.Fatalf("unexpected create body: %+v", createBody.Template)
	}

	updateRequest := httptest.NewRequest(http.MethodPatch, "/guilds/guild-1/templates/"+createBody.Template.ID, bytes.NewBufferString(templateRoutePayload("spam-updated")))
	updateRequest.Header.Set("Authorization", "Bearer "+sessionID)
	updateRequest.Header.Set("Content-Type", "application/json")
	updateResponse := httptest.NewRecorder()
	router.ServeHTTP(updateResponse, updateRequest)
	if updateResponse.Code != http.StatusOK {
		t.Fatalf("expected update status %d, got %d body=%s", http.StatusOK, updateResponse.Code, updateResponse.Body.String())
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, "/guilds/guild-1/templates/"+createBody.Template.ID, nil)
	deleteRequest.Header.Set("Authorization", "Bearer "+sessionID)
	deleteResponse := httptest.NewRecorder()
	router.ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusOK {
		t.Fatalf("expected delete status %d, got %d body=%s", http.StatusOK, deleteResponse.Code, deleteResponse.Body.String())
	}
}

func TestCaseRouteRequiresAuth(t *testing.T) {
	testutil.SetTestConfig(t)
	gin.SetMode(gin.TestMode)

	router := gin.New()
	SetupRoutes(router, app.New(nil))

	request := httptest.NewRequest(http.MethodPost, "/guilds/guild-1/cases", bytes.NewBufferString(`{}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, response.Code)
	}
}

func TestCaseRouteCreate(t *testing.T) {
	router, sessionID, templateID := newCaseRouteHarness(t, uint64(discordgo.PermissionModerateMembers))

	request := httptest.NewRequest(http.MethodPost, "/guilds/guild-1/cases", bytes.NewBufferString(caseRoutePayload(templateID, "target-1")))
	request.Header.Set("Authorization", "Bearer "+sessionID)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusCreated, response.Code, response.Body.String())
	}

	var body struct {
		Case struct {
			ID                     string `json:"id"`
			CaseNumber             uint64 `json:"case_number"`
			TargetDiscordUserID    string `json:"target_discord_user_id"`
			ModeratorDiscordUserID string `json:"moderator_discord_user_id"`
			Actions                []struct {
				ID       string `json:"id"`
				Position int    `json:"position"`
				Status   string `json:"status"`
			} `json:"actions"`
		} `json:"case"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode case response: %v", err)
	}
	if body.Case.ID == "" || body.Case.CaseNumber != 1 || body.Case.TargetDiscordUserID != "target-1" {
		t.Fatalf("unexpected case response: %+v", body.Case)
	}
	if len(body.Case.Actions) != 1 || body.Case.Actions[0].Status != string(structs.ActionExecutionPending) {
		t.Fatalf("unexpected actions: %+v", body.Case.Actions)
	}
}

func TestCaseRouteErrors(t *testing.T) {
	router, sessionID, templateID := newCaseRouteHarness(t, uint64(discordgo.PermissionModerateMembers))

	tests := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{name: "invalid payload", body: `{`, wantStatus: http.StatusBadRequest},
		{name: "missing template", body: caseRoutePayload("missing-template", "target-1"), wantStatus: http.StatusNotFound},
		{name: "validation", body: caseRoutePayload(templateID, ""), wantStatus: http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/guilds/guild-1/cases", bytes.NewBufferString(tt.body))
			request.Header.Set("Authorization", "Bearer "+sessionID)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()

			router.ServeHTTP(response, request)

			if response.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d body=%s", tt.wantStatus, response.Code, response.Body.String())
			}
		})
	}
}

func TestCaseRoutePermissionDenied(t *testing.T) {
	router, sessionID, templateID := newCaseRouteHarness(t, 0)

	request := httptest.NewRequest(http.MethodPost, "/guilds/guild-1/cases", bytes.NewBufferString(caseRoutePayload(templateID, "target-1")))
	request.Header.Set("Authorization", "Bearer "+sessionID)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusForbidden, response.Code, response.Body.String())
	}
}

type routeFakeDiscordClient struct {
	userGuilds []app.DiscordUserGuild
	botGuilds  []app.DiscordBotGuild
	botGuild   *app.DiscordBotGuild
}

func (f routeFakeDiscordClient) UserGuilds(ctx context.Context, accessToken string) ([]app.DiscordUserGuild, error) {
	return f.userGuilds, nil
}

func (f routeFakeDiscordClient) BotGuilds(ctx context.Context) ([]app.DiscordBotGuild, error) {
	return f.botGuilds, nil
}

func (f routeFakeDiscordClient) BotGuild(ctx context.Context, discordGuildID string) (*app.DiscordBotGuild, error) {
	return f.botGuild, nil
}

func routeTestSession(discordUserID string) *structs.AuthSession {
	now := time.Now().UTC()
	return &structs.AuthSession{
		ID:               "session-1",
		DiscordUserID:    discordUserID,
		Username:         "user",
		GlobalName:       "User",
		AccessToken:      "token",
		SessionExpiresAt: now.Add(time.Hour),
		CreatedAt:        now,
		LastSeenAt:       now,
	}
}

func newTemplateRouteHarness(t *testing.T, permissionBits uint64) (*gin.Engine, string) {
	t.Helper()

	testutil.SetTestConfig(t)
	gin.SetMode(gin.TestMode)

	store := testutil.NewSQLiteRedisStore(t)
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate schema: %v", err)
	}

	services := app.NewWithDiscordClient(store, routeFakeDiscordClient{
		userGuilds: []app.DiscordUserGuild{{
			ID:          "guild-1",
			Permissions: permissionBits,
		}},
		botGuild: &app.DiscordBotGuild{ID: "guild-1", Name: "Guild", OwnerID: "owner-1"},
	})

	session := routeTestSession("user-1")
	if err := store.SaveSession(context.Background(), session, time.Hour); err != nil {
		t.Fatalf("save session: %v", err)
	}

	router := gin.New()
	SetupRoutes(router, services)

	return router, session.ID
}

func newCaseRouteHarness(t *testing.T, permissionBits uint64) (*gin.Engine, string, string) {
	t.Helper()

	testutil.SetTestConfig(t)
	gin.SetMode(gin.TestMode)

	store := testutil.NewSQLiteRedisStore(t)
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate schema: %v", err)
	}

	guild, err := store.UpsertGuild(context.Background(), storage.UpsertGuildParams{
		DiscordGuildID:     "guild-1",
		Name:               "Guild",
		OwnerDiscordUserID: "owner-1",
	})
	if err != nil {
		t.Fatalf("upsert guild: %v", err)
	}
	template, err := store.CreateCaseTemplate(context.Background(), storage.CreateCaseTemplateParams{
		Template: structs.CaseTemplate{
			GuildID:                guild.ID,
			Slug:                   "spam",
			Name:                   "Spam",
			Description:            "Spam template",
			ReasonTemplate:         "No spam",
			DefaultSeverity:        structs.CaseSeverityMedium,
			DefaultWeight:          1,
			Enabled:                true,
			CreatedByDiscordUserID: "admin-1",
			UpdatedByDiscordUserID: "admin-1",
		},
		Actions: []structs.CaseTemplateAction{
			{Position: 1, ActionType: structs.ActionRecordWarning, ConfigJSON: `{}`, IdempotencyScope: "case", Enabled: true},
		},
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}

	services := app.NewWithDiscordClient(store, routeFakeDiscordClient{
		userGuilds: []app.DiscordUserGuild{{
			ID:          "guild-1",
			Permissions: permissionBits,
		}},
		botGuild: &app.DiscordBotGuild{ID: "guild-1", Name: "Guild", OwnerID: "owner-1"},
	})

	session := routeTestSession("user-1")
	if err := store.SaveSession(context.Background(), session, time.Hour); err != nil {
		t.Fatalf("save session: %v", err)
	}

	router := gin.New()
	SetupRoutes(router, services)

	return router, session.ID, template.Template.ID
}

func templateRoutePayload(slug string) string {
	return `{
		"slug": "` + slug + `",
		"name": "Spam",
		"description": "Spam template",
		"reason_template": "No spam",
		"default_severity": "medium",
		"default_weight": 1,
		"actions": [
			{
				"action_type": "record_warning",
				"config": {},
				"idempotency_scope": "case",
				"enabled": true
			}
		],
		"escalation_rules": []
	}`
}

func caseRoutePayload(templateID, targetDiscordUserID string) string {
	return `{
		"template_id": "` + templateID + `",
		"target_discord_user_id": "` + targetDiscordUserID + `",
		"metadata": {"source": "test"}
	}`
}
