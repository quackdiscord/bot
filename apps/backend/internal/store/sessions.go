package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
	"github.com/redis/go-redis/v9"
)

// Redis key prefixes for dashboard login state. A user's session set lets
// RevokeUserSessions find every session without scanning.
const (
	oauthStateKeyPrefix  = "auth:state:"
	sessionKeyPrefix     = "auth:session:"
	userSessionKeyPrefix = "auth:user-sessions:"
)

// sessionRecord is a session as stored in Redis. Unlike quack.AuthSession's
// JSON form, it includes the tokens.
type sessionRecord struct {
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

// revokeUserSessionsScript deletes a user's session set and every session it
// names in one step.
var revokeUserSessionsScript = redis.NewScript(`
local sessions = redis.call("SMEMBERS", KEYS[1])
for _, session_id in ipairs(sessions) do
  redis.call("DEL", ARGV[1] .. session_id)
end
redis.call("DEL", KEYS[1])
return #sessions
`)

// refreshSessionScript rewrites a session only if it still exists, so a
// refresh racing a logout cannot bring the session back.
var refreshSessionScript = redis.NewScript(`
if redis.call("EXISTS", KEYS[1]) == 0 then return 0 end
redis.call("SET", KEYS[1], ARGV[1], "PX", ARGV[2])
redis.call("SADD", KEYS[2], ARGV[3])
local ttl = redis.call("PTTL", KEYS[2])
if ttl < tonumber(ARGV[2]) then redis.call("PEXPIRE", KEYS[2], ARGV[2]) end
return 1
`)

// SaveOAuthState stores the server half of an OAuth login for ttl.
func (s *Store) SaveOAuthState(ctx context.Context, state string, payload *quack.OAuthState, ttl time.Duration) error {
	if s.redis == nil {
		return errNoRedis
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

// ConsumeOAuthState reads and deletes OAuth state in one step, so a callback
// cannot be replayed. It returns nil for unknown or expired state.
func (s *Store) ConsumeOAuthState(ctx context.Context, state string) (*quack.OAuthState, error) {
	if s.redis == nil {
		return nil, errNoRedis
	}
	body, err := s.redis.GetDel(ctx, oauthStateKeyPrefix+state).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read oauth state: %w", err)
	}
	var payload quack.OAuthState
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("unmarshal oauth state: %w", err)
	}
	return &payload, nil
}

// SaveSession stores a session for ttl and adds it to its user's session set.
func (s *Store) SaveSession(ctx context.Context, session *quack.AuthSession, ttl time.Duration) error {
	if s.redis == nil {
		return errNoRedis
	}
	if session == nil || session.ID == "" || session.DiscordUserID == "" || ttl <= 0 {
		return errors.New("valid auth session and TTL are required")
	}
	body, err := json.Marshal(newSessionRecord(session))
	if err != nil {
		return fmt.Errorf("marshal auth session: %w", err)
	}
	_, err = s.redis.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		userKey := userSessionKeyPrefix + session.DiscordUserID
		pipe.Set(ctx, sessionKeyPrefix+session.ID, body, ttl)
		pipe.SAdd(ctx, userKey, session.ID)
		// The set lives as long as its longest-lived session.
		pipe.ExpireNX(ctx, userKey, ttl)
		pipe.ExpireGT(ctx, userKey, ttl)
		return nil
	})
	if err != nil {
		return fmt.Errorf("save auth session: %w", err)
	}
	return nil
}

// GetSession returns a session, or nil when it is unknown or expired.
func (s *Store) GetSession(ctx context.Context, sessionID string) (*quack.AuthSession, error) {
	if s.redis == nil {
		return nil, errNoRedis
	}
	body, err := s.redis.Get(ctx, sessionKeyPrefix+sessionID).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read auth session: %w", err)
	}
	var record sessionRecord
	if err := json.Unmarshal(body, &record); err != nil {
		return nil, fmt.Errorf("unmarshal auth session: %w", err)
	}
	return record.model(), nil
}

// DeleteSession ends one session.
func (s *Store) DeleteSession(ctx context.Context, sessionID string) error {
	session, err := s.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	_, err = s.redis.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Del(ctx, sessionKeyPrefix+sessionID)
		if session != nil {
			pipe.SRem(ctx, userSessionKeyPrefix+session.DiscordUserID, sessionID)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("delete auth session: %w", err)
	}
	return nil
}

// RevokeUserSessions ends every session a user has.
func (s *Store) RevokeUserSessions(ctx context.Context, discordUserID string) error {
	if s.redis == nil {
		return errNoRedis
	}
	if discordUserID == "" {
		return errors.New("discord user id is required")
	}
	keys := []string{userSessionKeyPrefix + discordUserID}
	if err := revokeUserSessionsScript.Run(ctx, s.redis, keys, sessionKeyPrefix).Err(); err != nil {
		return fmt.Errorf("revoke user sessions: %w", err)
	}
	return nil
}

// RefreshSession rewrites a live session with a new ttl. It reports false,
// and writes nothing, when the session has already ended.
func (s *Store) RefreshSession(ctx context.Context, session *quack.AuthSession, ttl time.Duration) (bool, error) {
	if s.redis == nil {
		return false, errNoRedis
	}
	if session == nil || session.ID == "" || session.DiscordUserID == "" || ttl <= 0 {
		return false, errors.New("valid auth session and TTL are required")
	}
	body, err := json.Marshal(newSessionRecord(session))
	if err != nil {
		return false, fmt.Errorf("marshal auth session: %w", err)
	}
	keys := []string{sessionKeyPrefix + session.ID, userSessionKeyPrefix + session.DiscordUserID}
	result, err := refreshSessionScript.Run(ctx, s.redis, keys, body, ttl.Milliseconds(), session.ID).Int()
	if err != nil {
		return false, fmt.Errorf("refresh auth session: %w", err)
	}
	return result == 1, nil
}

func newSessionRecord(s *quack.AuthSession) sessionRecord {
	return sessionRecord{
		ID:               s.ID,
		DiscordUserID:    s.DiscordUserID,
		Username:         s.Username,
		GlobalName:       s.GlobalName,
		Avatar:           s.Avatar,
		AccessToken:      s.AccessToken,
		RefreshToken:     s.RefreshToken,
		CSRFToken:        s.CSRFToken,
		TokenType:        s.TokenType,
		Scope:            s.Scope,
		TokenExpiresAt:   s.TokenExpiresAt,
		SessionExpiresAt: s.SessionExpiresAt,
		CreatedAt:        s.CreatedAt,
		LastSeenAt:       s.LastSeenAt,
	}
}

func (r sessionRecord) model() *quack.AuthSession {
	return &quack.AuthSession{
		ID:               r.ID,
		DiscordUserID:    r.DiscordUserID,
		Username:         r.Username,
		GlobalName:       r.GlobalName,
		Avatar:           r.Avatar,
		AccessToken:      r.AccessToken,
		RefreshToken:     r.RefreshToken,
		CSRFToken:        r.CSRFToken,
		TokenType:        r.TokenType,
		Scope:            r.Scope,
		TokenExpiresAt:   r.TokenExpiresAt,
		SessionExpiresAt: r.SessionExpiresAt,
		CreatedAt:        r.CreatedAt,
		LastSeenAt:       r.LastSeenAt,
	}
}
