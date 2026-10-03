package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/httpapi/middleware"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
	storage "github.com/quackdiscord/bot/internal/store"
	"github.com/quackdiscord/bot/internal/testutil"
)

func TestSetupRoutesStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	SetupRoutes(router, quack.New(nil))

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

func TestSetupRoutesMountsCoreModerationRegistrarsInProductionRouter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	SetupRoutes(router, quack.New(nil))

	routes := map[string]bool{}
	for _, route := range router.Routes() {
		routes[route.Method+" "+route.Path] = true
	}
	want := []struct {
		method string
		path   string
		call   string
	}{
		{http.MethodPost, "/guilds/:discordGuildID/templates/:templateID/restore", "/guilds/guild/templates/template/restore"},
		{http.MethodGet, "/guilds/:discordGuildID/templates/:templateID/export", "/guilds/guild/templates/template/export"},
		{http.MethodPost, "/guilds/:discordGuildID/templates/import", "/guilds/guild/templates/import"},
		{http.MethodPost, "/guilds/:discordGuildID/cases/:caseRef/void", "/guilds/guild/cases/1/void"},
		{http.MethodGet, "/guilds/:discordGuildID/action-failures", "/guilds/guild/action-failures"},
		{http.MethodPost, "/guilds/:discordGuildID/action-failures/:executionID/retry", "/guilds/guild/action-failures/execution/retry"},
		{http.MethodPost, "/guilds/:discordGuildID/action-failures/:executionID/dismiss", "/guilds/guild/action-failures/execution/dismiss"},
		{http.MethodPost, "/guilds/:discordGuildID/cases/:caseRef/reversals", "/guilds/guild/cases/1/reversals"},
		{http.MethodGet, "/members/me/guilds/:guildID/cases", "/members/me/guilds/guild/cases"},
		{http.MethodGet, "/members/me/cases/:caseID", "/members/me/cases/case"},
	}
	for _, route := range want {
		if !routes[route.method+" "+route.path] {
			t.Errorf("production router is missing %s %s", route.method, route.path)
			continue
		}
		request := httptest.NewRequest(route.method, route.call, nil)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Errorf("mounted route %s %s bypassed authentication or fell through: status=%d body=%s", route.method, route.call, response.Code, response.Body.String())
		}
	}
}

func TestRequestContextMiddlewareEchoesRequestID(t *testing.T) {
	testutil.SetTestConfig(t)
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.Use(middleware.RequestContext)
	SetupRoutes(router, quack.New(nil))

	request := httptest.NewRequest(http.MethodGet, "/status", nil)
	request.Header.Set("X-Request-ID", "req-test-1")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, response.Code)
	}
	if response.Header().Get("X-Request-ID") != "req-test-1" {
		t.Fatalf("expected request id header to be echoed, got %q", response.Header().Get("X-Request-ID"))
	}
}

func TestOpsStatusRouteRequiresKey(t *testing.T) {
	testutil.SetTestConfig(t)

	gin.SetMode(gin.TestMode)

	store := testutil.NewSQLiteRedisStore(t)
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate schema: %v", err)
	}
	router := gin.New()
	services := quack.New(store)
	services.Config.API.OpsToken = "secret"
	SetupRoutes(router, services)

	denied := httptest.NewRequest(http.MethodGet, "/ops/status", nil)
	deniedResponse := httptest.NewRecorder()
	router.ServeHTTP(deniedResponse, denied)
	if deniedResponse.Code != http.StatusForbidden {
		t.Fatalf("expected denied status %d, got %d body=%s", http.StatusForbidden, deniedResponse.Code, deniedResponse.Body.String())
	}

	allowed := httptest.NewRequest(http.MethodGet, "/ops/status", nil)
	allowed.Header.Set("X-Quack-Ops-Key", "secret")
	allowedResponse := httptest.NewRecorder()
	router.ServeHTTP(allowedResponse, allowed)
	if allowedResponse.Code != http.StatusOK {
		t.Fatalf("expected allowed status %d, got %d body=%s", http.StatusOK, allowedResponse.Code, allowedResponse.Body.String())
	}

	var body struct {
		Scope   string `json:"scope"`
		Actions struct {
			Capabilities []struct {
				ActionType string `json:"action_type"`
				Executable bool   `json:"executable"`
				Status     string `json:"status"`
			} `json:"capabilities"`
		} `json:"actions"`
	}
	if err := json.Unmarshal(allowedResponse.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode ops body: %v", err)
	}
	if body.Scope != "global" || len(body.Actions.Capabilities) != 5 {
		t.Fatalf("unexpected ops body: %+v", body)
	}
	for _, capability := range body.Actions.Capabilities[:3] {
		if !capability.Executable || capability.Status != "implemented" {
			t.Fatalf("expected punitive actions to be executable, got %+v", body.Actions.Capabilities)
		}
	}
	for _, capability := range body.Actions.Capabilities[3:] {
		if !capability.Executable || capability.Status != "staff_confirmed_reversal" {
			t.Fatalf("expected reversals to require staff confirmation, got %+v", body.Actions.Capabilities)
		}
	}
}

