package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
	r "github.com/redis/go-redis/v9"
)

const (
	oauthStateKeyPrefix  = "auth:state:"
	authSessionKeyPrefix = "auth:session:"
	authUserKeyPrefix    = "auth:user-sessions:"
)

// authSessionRecord is the private Redis representation for fields intentionally excluded from public JSON.
type authSessionRecord struct {
	ID               string    `json:"id"`
	DiscordUserID    string    `json:"discord_user_id"`
	Username         string    `json:"username"`
	GlobalName       string    `json:"global_name"`
	Avatar           string    `json:"avatar"`
	AccessToken      string    `json:"access_token"`
	RefreshToken     string    `json:"refresh_token"`
	CSRFToken        string    `json:"csrf_token"`
	TokenType        string    `json:"token_type"`
	Scope            string    `json:"scope"`
	TokenExpiresAt   time.Time `json:"token_expires_at"`
	SessionExpiresAt time.Time `json:"session_expires_at"`
	CreatedAt        time.Time `json:"created_at"`
	LastSeenAt       time.Time `json:"last_seen_at"`
}

// revokeUserSessionsScript atomically removes the user index and every session it names.
var revokeUserSessionsScript = r.NewScript(`
local sessions = redis.call("SMEMBERS", KEYS[1])
for _, session_id in ipairs(sessions) do
  redis.call("DEL", ARGV[1] .. session_id)
end
redis.call("DEL", KEYS[1])
return #sessions
`)

// SaveOAuthState stores short-lived OAuth state so the callback can reject unsolicited or replayed login attempts.
func (s *Store) SaveOAuthState(ctx context.Context, state string, payload *quack.OAuthState, ttl time.Duration) error {
	if s == nil || s.redis == nil {
		return errors.New("redis not connected")
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal oauth state: %w", err)
	}

	if err := s.redis.Set(ctx, oauthStateKeyPrefix+state, body, ttl).Err(); err != nil {
		return fmt.Errorf("save oauth state: %w", err)
	}

	return nil
}

// ConsumeOAuthState atomically reads and deletes OAuth state so one authorization response cannot be replayed.
func (s *Store) ConsumeOAuthState(ctx context.Context, state string) (*quack.OAuthState, error) {
	if s == nil || s.redis == nil {
		return nil, errors.New("redis not connected")
	}

	key := oauthStateKeyPrefix + state
	body, err := s.redis.GetDel(ctx, key).Bytes()
	if err != nil {
		if errors.Is(err, r.Nil) {
			return nil, nil
		}
		return nil, fmt.Errorf("read oauth state: %w", err)
	}

	var payload quack.OAuthState
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("unmarshal oauth state: %w", err)
	}

	return &payload, nil
}

// SaveSession persists the complete authentication session with its configured expiry.
func (s *Store) SaveSession(ctx context.Context, session *quack.AuthSession, ttl time.Duration) error {
	if s == nil || s.redis == nil {
		return errors.New("redis not connected")
	}

	if session == nil || session.ID == "" || session.DiscordUserID == "" || ttl <= 0 {
		return errors.New("valid auth session and TTL are required")
	}
	body, err := json.Marshal(authSessionRecordFromModel(session))
	if err != nil {
		return fmt.Errorf("marshal auth session: %w", err)
	}

	_, err = s.redis.TxPipelined(ctx, func(pipe r.Pipeliner) error {
		pipe.Set(ctx, authSessionKeyPrefix+session.ID, body, ttl)
		userKey := authUserKeyPrefix + session.DiscordUserID
		pipe.SAdd(ctx, userKey, session.ID)
		pipe.ExpireNX(ctx, userKey, ttl)
		pipe.ExpireGT(ctx, userKey, ttl)
		return nil
	})
	if err != nil {
		return fmt.Errorf("save auth session: %w", err)
	}

	return nil
}

// GetSession loads a session by ID and treats a missing Redis key as an unauthenticated request rather than a storage failure.
func (s *Store) GetSession(ctx context.Context, sessionID string) (*quack.AuthSession, error) {
	if s == nil || s.redis == nil {
		return nil, errors.New("redis not connected")
	}

	body, err := s.redis.Get(ctx, authSessionKeyPrefix+sessionID).Bytes()
	if err != nil {
		if errors.Is(err, r.Nil) {
			return nil, nil
		}
		return nil, fmt.Errorf("read auth session: %w", err)
	}

	var record authSessionRecord
	if err := json.Unmarshal(body, &record); err != nil {
		return nil, fmt.Errorf("unmarshal auth session: %w", err)
	}
	return record.model(), nil
}

