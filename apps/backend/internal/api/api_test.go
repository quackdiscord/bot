package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/quackdiscord/bot/internal/config"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/quack"
	storage "github.com/quackdiscord/bot/internal/store"
	"github.com/quackdiscord/bot/internal/testutil"
	"github.com/redis/go-redis/v9"
)

// newRedis starts an in-memory Redis for one test.
func newRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return server, client
}

// downStore is storage whose dependencies are unreachable. Methods the test
// does not expect to reach panic through the nil embedded interface.
type downStore struct{ Storage }

var errDown = errors.New("dependency down")

func (downStore) PingDatabase(context.Context) error { return errDown }
func (downStore) PingRedis(context.Context) error    { return errDown }

func (downStore) GetSession(context.Context, string) (*quack.AuthSession, error) { return nil, errDown }

type fakeDiscordStatus struct{ connected bool }

func (d fakeDiscordStatus) Status() (bool, string, int64) { return d.connected, "quack", 1 }

// newTestServer builds the full API. Missing deps get test defaults: a
// fresh Redis, storage that is down, empty services, and a disconnected bot.
func newTestServer(t *testing.T, cfg config.Config, deps Deps) *Server {
	t.Helper()
	if deps.Redis == nil {
		_, deps.Redis = newRedis(t)
	}
	if deps.Store == nil {
		deps.Store = downStore{}
	}
	if deps.Services == nil {
		deps.Services = quack.New(quack.Deps{})
	}
	if deps.Discord == nil {
		deps.Discord = fakeDiscordStatus{}
	}
	server, err := New(cfg, deps)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return server
}

// storeServer builds the API over a real SQLite and Redis store.
func storeServer(t *testing.T, store *storage.Store, guilds quack.GuildDirectory, scheduler quack.Scheduler, cfg config.Config) *Server {
	t.Helper()
	return newTestServer(t, cfg, Deps{
		Services: quack.New(quack.Deps{Store: store, Guilds: guilds, Scheduler: scheduler, Modules: modules.NewRegistry(store.DB())}),
		Store:    store,
		Redis:    store.Redis(),
	})
}

func migratedStore(t *testing.T) *storage.Store {
	t.Helper()
	store := testutil.NewSQLiteRedisStore(t)
	if err := store.Migrate(); err != nil {
		t.Fatalf("migrate schema: %v", err)
	}
	return store
}

// send makes a request as the bearer of sessionID (none if empty). Writes
// get a fresh Idempotency-Key unless the caller set one in headers.
func send(t *testing.T, h http.Handler, method, path, body, sessionID string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request := httptest.NewRequest(method, path, reader)
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if sessionID != "" {
		request.Header.Set("Authorization", "Bearer "+sessionID)
	}
	if isWrite(method) {
		request.Header.Set(idempotencyKeyHeader, quack.NewID())
	}
	for i := 0; i+1 < len(headers); i += 2 {
		request.Header.Set(headers[i], headers[i+1])
	}
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	return response
}

func expectStatus(t *testing.T, response *httptest.ResponseRecorder, want int) {
	t.Helper()
	if response.Code != want {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, want, response.Body.String())
	}
}

type fakeGuilds struct {
	userGuilds []quack.DiscordUserGuild
	botGuilds  []quack.DiscordBotGuild
	botGuild   *quack.DiscordBotGuild
}

func (f fakeGuilds) UserGuilds(context.Context, string) ([]quack.DiscordUserGuild, error) {
	return f.userGuilds, nil
}

func (f fakeGuilds) BotGuilds(context.Context) ([]quack.DiscordBotGuild, error) {
	return f.botGuilds, nil
}

func (f fakeGuilds) GuildAuthorization(_ context.Context, guildID, actorID, targetID string) (*quack.DiscordGuildAuthorization, error) {
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
		Guild: *f.botGuild,
		Actor: actor,
		Bot: quack.DiscordMemberAuthorization{
			DiscordUserID: "quack", Present: true, PermissionBits: ^uint64(0), TopRolePosition: 20, Bot: true,
		},
	}
	if targetID != "" {
		snapshot.Target = &quack.DiscordMemberAuthorization{DiscordUserID: targetID, Present: true, TopRolePosition: 1}
	}
	return snapshot, nil
}

// staffGuilds is a Discord where the caller has permissions in guild-1.
func staffGuilds(permissions uint64) fakeGuilds {
	return fakeGuilds{
		userGuilds: []quack.DiscordUserGuild{{ID: "guild-1", Permissions: permissions}},
		botGuild:   &quack.DiscordBotGuild{ID: "guild-1", Name: "Guild", Icon: "icon", OwnerID: "owner-1"},
	}
}

