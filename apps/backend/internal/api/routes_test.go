package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/config"
	"github.com/quackdiscord/bot/internal/modules"
	honeypotmodule "github.com/quackdiscord/bot/internal/modules/honeypot"
	logmodule "github.com/quackdiscord/bot/internal/modules/logging"
	ticketmodule "github.com/quackdiscord/bot/internal/modules/tickets"
	"github.com/quackdiscord/bot/internal/quack"
	storage "github.com/quackdiscord/bot/internal/store"
)

func TestGuildAndMemberRoutesRequireAuthentication(t *testing.T) {
	server := newTestServer(t, config.Default(), Deps{})
	routes := []struct{ method, path string }{
		{http.MethodGet, "/guilds"},
		{http.MethodGet, "/guilds/guild/me"},
		{http.MethodPost, "/guilds/guild/cases"},
		{http.MethodPost, "/guilds/guild/templates/template/restore"},
		{http.MethodGet, "/guilds/guild/templates/template/export"},
		{http.MethodPost, "/guilds/guild/templates/import"},
		{http.MethodPost, "/guilds/guild/cases/1/void"},
		{http.MethodGet, "/guilds/guild/action-failures"},
		{http.MethodPost, "/guilds/guild/action-failures/execution/retry"},
		{http.MethodPost, "/guilds/guild/action-failures/execution/dismiss"},
		{http.MethodPost, "/guilds/guild/cases/1/reversals"},
		{http.MethodGet, "/guilds/guild/statistics"},
		{http.MethodGet, "/guilds/guild/appeals"},
		{http.MethodGet, "/guilds/guild/ops/status"},
		{http.MethodGet, "/members/me/guilds/guild/cases"},
		{http.MethodGet, "/members/me/cases/case"},
		{http.MethodPost, "/members/me/cases/case/appeal"},
		{http.MethodGet, "/auth/me"},
	}
	for _, route := range routes {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			response := send(t, server, route.method, route.path, "", "")
			expectStatus(t, response, http.StatusUnauthorized)
		})
	}
}

func TestUnknownRoutesAndMethodsAreNotFound(t *testing.T) {
	server := newTestServer(t, config.Default(), Deps{})
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/nope"},
		{http.MethodDelete, "/livez"},
		{http.MethodPut, "/guilds/guild/cases"},
	} {
		response := send(t, server, route.method, route.path, "", "")
		assertEnvelope(t, response, http.StatusNotFound, codeNotFound)
	}
}

func TestStatusReportsDisconnectedDependencies(t *testing.T) {
	server := newTestServer(t, config.Default(), Deps{})
	response := send(t, server, http.MethodGet, "/status", "", "", requestIDHeader, "req-test-1")
	expectStatus(t, response, http.StatusOK)
	if got := response.Header().Get(requestIDHeader); got != "req-test-1" {
		t.Fatalf("X-Request-ID = %q, want the caller's ID echoed", got)
	}
	var body map[string]map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, dependency := range []string{"discord", "redis", "database"} {
		if body[dependency]["connected"] != false {
			t.Errorf("%s connected = %v, want false", dependency, body[dependency]["connected"])
		}
	}
}