func TestOpsStatusDisabledWhenNoKeyConfigured(t *testing.T) {
	testutil.SetTestConfig(t)
	gin.SetMode(gin.TestMode)

	store := testutil.NewSQLiteRedisStore(t)
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate schema: %v", err)
	}
	router := gin.New()
	SetupRoutes(router, quack.New(store))

	request := httptest.NewRequest(http.MethodGet, "/ops/status", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("expected disabled status %d, got %d body=%s", http.StatusNotFound, response.Code, response.Body.String())
	}
}

func TestGuildOpsStatusAllowsAdminOrOpsKey(t *testing.T) {
	modRouter, modSessionID, _ := newCaseRouteHarness(t, uint64(discordgo.PermissionModerateMembers))
	modRequest := httptest.NewRequest(http.MethodGet, "/guilds/guild-1/ops/status", nil)
	modRequest.Header.Set("Authorization", "Bearer "+modSessionID)
	modResponse := httptest.NewRecorder()
	modRouter.ServeHTTP(modResponse, modRequest)
	if modResponse.Code != http.StatusForbidden {
		t.Fatalf("expected moderator status %d, got %d body=%s", http.StatusForbidden, modResponse.Code, modResponse.Body.String())
	}

	adminRouter, adminSessionID, _ := newCaseRouteHarness(t, uint64(discordgo.PermissionAdministrator))
	adminRequest := httptest.NewRequest(http.MethodGet, "/guilds/guild-1/ops/status", nil)
	adminRequest.Header.Set("Authorization", "Bearer "+adminSessionID)
	adminResponse := httptest.NewRecorder()
	adminRouter.ServeHTTP(adminResponse, adminRequest)
	if adminResponse.Code != http.StatusOK {
		t.Fatalf("expected admin status %d, got %d body=%s", http.StatusOK, adminResponse.Code, adminResponse.Body.String())
	}

	testutil.SetTestConfig(t)

	store := testutil.NewSQLiteRedisStore(t)
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate schema: %v", err)
	}
	if _, err := store.UpsertGuild(context.Background(), storage.UpsertGuildParams{DiscordGuildID: "guild-1", Name: "Guild", OwnerDiscordUserID: "owner-1"}); err != nil {
		t.Fatalf("upsert guild: %v", err)
	}
	keyRouter := gin.New()
	services := quack.New(store)
	services.Config.API.OpsToken = "secret"
	SetupRoutes(keyRouter, services)
	keyRequest := httptest.NewRequest(http.MethodGet, "/guilds/guild-1/ops/status", nil)
	keyRequest.Header.Set("X-Quack-Ops-Key", "secret")
	keyResponse := httptest.NewRecorder()
	keyRouter.ServeHTTP(keyResponse, keyRequest)
	if keyResponse.Code != http.StatusOK {
		t.Fatalf("expected ops key status %d, got %d body=%s", http.StatusOK, keyResponse.Code, keyResponse.Body.String())
	}
}

