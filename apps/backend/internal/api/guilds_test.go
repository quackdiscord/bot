package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/config"
	"github.com/quackdiscord/bot/internal/modules"
	honeypotmodule "github.com/quackdiscord/bot/internal/modules/honeypot"
	logmodule "github.com/quackdiscord/bot/internal/modules/logging"
	ticketmodule "github.com/quackdiscord/bot/internal/modules/tickets"
	"github.com/quackdiscord/bot/internal/quack"
	storage "github.com/quackdiscord/bot/internal/store"
)

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

// TestGuildMeForMembers answers a member who is not staff with no
// permissions, without writing a staff record for them.
func TestGuildMeForMembers(t *testing.T) {
	store := migratedStore(t)
	server := storeServer(t, store, staffGuilds(uint64(discordgo.PermissionSendMessages)), nil, config.Default())
	sessionID := saveSession(t, store, testSession("user-1"))
	response := send(t, server, http.MethodGet, "/guilds/guild-1/me", "", sessionID)
	expectStatus(t, response, http.StatusOK)
	if body := response.Body.String(); !strings.Contains(body, `"case.read":false`) || !strings.Contains(body, `"staff_roles.write":false`) {
		t.Fatalf("member /me = %s", body)
	}
	guild, err := store.GetGuildByDiscordID(context.Background(), "guild-1")
	if err != nil || guild == nil {
		t.Fatal(err)
	}
	if record, err := store.GetStaffMember(context.Background(), guild.ID, "user-1"); err != nil || record != nil {
		t.Fatalf("member got a staff record: %+v, %v", record, err)
	}
}

// TestGuildRoutesRequireConfirmed2FA checks that staff in a guild requiring
// 2FA are refused with mfa_required on every guild route, /me included,
// until sign-in confirms it.
func TestGuildRoutesRequireConfirmed2FA(t *testing.T) {
	store := migratedStore(t)
	guilds := staffGuilds(uint64(discordgo.PermissionAdministrator))
	guilds.botGuild.MFARequired = true
	// Module routes serve members their own resources, so the 2FA
	// requirement only stops staff capabilities there.
	server := newTestServer(t, config.Default(), Deps{
		Services: quack.New(quack.Deps{Store: store, Guilds: guilds, Modules: modules.NewRegistry(store.DB())}),
		Store:    store,
		Redis:    store.Redis(),
		Modules: func(mux *ModuleMux) {
			mux.Handle("GET /mine", modules.Doc{ID: "mine", Summary: "A member's own resource"},
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					writeJSON(w, http.StatusOK, map[string]bool{"staff": quack.StaffFromContext(r.Context()).Can(quack.PermissionActionTicketResolve)})
				}))
		},
	})
	sessionID := saveSession(t, store, testSession("user-1"))

	for _, path := range []string{"/guilds/guild-1/me", "/guilds/guild-1/settings", "/guilds/guild-1/cases",
		"/guilds/guild-1/appeals", "/guilds/guild-1/ops/status"} {
		assertEnvelope(t, send(t, server, http.MethodGet, path, "", sessionID), http.StatusForbidden, codeMFARequired)
	}
	mine := send(t, server, http.MethodGet, "/guilds/guild-1/modules/mine", "", sessionID)
	if mine.Code != http.StatusOK || mine.Body.String() != `{"staff":false}` {
		t.Fatalf("module route for a 2FA-blocked member = %d %s", mine.Code, mine.Body.String())
	}
	if err := store.RecordDiscordUserMFA(context.Background(), "user-1", true, time.Now()); err != nil {
		t.Fatal(err)
	}
	expectStatus(t, send(t, server, http.MethodGet, "/guilds/guild-1/me", "", sessionID), http.StatusOK)
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
	for _, field := range []string{`"can_manage_rules":true`, `"mfa_required":false`} {
		if !strings.Contains(response.Body.String(), field) {
			t.Errorf("guild list lacks %s: %s", field, response.Body.String())
		}
	}
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
	// A module that was never set up cannot be switched on.
	expectStatus(t, send(t, server, http.MethodPatch, "/guilds/guild-1/settings", patch, sessionID), http.StatusBadRequest)
	guild, err := store.GetGuildByDiscordID(context.Background(), "guild-1")
	if err != nil || guild == nil {
		t.Fatalf("load guild: %+v %v", guild, err)
	}
	registry := modules.NewRegistry(store.DB())
	for _, id := range []modules.ID{modules.Tickets, modules.GeneralLogging, modules.Honeypots} {
		if _, err := registry.SetConfiguration(context.Background(), modules.Configuration{GuildID: guild.ID, ModuleID: id}); err != nil {
			t.Fatal(err)
		}
	}
	expectStatus(t, send(t, server, http.MethodPatch, "/guilds/guild-1/settings", patch, sessionID), http.StatusOK)
	expectStatus(t, send(t, server, http.MethodPatch, "/guilds/guild-1/settings", `{"unknown_setting":true}`, sessionID), http.StatusBadRequest)
	// Manage Server cannot change moderator roles, only the owner and
	// Administrators can.
	assertEnvelope(t, send(t, server, http.MethodPatch, "/guilds/guild-1/settings", `{"moderator_role_ids":["123"]}`, sessionID),
		http.StatusForbidden, codeAuthorization)
	expectStatus(t, send(t, server, http.MethodPatch, "/guilds/guild-1/settings", `{"moderator_role_ids":[]}`, sessionID), http.StatusOK)

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
	for _, field := range []string{`"moderator_role_ids":[]`, `"rules_manager_role_ids":[]`} {
		if !strings.Contains(response.Body.String(), field) {
			t.Fatalf("settings lack %s: %s", field, response.Body.String())
		}
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
	// Refusals about the person acting are answered, not audited.
	if hasAudit(audits, "authorization.denied", "", quack.AuditResultDenied) || hasAudit(audits, "guild_settings.update", "", quack.AuditResultDenied) {
		t.Errorf("a refused write was audited: %+v", audits)
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
	_, loggingEnabled, _, err := logmodule.NewService(registry, nil, nil).Settings(ctx, actor)
	if err != nil || loggingEnabled != logging {
		t.Errorf("logging module sees enabled=%v (err %v), want %v", loggingEnabled, err, logging)
	}
	_, trapStatus, err := honeypotmodule.NewService(registry, honeypotmodule.NewStore(store.DB()), nil, nil, nil, nil).Settings(ctx, actor)
	if err != nil || trapStatus.Enabled != honeypot {
		t.Errorf("honeypot module sees enabled=%v (err %v), want %v", trapStatus.Enabled, err, honeypot)
	}
}