// DeleteSession removes a session during logout or explicit invalidation.
func (s *Store) DeleteSession(ctx context.Context, sessionID string) error {
	if s == nil || s.redis == nil {
		return errors.New("redis not connected")
	}

	session, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	_, err = s.redis.TxPipelined(ctx, func(pipe r.Pipeliner) error {
		pipe.Del(ctx, authSessionKeyPrefix+sessionID)
		if session != nil && session.DiscordUserID != "" {
			pipe.SRem(ctx, authUserKeyPrefix+session.DiscordUserID, sessionID)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("delete auth session: %w", err)
	}

	return nil
}

// RevokeUserSessions removes every currently indexed session for logout-all, compromise response, or account changes.
func (s *Store) RevokeUserSessions(ctx context.Context, discordUserID string) error {
	if s == nil || s.redis == nil {
		return errors.New("redis not connected")
	}
	if discordUserID == "" {
		return errors.New("discord user id is required")
	}
	userKey := authUserKeyPrefix + discordUserID
	if err := revokeUserSessionsScript.Run(ctx, s.redis, []string{userKey}, authSessionKeyPrefix).Err(); err != nil {
		return fmt.Errorf("revoke user sessions: %w", err)
	}
	return nil
}

// refreshSessionScript updates only a still-live session, atomically with its revocation index.
var refreshSessionScript = r.NewScript(`
if redis.call("EXISTS", KEYS[1]) == 0 then return 0 end
redis.call("SET", KEYS[1], ARGV[1], "PX", ARGV[2])
redis.call("SADD", KEYS[2], ARGV[3])
local ttl = redis.call("PTTL", KEYS[2])
if ttl < tonumber(ARGV[2]) then redis.call("PEXPIRE", KEYS[2], ARGV[2]) end
return 1
`)

// RefreshSession extends a live session without recreating one revoked by concurrent logout.
func (s *Store) RefreshSession(ctx context.Context, session *quack.AuthSession, ttl time.Duration) (bool, error) {
	if s == nil || s.redis == nil {
		return false, errors.New("redis not connected")
	}
	if session == nil || session.ID == "" || session.DiscordUserID == "" || ttl <= 0 {
		return false, errors.New("valid auth session and TTL are required")
	}
	body, err := json.Marshal(authSessionRecordFromModel(session))
	if err != nil {
		return false, fmt.Errorf("marshal auth session: %w", err)
	}
	result, err := refreshSessionScript.Run(ctx, s.redis, []string{authSessionKeyPrefix + session.ID, authUserKeyPrefix + session.DiscordUserID}, body, ttl.Milliseconds(), session.ID).Int()
	if err != nil {
		return false, fmt.Errorf("refresh auth session: %w", err)
	}
	return result == 1, nil
}

// authSessionRecordFromModel maps a domain session into its private Redis representation.
func authSessionRecordFromModel(session *quack.AuthSession) authSessionRecord {
	return authSessionRecord{
		ID: session.ID, DiscordUserID: session.DiscordUserID, Username: session.Username,
		GlobalName: session.GlobalName, Avatar: session.Avatar, AccessToken: session.AccessToken,
		RefreshToken: session.RefreshToken, CSRFToken: session.CSRFToken, TokenType: session.TokenType,
		Scope: session.Scope, TokenExpiresAt: session.TokenExpiresAt, SessionExpiresAt: session.SessionExpiresAt,
		CreatedAt: session.CreatedAt, LastSeenAt: session.LastSeenAt,
	}
}

// model maps a private Redis session record back to the domain boundary.
func (record authSessionRecord) model() *quack.AuthSession {
	return &quack.AuthSession{
		ID: record.ID, DiscordUserID: record.DiscordUserID, Username: record.Username,
		GlobalName: record.GlobalName, Avatar: record.Avatar, AccessToken: record.AccessToken,
		RefreshToken: record.RefreshToken, CSRFToken: record.CSRFToken, TokenType: record.TokenType,
		Scope: record.Scope, TokenExpiresAt: record.TokenExpiresAt, SessionExpiresAt: record.SessionExpiresAt,
		CreatedAt: record.CreatedAt, LastSeenAt: record.LastSeenAt,
	}
}