func TestGuildMe(t *testing.T) {
	store := migratedStore(t)
	server := storeServer(t, store, staffGuilds(uint64(discordgo.PermissionModerateMembers)), nil, config.Default())
	sessionID := saveSession(t, store, testSession("user-1"))

	response := send(t, server, http.MethodGet, "/guilds/guild-1/me", "", sessionID)
	expectStatus(t, response, http.StatusOK)
	var body struct {
		Guild struct {
			ID             string `json:"id"`
			DiscordGuildID string `json:"discord_guild_id"`
			Name           string `json:"name"`
		} `json:"guild"`
		Staff struct {
			ID            string `json:"id"`
			DiscordUserID string `json:"discord_user_id"`
			IsAdmin       bool   `json:"is_admin"`
			IsModerator   bool   `json:"is_moderator"`
		} `json:"staff"`
		Permissions map[string]bool `json:"permissions"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Guild.ID == "" || body.Guild.DiscordGuildID != "guild-1" || body.Guild.Name != "Guild" {
		t.Errorf("guild = %+v", body.Guild)
	}
	if body.Staff.ID == "" || body.Staff.DiscordUserID != "user-1" || body.Staff.IsAdmin || !body.Staff.IsModerator {
		t.Errorf("staff = %+v", body.Staff)
	}
	if !body.Permissions[string(quack.PermissionActionCaseCreate)] || body.Permissions[string(quack.PermissionActionCaseTemplateWrite)] {
		t.Errorf("permissions = %v", body.Permissions)
	}
}

func TestListUserGuilds(t *testing.T) {
	store := migratedStore(t)
	guilds := fakeGuilds{
		userGuilds: []quack.DiscordUserGuild{
			{ID: "guild-1", Name: "Guild One", Owner: true},
			{ID: "guild-2", Name: "Guild Two", Permissions: uint64(discordgo.PermissionManageGuild)},
			{ID: "guild-3", Name: "Guild Three", Permissions: uint64(discordgo.PermissionSendMessages)},
			{ID: "guild-4", Name: "Guild Four", Permissions: uint64(discordgo.PermissionModerateMembers)},
		},
		botGuilds: []quack.DiscordBotGuild{{ID: "guild-2", Name: "Guild Two"}},
	}
	server := storeServer(t, store, guilds, nil, config.Default())
	sessionID := saveSession(t, store, testSession("user-1"))

	response := send(t, server, http.MethodGet, "/guilds", "", sessionID)
	expectStatus(t, response, http.StatusOK)
	var body struct {
		Guilds []struct {
			DiscordGuildID string `json:"discord_guild_id"`
			CanManageGuild bool   `json:"can_manage_guild"`
			CanModerate    bool   `json:"can_moderate"`
			QuackInGuild   bool   `json:"quack_in_guild"`
		} `json:"guilds"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Guilds) != 3 {
		t.Fatalf("guilds = %+v, want the three Quack-capable guilds", body.Guilds)
	}
	if g := body.Guilds[0]; g.DiscordGuildID != "guild-1" || !g.CanManageGuild || g.QuackInGuild {
		t.Errorf("first guild = %+v", g)
	}
	if g := body.Guilds[1]; g.DiscordGuildID != "guild-2" || !g.QuackInGuild {
		t.Errorf("second guild = %+v", g)
	}
	if g := body.Guilds[2]; g.DiscordGuildID != "guild-4" || g.CanManageGuild || !g.CanModerate {
		t.Errorf("third guild = %+v", g)
	}
}

func TestTemplateRoutes(t *testing.T) {
	t.Run("moderator can read but not write", func(t *testing.T) {
		server, sessionID, _ := templateHarness(t, uint64(discordgo.PermissionModerateMembers))
		expectStatus(t, send(t, server, http.MethodGet, "/guilds/guild-1/templates", "", sessionID), http.StatusOK)
		expectStatus(t, send(t, server, http.MethodPost, "/guilds/guild-1/templates", templatePayload("spam"), sessionID), http.StatusForbidden)
		expectStatus(t, send(t, server, http.MethodPatch, "/guilds/guild-1/templates/template-1", templatePayload("spam"), sessionID), http.StatusForbidden)
		expectStatus(t, send(t, server, http.MethodDelete, "/guilds/guild-1/templates/template-1", "", sessionID), http.StatusForbidden)
	})
	t.Run("manager can create, update, and archive", func(t *testing.T) {
		server, sessionID, _ := templateHarness(t, uint64(discordgo.PermissionManageGuild))
		created := send(t, server, http.MethodPost, "/guilds/guild-1/templates", templatePayload("spam"), sessionID)
		expectStatus(t, created, http.StatusCreated)
		var body struct {
			Template struct {
				ID   string `json:"id"`
				Slug string `json:"slug"`
			} `json:"template"`
		}
		if err := json.Unmarshal(created.Body.Bytes(), &body); err != nil || body.Template.ID == "" || body.Template.Slug != "spam" {
			t.Fatalf("created = %s (err %v)", created.Body.String(), err)
		}
		path := "/guilds/guild-1/templates/" + body.Template.ID
		expectStatus(t, send(t, server, http.MethodPatch, path, templatePayload("spam-updated"), sessionID), http.StatusOK)
		expectStatus(t, send(t, server, http.MethodDelete, path, "", sessionID), http.StatusOK)
	})
	t.Run("retired fields are rejected", func(t *testing.T) {
		server, sessionID, _ := templateHarness(t, uint64(discordgo.PermissionManageGuild))
		payload := strings.Replace(templatePayload("retired"), `"levels": [`, `"enabled": true, "levels": [`, 1)
		expectStatus(t, send(t, server, http.MethodPost, "/guilds/guild-1/templates", payload, sessionID), http.StatusBadRequest)
	})
}

