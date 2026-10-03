package store_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/store"
	"github.com/quackdiscord/bot/internal/testutil"
	"github.com/redis/go-redis/v9"
)

func TestOAuthStateIsSingleUseAndExpires(t *testing.T) {
	server, s := sessionStore(t)
	ctx := context.Background()
	payload := &quack.OAuthState{RedirectTo: "/cases", ResponseMode: "json", CreatedAt: time.Now().UTC()}
	if err := s.SaveOAuthState(ctx, "state", payload, time.Minute); err != nil {
		t.Fatal(err)
	}
	if got, err := s.ConsumeOAuthState(ctx, "state"); err != nil || got == nil || got.RedirectTo != "/cases" {
		t.Fatalf("consume = %+v, %v", got, err)
	}
	if got, err := s.ConsumeOAuthState(ctx, "state"); err != nil || got != nil {
		t.Fatalf("replay = %+v, %v; want nil, nil", got, err)
	}
	if err := s.SaveOAuthState(ctx, "expiring", payload, time.Minute); err != nil {
		t.Fatal(err)
	}
	server.FastForward(time.Minute)
	if got, err := s.ConsumeOAuthState(ctx, "expiring"); err != nil || got != nil {
		t.Fatalf("expired consume = %+v, %v; want nil, nil", got, err)
	}
}

func TestSessionRoundTripExpiryAndRevocation(t *testing.T) {
	server, s := sessionStore(t)
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
		if err := s.SaveSession(ctx, session, time.Hour); err != nil {
			t.Fatal(err)
		}
	}
	loaded, err := s.GetSession(ctx, first.ID)
	if err != nil || loaded == nil || loaded.AccessToken != first.AccessToken ||
		loaded.RefreshToken != first.RefreshToken || loaded.CSRFToken != first.CSRFToken {
		t.Fatalf("round trip lost tokens: %+v, %v", loaded, err)
	}
	for _, key := range server.Keys() {
		if strings.Contains(key, first.AccessToken) || strings.Contains(key, first.RefreshToken) {
			t.Fatalf("token leaked into Redis key %q", key)
		}
	}
	if err := s.RevokeUserSessions(ctx, "user-1"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{first.ID, second.ID} {
		if got, err := s.GetSession(ctx, id); err != nil || got != nil {
			t.Fatalf("revoked session %s = %+v, %v", id, got, err)
		}
	}
	if err := s.SaveSession(ctx, first, time.Minute); err != nil {
		t.Fatal(err)
	}
	server.FastForward(time.Minute)
	if got, err := s.GetSession(ctx, first.ID); err != nil || got != nil {
		t.Fatalf("expired session = %+v, %v", got, err)
	}
}

func TestSessionRefreshCannotResurrectRevokedSession(t *testing.T) {
	ctx := context.Background()
	revocations := map[string]func(*store.Store, *quack.AuthSession) error{
		"logout": func(s *store.Store, session *quack.AuthSession) error { return s.DeleteSession(ctx, session.ID) },
		"logout all": func(s *store.Store, session *quack.AuthSession) error {
			return s.RevokeUserSessions(ctx, session.DiscordUserID)
		},
	}
	for name, revoke := range revocations {
		t.Run(name, func(t *testing.T) {
			_, s := sessionStore(t)
			session := &quack.AuthSession{ID: "session", DiscordUserID: "user"}
			if err := s.SaveSession(ctx, session, time.Hour); err != nil {
				t.Fatal(err)
			}
			if ok, err := s.RefreshSession(ctx, session, time.Hour); err != nil || !ok {
				t.Fatalf("live refresh = %v, %v", ok, err)
			}
			if err := revoke(s, session); err != nil {
				t.Fatal(err)
			}
			if ok, err := s.RefreshSession(ctx, session, time.Hour); err != nil || ok {
				t.Fatalf("refresh after revocation = %v, %v; want false", ok, err)
			}
			if got, err := s.GetSession(ctx, session.ID); err != nil || got != nil {
				t.Fatalf("session came back: %+v, %v", got, err)
			}
		})
	}
}

// sessionStore returns a store backed by a miniredis the test can control.
func sessionStore(t *testing.T) (*miniredis.Miniredis, *store.Store) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return server, store.New(testutil.NewSQLiteDB(t), client)
}