func TestGuildMeRouteAuthenticated(t *testing.T) {
	testutil.SetTestConfig(t)
	gin.SetMode(gin.TestMode)

	store := testutil.NewSQLiteRedisStore(t)
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate schema: %v", err)
	}

	services := quack.NewWithDiscordClient(store, routeFakeDiscordClient{
		userGuilds: []quack.DiscordUserGuild{{
			ID:          "guild-1",
			Permissions: uint64(discordgo.PermissionModerateMembers),
		}},
		botGuild: &quack.DiscordBotGuild{ID: "guild-1", Name: "Guild", Icon: "icon", OwnerID: "owner-1"},
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
			IsAdmin        bool   `json:"is_admin"`
			IsModerator    bool   `json:"is_moderator"`
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
	if body.Staff.IsAdmin || !body.Staff.IsModerator {
		t.Fatalf("unexpected staff role payload: %+v", body.Staff)
	}
	if body.Permissions[string(model.PermissionActionCaseCreate)] != true {
		t.Fatalf("expected case.create permission")
	}
	if body.Permissions[string(model.PermissionActionCaseTemplateWrite)] != false {
		t.Fatalf("did not expect case_template.write permission")
	}
}

func TestGuildMeRouteUnauthenticated(t *testing.T) {
	testutil.SetTestConfig(t)
	gin.SetMode(gin.TestMode)

	router := gin.New()
	SetupRoutes(router, quack.New(nil))

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

	services := quack.NewWithDiscordClient(store, routeFakeDiscordClient{
		userGuilds: []quack.DiscordUserGuild{
			{ID: "guild-1", Name: "Guild One", Owner: true},
			{ID: "guild-2", Name: "Guild Two", Permissions: uint64(discordgo.PermissionManageGuild)},
			{ID: "guild-3", Name: "Guild Three", Permissions: uint64(discordgo.PermissionSendMessages)},
			{ID: "guild-4", Name: "Guild Four", Permissions: uint64(discordgo.PermissionModerateMembers)},
		},
		botGuilds: []quack.DiscordBotGuild{{ID: "guild-2", Name: "Guild Two"}},
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
			CanModerate    bool   `json:"can_moderate"`
			QuackInGuild   bool   `json:"quack_in_guild"`
		} `json:"guilds"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode guild list response: %v", err)
	}

	if len(body.Guilds) != 3 {
		t.Fatalf("expected current Quack-capable guilds, got %+v", body.Guilds)
	}
	if body.Guilds[0].DiscordGuildID != "guild-1" || !body.Guilds[0].CanManageGuild || body.Guilds[0].QuackInGuild {
		t.Fatalf("unexpected first guild: %+v", body.Guilds[0])
	}
	if body.Guilds[1].DiscordGuildID != "guild-2" || !body.Guilds[1].QuackInGuild {
		t.Fatalf("unexpected second guild: %+v", body.Guilds[1])
	}
	if body.Guilds[2].DiscordGuildID != "guild-4" || body.Guilds[2].CanManageGuild || !body.Guilds[2].CanModerate {
		t.Fatalf("expected Moderate Members guild entry, got %+v", body.Guilds[2])
	}
}

func TestListUserGuildsRouteUnauthenticated(t *testing.T) {
	testutil.SetTestConfig(t)
	gin.SetMode(gin.TestMode)

	router := gin.New()
	SetupRoutes(router, quack.New(nil))

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

func TestTemplateRoutesRejectRetiredProductFields(t *testing.T) {
	router, sessionID := newTemplateRouteHarness(t, uint64(discordgo.PermissionManageGuild))
	payload := strings.Replace(templateRoutePayload("retired-field"), `"levels": [`, `"enabled": true, "levels": [`, 1)
	request := httptest.NewRequest(http.MethodPost, "/guilds/guild-1/templates", bytes.NewBufferString(payload))
	request.Header.Set("Authorization", "Bearer "+sessionID)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected retired field rejection status %d, got %d body=%s", http.StatusBadRequest, response.Code, response.Body.String())
	}
}

func TestGuildSettingsRoutesReadWriteAcknowledgeAndAuditDenied(t *testing.T) {
	managerRouter, managerSessionID, managerStore := newTemplateRouteHarnessWithStore(t, uint64(discordgo.PermissionManageGuild))
	patch := httptest.NewRequest(http.MethodPatch, "/guilds/guild-1/settings", bytes.NewBufferString(`{
		"audit_mirror_channel_discord_id":"",
		"notification_introduction":"Welcome",
		"notification_footer":"Footer",
		"tickets_enabled":true,
		"general_logging_enabled":false,
		"honeypot_enabled":true
	}`))
	patch.Header.Set("Authorization", "Bearer "+managerSessionID)
	patch.Header.Set("Content-Type", "application/json")
	patchResponse := httptest.NewRecorder()
	managerRouter.ServeHTTP(patchResponse, patch)
	if patchResponse.Code != http.StatusOK {
		t.Fatalf("expected settings patch status %d, got %d body=%s", http.StatusOK, patchResponse.Code, patchResponse.Body.String())
	}
	malformed := httptest.NewRequest(http.MethodPatch, "/guilds/guild-1/settings", bytes.NewBufferString(`{"unknown_setting":true}`))
	malformed.Header.Set("Authorization", "Bearer "+managerSessionID)
	malformed.Header.Set("Content-Type", "application/json")
	malformedResponse := httptest.NewRecorder()
	managerRouter.ServeHTTP(malformedResponse, malformed)
	if malformedResponse.Code != http.StatusBadRequest {
		t.Fatalf("expected malformed settings status %d, got %d body=%s", http.StatusBadRequest, malformedResponse.Code, malformedResponse.Body.String())
	}

	get := httptest.NewRequest(http.MethodGet, "/guilds/guild-1/settings", nil)
	get.Header.Set("Authorization", "Bearer "+managerSessionID)
	getResponse := httptest.NewRecorder()
	managerRouter.ServeHTTP(getResponse, get)
	if getResponse.Code != http.StatusOK {
		t.Fatalf("expected settings get status %d, got %d body=%s", http.StatusOK, getResponse.Code, getResponse.Body.String())
	}
	var body struct {
		Settings quack.GuildSettingsResponse `json:"settings"`
	}
	if err := json.Unmarshal(getResponse.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode settings response: %v", err)
	}
	if body.Settings.AuditMirrorChannelDiscordID != "" || !body.Settings.TicketsEnabled || !body.Settings.HoneypotEnabled || !body.Settings.StarterPolicyReviewRequired {
		t.Fatalf("unexpected settings response: %+v", body.Settings)
	}

	ack := httptest.NewRequest(http.MethodPost, "/guilds/guild-1/settings/starter-policy-notice/acknowledge", nil)
	ack.Header.Set("Authorization", "Bearer "+managerSessionID)
	ackResponse := httptest.NewRecorder()
	managerRouter.ServeHTTP(ackResponse, ack)
	if ackResponse.Code != http.StatusOK || strings.Contains(ackResponse.Body.String(), `"starter_policy_review_required":true`) {
		t.Fatalf("starter notice acknowledgement failed: status=%d body=%s", ackResponse.Code, ackResponse.Body.String())
	}

	managerGuild, err := managerStore.GetGuildByDiscordID(context.Background(), "guild-1")
	if err != nil || managerGuild == nil {
		t.Fatalf("load manager guild: guild=%+v err=%v", managerGuild, err)
	}
	managerAudits, err := managerStore.ListAuditLogEntries(context.Background(), managerGuild.ID)
	if err != nil {
		t.Fatalf("list malformed settings audit: %v", err)
	}
	foundFailure := false
	for _, audit := range managerAudits {
		if audit.Action == "guild_settings.update" && audit.Result == model.AuditResultFailure {
			foundFailure = true
		}
	}
	if !foundFailure {
		t.Fatalf("missing malformed payload failure audit: %+v", managerAudits)
	}
	starterSettings, err := managerStore.GetGuildSettings(context.Background(), managerGuild.ID)
	if err != nil || starterSettings.StarterPolicyTemplateID == "" {
		t.Fatalf("missing starter settings after API flow: settings=%+v err=%v", starterSettings, err)
	}
	starter, err := managerStore.GetCaseTemplateExpanded(context.Background(), managerGuild.ID, starterSettings.StarterPolicyTemplateID)
	if err != nil || starter == nil || starter.Template.ArchivedAt != nil {
		t.Fatalf("acknowledgement made starter inactive: starter=%+v err=%v", starter, err)
	}

	moderatorRouter, moderatorSessionID, moderatorStore := newTemplateRouteHarnessWithStore(t, uint64(discordgo.PermissionModerateMembers))
	denied := httptest.NewRequest(http.MethodPatch, "/guilds/guild-1/settings", bytes.NewBufferString(`{"unknown_setting":true}`))
	denied.Header.Set("Authorization", "Bearer "+moderatorSessionID)
	denied.Header.Set("Content-Type", "application/json")
	deniedResponse := httptest.NewRecorder()
	moderatorRouter.ServeHTTP(deniedResponse, denied)
	if deniedResponse.Code != http.StatusForbidden {
		t.Fatalf("expected denied settings status %d, got %d body=%s", http.StatusForbidden, deniedResponse.Code, deniedResponse.Body.String())
	}
	moderatorGuild, _ := moderatorStore.GetGuildByDiscordID(context.Background(), "guild-1")
	audits, err := moderatorStore.ListAuditLogEntries(context.Background(), moderatorGuild.ID)
	if err != nil {
		t.Fatalf("list denied settings audit: %v", err)
	}
	foundDenied := false
	for _, audit := range audits {
		if audit.Action == "authorization.denied" && audit.ResourceID == string(model.PermissionActionGuildSettingsWrite) && audit.Result == model.AuditResultDenied {
			foundDenied = true
		}
	}
	if !foundDenied {
		t.Fatalf("missing denied settings audit: %+v", audits)
	}
}

func TestTemplateRouteRejectsQuarantinedLegacyPolicyExplicitly(t *testing.T) {
	router, sessionID, repositories := newTemplateRouteHarnessWithStore(t, uint64(discordgo.PermissionAdministrator))
	// Recreate a pre-0410 quarantined fixture. The final live schema rejects
	// these shapes at the database boundary, while upgraded installations may
	// still need the compatibility response before operator adjudication.
	for _, index := range []string{"uq_v5_template_default_level", "uq_v5_level_enforcement_action"} {
		if err := repositories.DB().Exec("DROP INDEX " + index).Error; err != nil {
			t.Fatalf("drop final constraint %s for compatibility fixture: %v", index, err)
		}
	}
	guild, err := repositories.UpsertGuild(context.Background(), storage.UpsertGuildParams{
		DiscordGuildID:     "guild-1",
		Name:               "Guild",
		OwnerDiscordUserID: "owner-1",
	})
	if err != nil {
		t.Fatalf("upsert compatibility guild: %v", err)
	}
	created, err := repositories.CreateCaseTemplate(context.Background(), storage.CreateCaseTemplateParams{
		Template: model.CaseTemplate{
			GuildID:                guild.ID,
			Slug:                   "legacy-policy",
			Name:                   "Legacy policy",
			ReasonTemplate:         "Preserved legacy policy",
			CreatedByDiscordUserID: "admin-1",
			UpdatedByDiscordUserID: "admin-1",
		},
		Levels: []storage.ExpandedCaseTemplateLevel{
			{
				Level: model.CaseTemplateLevel{Position: 1, Name: "Legacy default one", IsDefault: true},
				Actions: []model.CaseTemplateLevelAction{
					{ActionType: model.ActionTimeoutUser, ConfigJSON: `{"duration_seconds":3600}`},
				},
			},
			{Level: model.CaseTemplateLevel{Position: 2, Name: "Legacy default two", IsDefault: true}},
		},
	})
	if err != nil {
		t.Fatalf("create preserved legacy policy: %v", err)
	}
	now := time.Now().UTC()
	var firstLevel storage.CaseTemplateLevelRecord
	if err := repositories.DB().Where("template_id = ? AND position = ?", created.Template.ID, 1).First(&firstLevel).Error; err != nil {
		t.Fatalf("load first legacy level: %v", err)
	}
	secondAction := storage.CaseTemplateLevelActionRecord{
		ULIDModelRecord:  storage.ULIDModelRecord{ID: "route-compat-action0000000", CreatedAt: now, UpdatedAt: now},
		LevelID:          firstLevel.ID,
		Position:         2,
		ActionType:       model.ActionKickUser,
		ConfigJSON:       `{}`,
		IdempotencyScope: "case",
		Enabled:          true,
	}
	if err := repositories.DB().Select("*").Create(&secondAction).Error; err != nil {
		t.Fatalf("create preserved second action: %v", err)
	}
	if err := repositories.DB().Model(&storage.CaseTemplateRecord{}).Where("id = ?", created.Template.ID).UpdateColumn("archived_at", now).Error; err != nil {
		t.Fatalf("archive quarantined template: %v", err)
	}
	reason := "level has multiple actions; template does not have exactly one default level"
	if err := repositories.DB().Exec(
		"INSERT INTO quack_v5_0002_template_compatibility (template_id, previous_archived_at, previous_deleted_at, reason, recorded_at) VALUES (?, ?, ?, ?, ?)",
		created.Template.ID, nil, nil, reason, now,
	).Error; err != nil {
		t.Fatalf("record compatibility state: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/guilds/guild-1/templates/"+created.Template.ID, nil)
	request.Header.Set("Authorization", "Bearer "+sessionID)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("expected compatibility conflict %d, got %d body=%s", http.StatusConflict, response.Code, response.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode compatibility response: %v", err)
	}
	if body["error"] != quack.ErrTemplateCompatibilityReviewRequired.Error() || body["template_id"] != created.Template.ID || body["compatibility_reason"] != reason {
		t.Fatalf("unexpected compatibility response: %+v", body)
	}
	if _, exists := body["template"]; exists {
		t.Fatalf("compatibility response exposed invalid template policy: %+v", body)
	}
	if _, exists := body["levels"]; exists {
		t.Fatalf("compatibility response exposed invalid levels: %+v", body)
	}
}

func TestCaseRouteRequiresAuth(t *testing.T) {
	testutil.SetTestConfig(t)
	gin.SetMode(gin.TestMode)

	router := gin.New()
	SetupRoutes(router, quack.New(nil))

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
			Reason                 string `json:"reason"`
			Validity               string `json:"validity"`
			Source                 string `json:"source"`
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
	if body.Case.ID == "" || body.Case.CaseNumber != 1 || body.Case.TargetDiscordUserID != "target-1" || body.Case.Reason != "No spam" || body.Case.Validity != "valid" || body.Case.Source != "dashboard" {
		t.Fatalf("unexpected case response: %+v", body.Case)
	}
	if len(body.Case.Actions) != 0 {
		t.Fatalf("unexpected actions: %+v", body.Case.Actions)
	}
	var raw struct {
		Case map[string]json.RawMessage `json:"case"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw case response: %v", err)
	}
	for _, retired := range []string{"severity", "weight", "status", "reason_override"} {
		if _, exists := raw.Case[retired]; exists {
			t.Fatalf("case response exposed retired field %q: %s", retired, response.Body.String())
		}
	}
}

func TestCaseRouteRejectsReasonOverride(t *testing.T) {
	router, sessionID, templateID := newCaseRouteHarness(t, uint64(discordgo.PermissionModerateMembers))
	payload := `{"template_id":"` + templateID + `","target_discord_user_id":"target-1","reason_override":"invented"}`
	request := httptest.NewRequest(http.MethodPost, "/guilds/guild-1/cases", bytes.NewBufferString(payload))
	request.Header.Set("Authorization", "Bearer "+sessionID)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected retired reason override rejection %d, got %d body=%s", http.StatusBadRequest, response.Code, response.Body.String())
	}
}

func TestCaseReadRoutes(t *testing.T) {
	router, sessionID, templateID := newCaseRouteHarness(t, uint64(discordgo.PermissionModerateMembers))

	createRequest := httptest.NewRequest(http.MethodPost, "/guilds/guild-1/cases", bytes.NewBufferString(caseRoutePayload(templateID, "target-1")))
	createRequest.Header.Set("Authorization", "Bearer "+sessionID)
	createRequest.Header.Set("Content-Type", "application/json")
	createResponse := httptest.NewRecorder()
	router.ServeHTTP(createResponse, createRequest)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("expected create status %d, got %d body=%s", http.StatusCreated, createResponse.Code, createResponse.Body.String())
	}

	listRequest := httptest.NewRequest(http.MethodGet, "/guilds/guild-1/cases?limit=10&target_discord_user_id=target-1", nil)
	listRequest.Header.Set("Authorization", "Bearer "+sessionID)
	listResponse := httptest.NewRecorder()
	router.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("expected list status %d, got %d body=%s", http.StatusOK, listResponse.Code, listResponse.Body.String())
	}

	var listBody struct {
		Total int `json:"total"`
		Cases []struct {
			ID            string `json:"id"`
			SelectedLevel *struct {
				ID string `json:"id"`
			} `json:"selected_level"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(listResponse.Body.Bytes(), &listBody); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if listBody.Total != 1 || len(listBody.Cases) != 1 || listBody.Cases[0].SelectedLevel == nil {
		t.Fatalf("unexpected case list response: %+v", listBody)
	}

	detailRequest := httptest.NewRequest(http.MethodGet, "/guilds/guild-1/cases/1", nil)
	detailRequest.Header.Set("Authorization", "Bearer "+sessionID)
	detailResponse := httptest.NewRecorder()
	router.ServeHTTP(detailResponse, detailRequest)
	if detailResponse.Code != http.StatusOK {
		t.Fatalf("expected detail status %d, got %d body=%s", http.StatusOK, detailResponse.Code, detailResponse.Body.String())
	}

	var detailBody struct {
		Case struct {
			ID     string `json:"id"`
			Events []struct {
				EventType string `json:"event_type"`
			} `json:"events"`
			Actions []struct {
				ID       string `json:"id"`
				Attempts []struct {
					ID string `json:"id"`
				} `json:"attempts"`
			} `json:"actions"`
		} `json:"case"`
	}
	if err := json.Unmarshal(detailResponse.Body.Bytes(), &detailBody); err != nil {
		t.Fatalf("decode detail response: %v", err)
	}
	if detailBody.Case.ID == "" || len(detailBody.Case.Events) != 1 {
		t.Fatalf("unexpected case detail response: %+v", detailBody.Case)
	}

	userRequest := httptest.NewRequest(http.MethodGet, "/guilds/guild-1/users/target-1/cases?limit=10", nil)
	userRequest.Header.Set("Authorization", "Bearer "+sessionID)
	userResponse := httptest.NewRecorder()
	router.ServeHTTP(userResponse, userRequest)
	if userResponse.Code != http.StatusOK {
		t.Fatalf("expected user history status %d, got %d body=%s", http.StatusOK, userResponse.Code, userResponse.Body.String())
	}

	missingRequest := httptest.NewRequest(http.MethodGet, "/guilds/guild-1/cases/missing", nil)
	missingRequest.Header.Set("Authorization", "Bearer "+sessionID)
	missingResponse := httptest.NewRecorder()
	router.ServeHTTP(missingResponse, missingRequest)
	if missingResponse.Code != http.StatusNotFound {
		t.Fatalf("expected missing detail status %d, got %d body=%s", http.StatusNotFound, missingResponse.Code, missingResponse.Body.String())
	}

	invalidRequest := httptest.NewRequest(http.MethodGet, "/guilds/guild-1/cases?limit=0", nil)
	invalidRequest.Header.Set("Authorization", "Bearer "+sessionID)
	invalidResponse := httptest.NewRecorder()
	router.ServeHTTP(invalidResponse, invalidRequest)
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid query status %d, got %d body=%s", http.StatusBadRequest, invalidResponse.Code, invalidResponse.Body.String())
	}
}

func TestAuditLogRoutePermissionsAndFilters(t *testing.T) {
	modRouter, modSessionID, _ := newCaseRouteHarness(t, uint64(discordgo.PermissionModerateMembers))
	modRequest := httptest.NewRequest(http.MethodGet, "/guilds/guild-1/audit-log", nil)
	modRequest.Header.Set("Authorization", "Bearer "+modSessionID)
	modResponse := httptest.NewRecorder()
	modRouter.ServeHTTP(modResponse, modRequest)
	if modResponse.Code != http.StatusOK {
		t.Fatalf("expected moderator audit status %d, got %d body=%s", http.StatusOK, modResponse.Code, modResponse.Body.String())
	}

	adminRouter, adminSessionID, templateID := newCaseRouteHarness(t, uint64(discordgo.PermissionAdministrator))
	createRequest := httptest.NewRequest(http.MethodPost, "/guilds/guild-1/cases", bytes.NewBufferString(caseRoutePayload(templateID, "target-1")))
	createRequest.Header.Set("Authorization", "Bearer "+adminSessionID)
	createRequest.Header.Set("Content-Type", "application/json")
	createResponse := httptest.NewRecorder()
	adminRouter.ServeHTTP(createResponse, createRequest)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("expected create status %d, got %d body=%s", http.StatusCreated, createResponse.Code, createResponse.Body.String())
	}

	auditRequest := httptest.NewRequest(http.MethodGet, "/guilds/guild-1/audit-log?action=case.create&result=success", nil)
	auditRequest.Header.Set("Authorization", "Bearer "+adminSessionID)
	auditResponse := httptest.NewRecorder()
	adminRouter.ServeHTTP(auditResponse, auditRequest)
	if auditResponse.Code != http.StatusOK {
		t.Fatalf("expected audit status %d, got %d body=%s", http.StatusOK, auditResponse.Code, auditResponse.Body.String())
	}

	var auditBody struct {
		Total   int `json:"total"`
		Entries []struct {
			Action string `json:"action"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(auditResponse.Body.Bytes(), &auditBody); err != nil {
		t.Fatalf("decode audit response: %v", err)
	}
	if auditBody.Total != 1 || len(auditBody.Entries) != 1 || auditBody.Entries[0].Action != "case.create" {
		t.Fatalf("unexpected audit response: %+v", auditBody)
	}

	invalidRequest := httptest.NewRequest(http.MethodGet, "/guilds/guild-1/audit-log?result=partial", nil)
	invalidRequest.Header.Set("Authorization", "Bearer "+adminSessionID)
	invalidResponse := httptest.NewRecorder()
	adminRouter.ServeHTTP(invalidResponse, invalidRequest)
	if invalidResponse.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid audit status %d, got %d body=%s", http.StatusBadRequest, invalidResponse.Code, invalidResponse.Body.String())
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
	userGuilds []quack.DiscordUserGuild
	botGuilds  []quack.DiscordBotGuild
	botGuild   *quack.DiscordBotGuild
}

func (f routeFakeDiscordClient) UserGuilds(ctx context.Context, accessToken string) ([]quack.DiscordUserGuild, error) {
	return f.userGuilds, nil
}

func (f routeFakeDiscordClient) BotGuilds(ctx context.Context) ([]quack.DiscordBotGuild, error) {
	return f.botGuilds, nil
}

func (f routeFakeDiscordClient) BotGuild(ctx context.Context, discordGuildID string) (*quack.DiscordBotGuild, error) {
	return f.botGuild, nil
}

func (f routeFakeDiscordClient) GuildAuthorization(ctx context.Context, guildID, actorID, targetID string) (*quack.DiscordGuildAuthorization, error) {
	if f.botGuild == nil {
		return nil, quack.ErrBotNotInGuild
	}
	actor := quack.DiscordMemberAuthorization{DiscordUserID: actorID, Present: true, TopRolePosition: 10}
	for _, guild := range f.userGuilds {
		if guild.ID == guildID {
			actor.PermissionBits = guild.Permissions
			break
		}
	}
	snapshot := &quack.DiscordGuildAuthorization{
		Guild: *f.botGuild, Actor: actor,
		Bot: quack.DiscordMemberAuthorization{DiscordUserID: "quack", Present: true, PermissionBits: ^uint64(0), TopRolePosition: 20, Bot: true},
	}
	if targetID != "" {
		snapshot.Target = &quack.DiscordMemberAuthorization{DiscordUserID: targetID, Present: true, TopRolePosition: 1}
	}
	return snapshot, nil
}

func routeTestSession(discordUserID string) *model.AuthSession {
	now := time.Now().UTC()
	return &model.AuthSession{
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
	router, sessionID, _ := newTemplateRouteHarnessWithStore(t, permissionBits)
	return router, sessionID
}

// newTemplateRouteHarnessWithStore exposes the adapter only for route-level persistence boundary fixtures.
func newTemplateRouteHarnessWithStore(t *testing.T, permissionBits uint64) (*gin.Engine, string, *storage.Store) {
	t.Helper()

	testutil.SetTestConfig(t)
	gin.SetMode(gin.TestMode)

	store := testutil.NewSQLiteRedisStore(t)
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate schema: %v", err)
	}
	if _, err := store.BootstrapGuild(context.Background(), model.BootstrapGuildParams{
		DiscordGuildID: "guild-1", Name: "Guild", OwnerDiscordUserID: "owner-1",
	}); err != nil {
		t.Fatalf("bootstrap route guild: %v", err)
	}

	services := quack.NewWithDiscordClient(store, routeFakeDiscordClient{
		userGuilds: []quack.DiscordUserGuild{{
			ID:          "guild-1",
			Permissions: permissionBits,
		}},
		botGuild: &quack.DiscordBotGuild{ID: "guild-1", Name: "Guild", OwnerID: "owner-1"},
	})

	session := routeTestSession("user-1")
	if err := store.SaveSession(context.Background(), session, time.Hour); err != nil {
		t.Fatalf("save session: %v", err)
	}

	router := gin.New()
	SetupRoutes(router, services)

	return router, session.ID, store
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
		Template: model.CaseTemplate{
			GuildID:                guild.ID,
			Slug:                   "spam",
			Name:                   "Spam",
			Description:            "Spam template",
			ReasonTemplate:         "No spam",
			CreatedByDiscordUserID: "admin-1",
			UpdatedByDiscordUserID: "admin-1",
		},
		Levels: []storage.ExpandedCaseTemplateLevel{
			{
				Level: model.CaseTemplateLevel{Position: 1, Name: "Default", IsDefault: true},
			},
		},
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}

	services := quack.NewWithDiscordClient(store, routeFakeDiscordClient{
		userGuilds: []quack.DiscordUserGuild{{
			ID:          "guild-1",
			Permissions: permissionBits,
		}},
		botGuild: &quack.DiscordBotGuild{ID: "guild-1", Name: "Guild", OwnerID: "owner-1"},
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
		"levels": [
			{
				"name": "Default",
				"position": 1,
				"is_default": true,
					"actions": []
			}
		]
	}`
}

func caseRoutePayload(templateID, targetDiscordUserID string) string {
	return `{
		"template_id": "` + templateID + `",
		"target_discord_user_id": "` + targetDiscordUserID + `",
		"metadata": {"source": "test"}
	}`
}
