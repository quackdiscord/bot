package api

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/config"
	"github.com/quackdiscord/bot/internal/quack"
)

func TestIdempotencyStoreLifecycle(t *testing.T) {
	server, client := newRedis(t)
	store := newIdempotencyStore(client)
	ctx := context.Background()

	first, err := store.begin(ctx, "case-create", "caller-secret-key", time.Minute, "fingerprint")
	if err != nil || first.state != idempotencyAcquired || first.leaseToken == "" {
		t.Fatalf("first begin = %+v, %v", first, err)
	}
	duplicate, err := store.begin(ctx, "case-create", "caller-secret-key", time.Minute, "fingerprint")
	if err != nil || duplicate.state != idempotencyInProgress || duplicate.leaseToken != "" {
		t.Fatalf("concurrent begin = %+v, %v", duplicate, err)
	}
	if conflict, err := store.begin(ctx, "case-create", "caller-secret-key", time.Minute, "other"); err != nil || conflict.state != idempotencyConflict {
		t.Fatalf("different fingerprint = %+v, %v", conflict, err)
	}
	body := []byte(`{"case_id":"case-1"}`)
	if err := store.complete(ctx, "case-create", "caller-secret-key", "wrong-token", 201, body, time.Hour); err == nil {
		t.Fatal("complete accepted a lease token it did not issue")
	}
	if err := store.complete(ctx, "case-create", "caller-secret-key", first.leaseToken, 201, body, time.Hour); err != nil {
		t.Fatalf("complete: %v", err)
	}
	replay, err := store.begin(ctx, "case-create", "caller-secret-key", time.Minute, "fingerprint")
	if err != nil || replay.state != idempotencyComplete || replay.statusCode != 201 || string(replay.body) != string(body) {
		t.Fatalf("replay = %+v, %v", replay, err)
	}
	for _, key := range server.Keys() {
		if len(key) != len("http:idempotency:")+64 {
			t.Fatalf("Redis key %q is not an opaque hash", key)
		}
	}
	server.FastForward(time.Hour)
	if again, err := store.begin(ctx, "case-create", "caller-secret-key", time.Minute, "fingerprint"); err != nil || again.state != idempotencyAcquired {
		t.Fatalf("after expiry = %+v, %v", again, err)
	}
}

func TestIdempotencyStoreGrantsOneLease(t *testing.T) {
	_, client := newRedis(t)
	store := newIdempotencyStore(client)
	var acquired atomic.Int64
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			result, err := store.begin(context.Background(), "evidence", "same-key", time.Minute, "")
			if err != nil {
				t.Errorf("begin: %v", err)
			}
			if result.state == idempotencyAcquired {
				acquired.Add(1)
			}
		})
	}
	wg.Wait()
	if got := acquired.Load(); got != 1 {
		t.Fatalf("%d lease owners, want 1", got)
	}
}

func TestCoreWritesRequireAndReplayIdempotencyKey(t *testing.T) {
	server, sessionID, store := templateHarness(t, uint64(discordgo.PermissionManageGuild))
	create := func(key, payload string, authorization ...string) (int, string, bool) {
		t.Helper()
		headers := []string{idempotencyKeyHeader, key}
		if len(authorization) > 0 {
			headers = append(headers, "Authorization", authorization[0])
		}
		response := send(t, server, http.MethodPost, "/guilds/guild-1/templates", payload, sessionID, headers...)
		return response.Code, response.Body.String(), response.Header().Get("Idempotency-Replayed") == "true"
	}

	if status, _, _ := create("", templatePayload("private-policy")); status != http.StatusBadRequest {
		t.Fatalf("missing key: status %d, want 400", status)
	}
	status, original, _ := create("create-policy", templatePayload("private-policy"))
	if status != http.StatusCreated {
		t.Fatalf("create: %d %s", status, original)
	}
	// Equivalent bearer spellings share a scope.
	status, replayed, isReplay := create("create-policy", templatePayload("private-policy"), "bearer   "+sessionID)
	if status != http.StatusCreated || !isReplay || replayed != original {
		t.Fatalf("replay: %d replayed=%v %s", status, isReplay, replayed)
	}
	if status, _, _ := create("create-policy", templatePayload("other-policy")); status != http.StatusConflict {
		t.Fatalf("changed payload: status %d, want 409", status)
	}

	// A replay must pass today's authorization, not the original's.
	server.services = quack.New(quack.Deps{Store: store, Guilds: staffGuilds(0)})
	if status, body, _ := create("create-policy", templatePayload("private-policy")); status != http.StatusForbidden || strings.Contains(body, "private-policy") {
		t.Fatalf("demoted manager replay: %d %s", status, body)
	}
	server.services = quack.New(quack.Deps{Store: store, Guilds: staffGuilds(uint64(discordgo.PermissionManageGuild))})
	if err := store.DeleteSession(context.Background(), sessionID); err != nil {
		t.Fatal(err)
	}
	if status, body, _ := create("create-policy", templatePayload("private-policy")); status != http.StatusUnauthorized || strings.Contains(body, "private-policy") {
		t.Fatalf("revoked session replay: %d %s", status, body)
	}
}