func TestGuildSettingsRoutes(t *testing.T) {
	server, sessionID, store := templateHarness(t, uint64(discordgo.PermissionManageGuild))
	patch := `{
		"audit_mirror_channel_discord_id": "",
		"notification_introduction": "Welcome",
		"notification_footer": "Footer",
		"tickets_enabled": true,
		"general_logging_enabled": false,
		"honeypot_enabled": true
	}`
	expectStatus(t, send(t, server, http.MethodPatch, "/guilds/guild-1/settings", patch, sessionID), http.StatusOK)
	expectStatus(t, send(t, server, http.MethodPatch, "/guilds/guild-1/settings", `{"unknown_setting":true}`, sessionID), http.StatusBadRequest)

	response := send(t, server, http.MethodGet, "/guilds/guild-1/settings", "", sessionID)
	expectStatus(t, response, http.StatusOK)
	var body struct {
		Settings quack.GuildSettingsResponse `json:"settings"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if s := body.Settings; s.AuditMirrorChannelDiscordID != "" || !s.TicketsEnabled || !s.HoneypotEnabled || !s.StarterPolicyReviewRequired {
		t.Fatalf("settings = %+v", s)
	}
	assertModulesSeeToggles(t, store, "guild-1", true, false, true)
	off := `{"tickets_enabled": false, "honeypot_enabled": false, "general_logging_enabled": true}`
	expectStatus(t, send(t, server, http.MethodPatch, "/guilds/guild-1/settings", off, sessionID), http.StatusOK)
	assertModulesSeeToggles(t, store, "guild-1", false, true, false)

	ack := send(t, server, http.MethodPost, "/guilds/guild-1/settings/starter-policy-notice/acknowledge", "", sessionID)
	expectStatus(t, ack, http.StatusOK)
	if strings.Contains(ack.Body.String(), `"starter_policy_review_required":true`) {
		t.Fatalf("notice still required after acknowledgement: %s", ack.Body.String())
	}

	guild, err := store.GetGuildByDiscordID(context.Background(), "guild-1")
	if err != nil || guild == nil {
		t.Fatalf("load guild: %+v %v", guild, err)
	}
	audits, err := store.ListAuditLogEntries(context.Background(), guild.ID)
	if err != nil {
		t.Fatalf("list audits: %v", err)
	}
	if !hasAudit(audits, "guild_settings.update", "", quack.AuditResultFailure) {
		t.Errorf("malformed payload was not audited as a failure: %+v", audits)
	}
	settings, err := store.GetGuildSettings(context.Background(), guild.ID)
	if err != nil || settings.StarterPolicyTemplateID == "" {
		t.Fatalf("starter settings = %+v, %v", settings, err)
	}
	starter, err := store.GetCaseTemplateExpanded(context.Background(), guild.ID, settings.StarterPolicyTemplateID)
	if err != nil || starter == nil || starter.Template.ArchivedAt != nil {
		t.Fatalf("acknowledgement archived the starter template: %+v %v", starter, err)
	}

	moderator, moderatorSession, moderatorStore := templateHarness(t, uint64(discordgo.PermissionModerateMembers))
	expectStatus(t, send(t, moderator, http.MethodPatch, "/guilds/guild-1/settings", `{"unknown_setting":true}`, moderatorSession), http.StatusForbidden)
	moderatorGuild, _ := moderatorStore.GetGuildByDiscordID(context.Background(), "guild-1")
	audits, err = moderatorStore.ListAuditLogEntries(context.Background(), moderatorGuild.ID)
	if err != nil {
		t.Fatalf("list audits: %v", err)
	}
	if !hasAudit(audits, "authorization.denied", string(quack.PermissionActionGuildSettingsWrite), quack.AuditResultDenied) {
		t.Errorf("denied write was not audited: %+v", audits)
	}
}

func hasAudit(entries []quack.AuditLogEntry, action, resourceID string, result quack.AuditResult) bool {
	for _, entry := range entries {
		if entry.Action == action && entry.Result == result && (resourceID == "" || entry.ResourceID == resourceID) {
			return true
		}
	}
	return false
}

func TestCaseRoutes(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		server, sessionID, templateID := caseHarness(t, uint64(discordgo.PermissionModerateMembers))
		response := send(t, server, http.MethodPost, "/guilds/guild-1/cases", casePayload(templateID, "target-1"), sessionID)
		expectStatus(t, response, http.StatusCreated)
		var body struct {
			Case struct {
				ID                  string            `json:"id"`
				CaseNumber          uint64            `json:"case_number"`
				TargetDiscordUserID string            `json:"target_discord_user_id"`
				Reason              string            `json:"reason"`
				Validity            string            `json:"validity"`
				Source              string            `json:"source"`
				Actions             []json.RawMessage `json:"actions"`
			} `json:"case"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		c := body.Case
		if c.ID == "" || c.CaseNumber != 1 || c.TargetDiscordUserID != "target-1" || c.Reason != "No spam" ||
			c.Validity != "valid" || c.Source != "dashboard" || len(c.Actions) != 0 {
			t.Fatalf("case = %+v", c)
		}
		var raw struct {
			Case map[string]json.RawMessage `json:"case"`
		}
		_ = json.Unmarshal(response.Body.Bytes(), &raw)
		for _, retired := range []string{"severity", "weight", "status", "reason_override"} {
			if _, ok := raw.Case[retired]; ok {
				t.Errorf("case exposes retired field %q", retired)
			}
		}
	})
	t.Run("reason override is rejected", func(t *testing.T) {
		server, sessionID, templateID := caseHarness(t, uint64(discordgo.PermissionModerateMembers))
		payload := `{"template_id":"` + templateID + `","target_discord_user_id":"target-1","reason_override":"invented"}`
		expectStatus(t, send(t, server, http.MethodPost, "/guilds/guild-1/cases", payload, sessionID), http.StatusBadRequest)
	})
	t.Run("errors", func(t *testing.T) {
		server, sessionID, templateID := caseHarness(t, uint64(discordgo.PermissionModerateMembers))
		for _, test := range []struct {
			name, body string
			want       int
		}{
			{"invalid payload", `{`, http.StatusBadRequest},
			{"missing template", casePayload("missing-template", "target-1"), http.StatusNotFound},
			{"validation", casePayload(templateID, ""), http.StatusBadRequest},
		} {
			t.Run(test.name, func(t *testing.T) {
				expectStatus(t, send(t, server, http.MethodPost, "/guilds/guild-1/cases", test.body, sessionID), test.want)
			})
		}
	})
	t.Run("permission denied", func(t *testing.T) {
		server, sessionID, templateID := caseHarness(t, 0)
		expectStatus(t, send(t, server, http.MethodPost, "/guilds/guild-1/cases", casePayload(templateID, "target-1"), sessionID), http.StatusForbidden)
	})
}