func testSession(discordUserID string) *quack.AuthSession {
	now := time.Now().UTC()
	return &quack.AuthSession{
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

func saveSession(t *testing.T, store *storage.Store, session *quack.AuthSession) string {
	t.Helper()
	if err := store.SaveSession(context.Background(), session, time.Hour); err != nil {
		t.Fatalf("save session: %v", err)
	}
	return session.ID
}

// templateHarness serves a bootstrapped guild-1 whose caller has
// permissions. It returns the server, the session ID, and the store.
func templateHarness(t *testing.T, permissions uint64) (*Server, string, *storage.Store) {
	t.Helper()
	store := migratedStore(t)
	if _, err := store.BootstrapGuild(context.Background(), quack.BootstrapGuildParams{
		Starter:        quack.StarterTemplate(),
		DiscordGuildID: "guild-1", Name: "Guild", OwnerDiscordUserID: "owner-1",
	}); err != nil {
		t.Fatalf("bootstrap guild: %v", err)
	}
	server := storeServer(t, store, staffGuilds(permissions), nil, config.Default())
	return server, saveSession(t, store, testSession("user-1")), store
}

// caseHarness serves guild-1 with one "spam" template. It returns the
// server, the session ID, and the template ID.
func caseHarness(t *testing.T, permissions uint64) (*Server, string, string) {
	t.Helper()
	store := migratedStore(t)
	guild, err := store.UpsertGuild(context.Background(), quack.UpsertGuildParams{
		DiscordGuildID: "guild-1", Name: "Guild", OwnerDiscordUserID: "owner-1",
	})
	if err != nil {
		t.Fatalf("upsert guild: %v", err)
	}
	template, err := store.CreateCaseTemplate(context.Background(), quack.CreateCaseTemplateParams{
		Template: quack.CaseTemplate{
			GuildID:                guild.ID,
			Slug:                   "spam",
			Name:                   "Spam",
			Description:            "Spam template",
			ReasonTemplate:         "No spam",
			CreatedByDiscordUserID: "admin-1",
			UpdatedByDiscordUserID: "admin-1",
		},
		Levels: []quack.ExpandedCaseTemplateLevel{
			{Level: quack.CaseTemplateLevel{Position: 1, Name: "Default", IsDefault: true}},
		},
	})
	if err != nil {
		t.Fatalf("create template: %v", err)
	}
	server := storeServer(t, store, staffGuilds(permissions), nil, config.Default())
	return server, saveSession(t, store, testSession("user-1")), template.Template.ID
}

func templatePayload(slug string) string {
	return `{
		"slug": "` + slug + `",
		"name": "Spam",
		"description": "Spam template",
		"reason_template": "No spam",
		"levels": [{"name": "Default", "position": 1, "is_default": true, "actions": []}]
	}`
}

func casePayload(templateID, targetDiscordUserID string) string {
	return `{
		"template_id": "` + templateID + `",
		"target_discord_user_id": "` + targetDiscordUserID + `",
		"metadata": {"source": "test"}
	}`
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

// TestListErrorsUseServiceTables checks the list handlers' error tables: a
// known sentinel keeps its own status, and anything else keeps the status
// and message the handlers have always sent.
func TestListErrorsUseServiceTables(t *testing.T) {
	for _, test := range []struct {
		name    string
		table   serviceErrors
		err     error
		status  int
		code    errorCode
		message string
	}{
		{"template denial", templateErrors.withFallback("failed to list templates"), quack.ErrTemplatePermissionDenied,
			http.StatusForbidden, codeAuthorization, "template access denied"},
		{"template failure", templateErrors.withFallback("failed to list templates"), errors.New("database down"),
			http.StatusInternalServerError, codeInternal, "failed to list templates"},
		{"settings conflict", settingsErrors, quack.ErrGuildSettingsConflict,
			http.StatusConflict, codeConflict, quack.ErrGuildSettingsConflict.Error()},
		{"guild list failure", guildListErrors, errors.New("discord down"),
			http.StatusBadGateway, codeDependency, "failed to list discord guilds"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			test.table.write(response, httptest.NewRequest(http.MethodGet, "/", nil), test.err)
			var body errorResponse
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if response.Code != test.status || body.Error.Code != test.code || body.Error.Message != test.message {
				t.Fatalf("got %d %+v, want %d %s %q", response.Code, body.Error, test.status, test.code, test.message)
			}
		})
	}
	if templateErrors.fallback != "template operation failed" {
		t.Fatal("withFallback changed the shared table")
	}
}

func TestHTTPServerUsesConfiguredBounds(t *testing.T) {
	cfg := config.Default()
	cfg.API.Port = "9090"
	cfg.API.ReadHeaderTimeout = 2 * time.Second
	cfg.API.ReadTimeout = 3 * time.Second
	cfg.API.WriteTimeout = 4 * time.Second
	cfg.API.IdleTimeout = 5 * time.Second
	server := newHTTPServer(cfg, http.NotFoundHandler())
	if server.Addr != ":9090" || server.ReadHeaderTimeout != 2*time.Second || server.ReadTimeout != 3*time.Second ||
		server.WriteTimeout != 4*time.Second || server.IdleTimeout != 5*time.Second {
		t.Fatalf("server = %+v", server)
	}
}