func TestIdempotencyScopeIncludesResource(t *testing.T) {
	server, sessionID, templateID := caseHarness(t, uint64(discordgo.PermissionAdministrator))
	for range 2 {
		expectStatus(t, send(t, server, http.MethodPost, "/guilds/guild-1/cases", casePayload(templateID, "target-1"), sessionID), http.StatusCreated)
	}
	void := func(caseRef string) int {
		return send(t, server, http.MethodPost, "/guilds/guild-1/cases/"+caseRef+"/void", `{"reason":"mistake"}`, sessionID,
			idempotencyKeyHeader, "same-key").Code
	}
	if first, second := void("1"), void("2"); first != http.StatusOK || second != http.StatusOK {
		t.Fatalf("void statuses = %d, %d; the key must not replay across cases", first, second)
	}
}

// TestVoidRejectsReplacementCaseID checks that the retired
// replacement_case_id field is still refused, before the case is touched.
func TestVoidRejectsReplacementCaseID(t *testing.T) {
	server, sessionID, templateID := caseHarness(t, uint64(discordgo.PermissionAdministrator))
	expectStatus(t, send(t, server, http.MethodPost, "/guilds/guild-1/cases", casePayload(templateID, "target-1"), sessionID), http.StatusCreated)
	response := send(t, server, http.MethodPost, "/guilds/guild-1/cases/1/void", `{"reason":"mistake","replacement_case_id":"2"}`,
		sessionID, idempotencyKeyHeader, "void-replacement")
	assertEnvelope(t, response, http.StatusBadRequest, codeValidation)
	if !strings.Contains(response.Body.String(), "create the replacement after voiding this case") {
		t.Fatalf("body = %s", response.Body.String())
	}
	expectStatus(t, send(t, server, http.MethodPost, "/guilds/guild-1/cases/1/void", `{"reason":"mistake"}`,
		sessionID, idempotencyKeyHeader, "void-plain"), http.StatusOK)
}

// fakeModule mounts an "example" module whose writes are allowed while
// allowed is set.
type fakeModule struct {
	allowed *atomic.Bool
	calls   *atomic.Int64
}

func (m fakeModule) MountHTTP(mux *ModuleMux) {
	allowed := func(*http.Request) bool { return m.allowed == nil || m.allowed.Load() }
	mux.HandleWrite("PUT /example/settings", allowed, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m.calls != nil {
			m.calls.Add(1)
		}
		writeJSON(w, http.StatusOK, map[string]any{"saved": true, "actor": quack.StaffFromContext(r.Context()).ActorDiscordUserID})
	}))
	mux.HandleWrite("POST /example/fail", allowed, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "token=must-not-persist"})
	}))
}

func TestModuleWrites(t *testing.T) {
	store := migratedStore(t)
	if _, err := store.UpsertGuild(context.Background(), quack.UpsertGuildParams{
		DiscordGuildID: "guild-1", Name: "Guild", OwnerDiscordUserID: "owner-1",
	}); err != nil {
		t.Fatalf("upsert guild: %v", err)
	}
	module := fakeModule{allowed: &atomic.Bool{}, calls: &atomic.Int64{}}
	module.allowed.Store(true)
	server := newTestServer(t, config.Default(), Deps{
		Services: quack.New(quack.Deps{Store: store, Guilds: staffGuilds(uint64(discordgo.PermissionManageGuild))}),
		Store:    store,
		Redis:    store.Redis(),
		Modules:  module.MountHTTP,
	})
	sessionID := saveSession(t, store, testSession("user-1"))
	expectStatus(t, send(t, server, http.MethodPut, "/guilds/guild-1/modules/example/settings", `{}`, sessionID,
		idempotencyKeyHeader, ""), http.StatusBadRequest)
	for attempt := range 2 {
		response := send(t, server, http.MethodPut, "/guilds/guild-1/modules/example/settings", `{}`, sessionID,
			idempotencyKeyHeader, "settings")
		expectStatus(t, response, http.StatusOK)
		if !strings.Contains(response.Body.String(), `"actor":"user-1"`) {
			t.Fatalf("handler did not see the guild staff: %s", response.Body.String())
		}
		if replayed := response.Header().Get("Idempotency-Replayed") == "true"; replayed != (attempt == 1) {
			t.Fatalf("attempt %d replayed = %v", attempt, replayed)
		}
	}
	if module.calls.Load() != 1 {
		t.Fatalf("handler ran %d times, want 1", module.calls.Load())
	}

	// The same key on another operation is a different scope.
	response := send(t, server, http.MethodPost, "/guilds/guild-1/modules/example/fail", `{}`, sessionID, idempotencyKeyHeader, "settings")
	assertEnvelope(t, response, http.StatusBadRequest, codeValidation)
	replay := send(t, server, http.MethodPost, "/guilds/guild-1/modules/example/fail", `{}`, sessionID, idempotencyKeyHeader, "settings")
	assertEnvelope(t, replay, http.StatusBadRequest, codeValidation)
	if replay.Header().Get("Idempotency-Replayed") != "true" || strings.Contains(replay.Body.String(), "must-not-persist") {
		t.Fatalf("stored error was not the envelope: %s", replay.Body.String())
	}

	module.allowed.Store(false)
	response = send(t, server, http.MethodPut, "/guilds/guild-1/modules/example/settings", `{}`, sessionID, idempotencyKeyHeader, "settings")
	assertEnvelope(t, response, http.StatusForbidden, codeAuthorization)
	if strings.Contains(response.Body.String(), "saved") {
		t.Fatalf("denied caller got the replay: %s", response.Body.String())
	}
}