func TestCaseReadRoutes(t *testing.T) {
	server, sessionID, templateID := caseHarness(t, uint64(discordgo.PermissionModerateMembers))
	expectStatus(t, send(t, server, http.MethodPost, "/guilds/guild-1/cases", casePayload(templateID, "target-1"), sessionID), http.StatusCreated)

	list := send(t, server, http.MethodGet, "/guilds/guild-1/cases?limit=10&target_discord_user_id=target-1", "", sessionID)
	expectStatus(t, list, http.StatusOK)
	var listBody struct {
		Total int `json:"total"`
		Cases []struct {
			SelectedLevel *struct {
				ID string `json:"id"`
			} `json:"selected_level"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &listBody); err != nil || listBody.Total != 1 ||
		len(listBody.Cases) != 1 || listBody.Cases[0].SelectedLevel == nil {
		t.Fatalf("list = %s (err %v)", list.Body.String(), err)
	}

	detail := send(t, server, http.MethodGet, "/guilds/guild-1/cases/1", "", sessionID)
	expectStatus(t, detail, http.StatusOK)
	var detailBody struct {
		Case struct {
			ID     string            `json:"id"`
			Events []json.RawMessage `json:"events"`
		} `json:"case"`
	}
	if err := json.Unmarshal(detail.Body.Bytes(), &detailBody); err != nil || detailBody.Case.ID == "" || len(detailBody.Case.Events) != 1 {
		t.Fatalf("detail = %s (err %v)", detail.Body.String(), err)
	}

	expectStatus(t, send(t, server, http.MethodGet, "/guilds/guild-1/users/target-1/cases?limit=10", "", sessionID), http.StatusOK)
	expectStatus(t, send(t, server, http.MethodGet, "/guilds/guild-1/cases/missing", "", sessionID), http.StatusNotFound)
	expectStatus(t, send(t, server, http.MethodGet, "/guilds/guild-1/cases?limit=0", "", sessionID), http.StatusBadRequest)
}

func TestAuditLogRoute(t *testing.T) {
	moderator, moderatorSession, _ := caseHarness(t, uint64(discordgo.PermissionModerateMembers))
	expectStatus(t, send(t, moderator, http.MethodGet, "/guilds/guild-1/audit-log", "", moderatorSession), http.StatusOK)

	admin, adminSession, templateID := caseHarness(t, uint64(discordgo.PermissionAdministrator))
	expectStatus(t, send(t, admin, http.MethodPost, "/guilds/guild-1/cases", casePayload(templateID, "target-1"), adminSession), http.StatusCreated)
	response := send(t, admin, http.MethodGet, "/guilds/guild-1/audit-log?action=case.create&result=success", "", adminSession)
	expectStatus(t, response, http.StatusOK)
	var body struct {
		Total   int `json:"total"`
		Entries []struct {
			Action string `json:"action"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Total != 1 ||
		len(body.Entries) != 1 || body.Entries[0].Action != "case.create" {
		t.Fatalf("audit = %s (err %v)", response.Body.String(), err)
	}
	expectStatus(t, send(t, admin, http.MethodGet, "/guilds/guild-1/audit-log?result=partial", "", adminSession), http.StatusBadRequest)
}

// assertEnvelope checks the status and that the body is the error envelope
// with trace IDs.
func assertEnvelope(t *testing.T, response *httptest.ResponseRecorder, status int, code errorCode) {
	t.Helper()
	expectStatus(t, response, status)
	var body errorResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode envelope: %v; body=%s", err, response.Body.String())
	}
	if e := body.Error; e.Code != code || e.Message == "" || e.RequestID == "" || e.CorrelationID == "" {
		t.Fatalf("envelope = %+v, want code %q", e, code)
	}
}

// assertModulesSeeToggles checks the settings API's module switches through
// each module's own service, which reads module_configurations.
func assertModulesSeeToggles(t *testing.T, store *storage.Store, discordGuildID string, tickets, logging, honeypot bool) {
	t.Helper()
	ctx := context.Background()
	guild, err := store.GetGuildByDiscordID(ctx, discordGuildID)
	if err != nil || guild == nil {
		t.Fatalf("load guild: %+v %v", guild, err)
	}
	registry := modules.NewRegistry(store.DB())
	actor := modules.Actor{GuildID: guild.ID, DiscordUserID: "admin", CanManage: true}
	ticketStatus, err := ticketmodule.NewService(registry, ticketmodule.NewStore(store.DB()), nil).Status(ctx, actor)
	if err != nil || ticketStatus.Enabled != tickets {
		t.Errorf("tickets module sees enabled=%v (err %v), want %v", ticketStatus.Enabled, err, tickets)
	}
	_, loggingEnabled, _, err := logmodule.NewService(registry, nil, nil, nil).Settings(ctx, actor)
	if err != nil || loggingEnabled != logging {
		t.Errorf("logging module sees enabled=%v (err %v), want %v", loggingEnabled, err, logging)
	}
	_, trapStatus, err := honeypotmodule.NewService(registry, honeypotmodule.NewStore(store.DB()), nil, nil, nil, nil).Settings(ctx, actor)
	if err != nil || trapStatus.Enabled != honeypot {
		t.Errorf("honeypot module sees enabled=%v (err %v), want %v", trapStatus.Enabled, err, honeypot)
	}
}
