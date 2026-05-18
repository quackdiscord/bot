package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/quackdiscord/bot/structs"
	r "github.com/redis/go-redis/v9"
)

const (
	oauthStateKeyPrefix  = "auth:state:"
	authSessionKeyPrefix = "auth:session:"
)

// saves oauth state with a short ttl so callback can verify it
func (s *Store) SaveOAuthState(ctx context.Context, state string, payload *structs.OAuthState, ttl time.Duration) error {
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

// consumes oauth state once and removes it in the same redis call
func (s *Store) ConsumeOAuthState(ctx context.Context, state string) (*structs.OAuthState, error) {
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

	var payload structs.OAuthState
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("unmarshal oauth state: %w", err)
	}

	return &payload, nil
}

// persists full auth session payload in redis
func (s *Store) SaveSession(ctx context.Context, session *structs.AuthSession, ttl time.Duration) error {
	if s == nil || s.redis == nil {
		return errors.New("redis not connected")
	}

	body, err := json.Marshal(session)
	if err != nil {
		return fmt.Errorf("marshal auth session: %w", err)
	}

	if err := s.redis.Set(ctx, authSessionKeyPrefix+session.ID, body, ttl).Err(); err != nil {
		return fmt.Errorf("save auth session: %w", err)
	}

	return nil
}

// fetches a session by id
func (s *Store) GetSession(ctx context.Context, sessionID string) (*structs.AuthSession, error) {
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

	var session structs.AuthSession
	if err := json.Unmarshal(body, &session); err != nil {
		return nil, fmt.Errorf("unmarshal auth session: %w", err)
	}

	return &session, nil
}

// deletes a session on logout or expiry cleanup
func (s *Store) DeleteSession(ctx context.Context, sessionID string) error {
	if s == nil || s.redis == nil {
		return errors.New("redis not connected")
	}

	if err := s.redis.Del(ctx, authSessionKeyPrefix+sessionID).Err(); err != nil {
		return fmt.Errorf("delete auth session: %w", err)
	}

	return nil
}

// keeps session key alive when user is active
func (s *Store) RefreshSessionTTL(ctx context.Context, sessionID string, ttl time.Duration) error {
	if s == nil || s.redis == nil {
		return errors.New("redis not connected")
	}

	if err := s.redis.Expire(ctx, authSessionKeyPrefix+sessionID, ttl).Err(); err != nil {
		return fmt.Errorf("refresh auth session ttl: %w", err)
	}

	return nil
}
