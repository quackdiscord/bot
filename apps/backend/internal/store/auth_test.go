package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/redis/go-redis/v9"
)

func TestOAuthStateConsumeIsSingleUseAndExpires(t *testing.T) {
	server, adapter := authStoreHarness(t)
	ctx := context.Background()
	payload := &quack.OAuthState{RedirectTo: "/cases", ResponseMode: "json", CreatedAt: time.Now().UTC()}
	if err := adapter.SaveOAuthState(ctx, "state-secret", payload, time.Minute); err != nil {
		t.Fatalf("save state: %v", err)
	}
	consumed, err := adapter.ConsumeOAuthState(ctx, "state-secret")
	if err != nil || consumed == nil || consumed.RedirectTo != "/cases" {
		t.Fatalf("unexpected consumed state: %+v err=%v", consumed, err)
	}
	replayed, err := adapter.ConsumeOAuthState(ctx, "state-secret")
	if err != nil || replayed != nil {
		t.Fatalf("expected replay rejection, got %+v err=%v", replayed, err)
	}
	if err := adapter.SaveOAuthState(ctx, "expiring", payload, time.Minute); err != nil {
		t.Fatalf("save expiring state: %v", err)
	}
	server.FastForward(time.Minute)
	expired, err := adapter.ConsumeOAuthState(ctx, "expiring")
	if err != nil || expired != nil {
		t.Fatalf("expected expired state, got %+v err=%v", expired, err)
	}
}

func TestSessionRoundTripExpiryAndUserRevocation(t *testing.T) {
	server, adapter := authStoreHarness(t)
	ctx := context.Background()
	now := time.Now().UTC()
	first := &quack.AuthSession{
		ID: "session-one", DiscordUserID: "user-1", Username: "user", AccessToken: "access-secret",
		RefreshToken: "refresh-secret", CSRFToken: "csrf-secret", TokenExpiresAt: now.Add(time.Hour),
		SessionExpiresAt: now.Add(time.Hour), CreatedAt: now, LastSeenAt: now,
	}
	second := *first
	second.ID = "session-two"
	for _, session := range []*quack.AuthSession{first, &second} {
		if err := adapter.SaveSession(ctx, session, time.Hour); err != nil {
			t.Fatalf("save session: %v", err)
		}
	}
	loaded, err := adapter.GetSession(ctx, first.ID)
	if err != nil || loaded == nil || loaded.AccessToken != first.AccessToken || loaded.RefreshToken != first.RefreshToken || loaded.CSRFToken != first.CSRFToken {
		t.Fatalf("unexpected private session round trip: %+v err=%v", loaded, err)
	}
	for _, key := range server.Keys() {
		if strings.Contains(key, first.AccessToken) || strings.Contains(key, first.RefreshToken) {
			t.Fatalf("credential leaked into Redis key %q", key)
		}
	}
	if err := adapter.RevokeUserSessions(ctx, "user-1"); err != nil {
		t.Fatalf("revoke user sessions: %v", err)
	}
	for _, sessionID := range []string{first.ID, second.ID} {
		loaded, err := adapter.GetSession(ctx, sessionID)
		if err != nil || loaded != nil {
			t.Fatalf("expected revoked %s, got %+v err=%v", sessionID, loaded, err)
		}
	}
	if err := adapter.SaveSession(ctx, first, time.Minute); err != nil {
		t.Fatalf("save expiring session: %v", err)
	}
	server.FastForward(time.Minute)
	loaded, err = adapter.GetSession(ctx, first.ID)
	if err != nil || loaded != nil {
		t.Fatalf("expected Redis TTL expiry, got %+v err=%v", loaded, err)
	}
}

func TestAuthStoreUnavailableBehavior(t *testing.T) {
	adapter := New(nil, nil)
	ctx := context.Background()
	if err := adapter.SaveSession(ctx, &quack.AuthSession{ID: "id", DiscordUserID: "user"}, time.Minute); err == nil {
		t.Fatal("expected unavailable SaveSession error")
	}
	if _, err := adapter.GetSession(ctx, "id"); err == nil {
		t.Fatal("expected unavailable GetSession error")
	}
	if err := adapter.RevokeUserSessions(ctx, "user"); err == nil {
		t.Fatal("expected unavailable RevokeUserSessions error")
	}
}

// authStoreHarness constructs a Redis-only auth adapter.
func authStoreHarness(t *testing.T) (*miniredis.Miniredis, *Store) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return server, New(nil, client)
}

func TestSessionRefreshCannotResurrectRevocation(t *testing.T) {
	for _, all := range []bool{false, true} {
		_, adapter := authStoreHarness(t)
		ctx := context.Background()
		session := &quack.AuthSession{ID: "session", DiscordUserID: "user"}
		if err := adapter.SaveSession(ctx, session, time.Hour); err != nil {
			t.Fatal(err)
		}
		loaded, err := adapter.GetSession(ctx, session.ID)
		if err != nil || loaded == nil {
			t.Fatal("session was not created")
		}
		if ok, err := adapter.RefreshSession(ctx, loaded, time.Hour); err != nil || !ok {
			t.Fatalf("live refresh: %v", err)
		}
		if all {
			err = adapter.RevokeUserSessions(ctx, session.DiscordUserID)
		} else {
			err = adapter.DeleteSession(ctx, session.ID)
		}
		if err != nil {
			t.Fatal(err)
		}
		if ok, err := adapter.RefreshSession(ctx, loaded, time.Hour); err != nil || ok {
			t.Fatalf("revoked session refreshed: ok=%v err=%v", ok, err)
		}
		if got, err := adapter.GetSession(ctx, session.ID); err != nil || got != nil {
			t.Fatalf("session resurrected: %v", err)
		}
	}
}
