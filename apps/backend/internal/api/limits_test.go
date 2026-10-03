package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/config"
)

func TestRateLimiterWindow(t *testing.T) {
	server, client := newRedis(t)
	limiter := newRateLimiter(client)
	limit := config.Limit{Max: 2, Window: time.Minute}
	ctx := context.Background()

	for i, want := range []rateDecision{{allowed: true, remaining: 1}, {allowed: true, remaining: 0}, {allowed: false}} {
		got, err := limiter.allow(ctx, "actor-secret", limit)
		if err != nil || got.allowed != want.allowed || got.remaining != want.remaining {
			t.Fatalf("request %d = %+v, %v; want %+v", i+1, got, err, want)
		}
		if !got.allowed && got.retryAfter <= 0 {
			t.Fatalf("denied request has no retry-after: %+v", got)
		}
	}
	for _, key := range server.Keys() {
		if len(key) != len("http:rate:")+64 {
			t.Fatalf("Redis key %q is not an opaque hash", key)
		}
	}
	server.FastForward(time.Minute)
	if got, err := limiter.allow(ctx, "actor-secret", limit); err != nil || !got.allowed || got.remaining != 1 {
		t.Fatalf("after the window = %+v, %v", got, err)
	}

	server.Close()
	if got, err := limiter.allow(ctx, "actor-secret", limit); !errors.Is(err, errUnavailable) || got.allowed {
		t.Fatalf("with Redis down = %+v, %v; want errUnavailable", got, err)
	}
}

func TestRateLimiterIsAtomic(t *testing.T) {
	_, client := newRedis(t)
	limiter := newRateLimiter(client)
	var allowed atomic.Int64
	var wg sync.WaitGroup
	for range 40 {
		wg.Go(func() {
			decision, err := limiter.allow(context.Background(), "one-actor", config.Limit{Max: 7, Window: time.Minute})
			if err != nil {
				t.Errorf("allow: %v", err)
			}
			if decision.allowed {
				allowed.Add(1)
			}
		})
	}
	wg.Wait()
	if got := allowed.Load(); got != 7 {
		t.Fatalf("allowed %d requests, want 7", got)
	}
}

// TestEndpointClasses checks which configured limit each route spends from,
// using distinct maximums so RateLimit-Limit identifies the class.
func TestEndpointClasses(t *testing.T) {
	cfg := config.Default()
	cfg.Limits.MemberRead.Max = 101
	cfg.Limits.TemplateWrite.Max = 102
	cfg.Limits.CaseCreate.Max = 103
	cfg.Limits.Retry.Max = 104
	cfg.Limits.Evidence.Max = 105
	server := newTestServer(t, cfg, Deps{Modules: fakeModule{}})
	for _, test := range []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/members/me/cases/case-1", 101},
		{http.MethodGet, "/guilds/guild-1/cases", 101},
		{http.MethodPut, "/guilds/guild-1/modules/example/settings", 102},
		{http.MethodPost, "/guilds/guild-1/cases/case-1/void", 102},
		{http.MethodPost, "/members/me/cases/case-1/appeal", 102},
		{http.MethodPost, "/guilds/guild-1/cases", 105}, // case-create, then evidence
		{http.MethodPost, "/guilds/guild-1/action-failures/action-1/retry", 104},
		{http.MethodPost, "/guilds/guild-1/cases/case-1/reversals", 104},
		{http.MethodPost, "/guilds/guild-1/appeals/appeal-1/reversals", 104},
		{http.MethodGet, "/livez", 0},
	} {
		response := send(t, server, test.method, test.path, "", "")
		got, _ := strconv.Atoi(response.Header().Get("RateLimit-Limit"))
		if got != test.want {
			t.Errorf("%s %s: RateLimit-Limit = %d, want %d", test.method, test.path, got, test.want)
		}
	}
}

func TestEndpointLimitRejectsWithRetryAfter(t *testing.T) {
	cfg := config.Default()
	cfg.Limits.MemberRead = config.Limit{Max: 1, Window: time.Minute}
	server := newTestServer(t, cfg, Deps{})
	// The session store is down, so requests that pass the limit get a 503.
	expectStatus(t, send(t, server, http.MethodGet, "/guilds", "", "session"), http.StatusServiceUnavailable)
	response := send(t, server, http.MethodGet, "/guilds", "", "session")
	assertEnvelope(t, response, http.StatusTooManyRequests, codeRateLimited)
	if response.Header().Get("Retry-After") == "" || response.Header().Get("RateLimit-Remaining") != "0" {
		t.Fatalf("headers = %v", response.Header())
	}
	// Another credential has its own counter.
	expectStatus(t, send(t, server, http.MethodGet, "/guilds", "", "other"), http.StatusServiceUnavailable)
}

func TestLimitsFailClosedWithoutRedis(t *testing.T) {
	redisServer, client := newRedis(t)
	server := newTestServer(t, config.Default(), Deps{Redis: client})
	redisServer.Close()
	for _, path := range []string{"/members/me/cases/case-1", "/auth/discord/login"} {
		assertEnvelope(t, send(t, server, http.MethodGet, path, "", ""), http.StatusServiceUnavailable, codeDependency)
	}
}

func TestPaginationIsBounded(t *testing.T) {
	server := newTestServer(t, config.Default(), Deps{})
	for _, query := range []string{"limit=0", "limit=101", "limit=x", "offset=-1", "offset=100001", "cursor=" + strings.Repeat("a", 257)} {
		response := send(t, server, http.MethodGet, "/guilds/guild-1/cases?"+query, "", "")
		assertEnvelope(t, response, http.StatusBadRequest, codeValidation)
	}
}
