package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	r "github.com/redis/go-redis/v9"
)

// commandCacheEntry stores one command cache entry value together with the metadata needed to use it safely.
type commandCacheEntry struct {
	DiscordCommandID string `json:"discord_command_id"`
	Hash             string `json:"hash"`
}

// commandHashCache groups the command hash cache state used to keep this package's responsibilities explicit.
type commandHashCache interface {
	Get(ctx context.Context, scope, commandName string) (*commandCacheEntry, error)
	Set(ctx context.Context, scope, commandName string, entry commandCacheEntry) error
}

// noopCommandCache groups the noop command cache state used to keep this package's responsibilities explicit.
type noopCommandCache struct{}

// Get retrieves get without exposing the underlying adapter implementation.
func (noopCommandCache) Get(ctx context.Context, scope, commandName string) (*commandCacheEntry, error) {
	return nil, nil
}

// Set encapsulates the set rule so callers share one consistent package implementation.
func (noopCommandCache) Set(ctx context.Context, scope, commandName string, entry commandCacheEntry) error {
	return nil
}

// redisCommandCache groups the redis command cache state used to keep this package's responsibilities explicit.
type redisCommandCache struct {
	store CommandHashStore
}

// CommandHashStore is the Redis hash storage the command cache uses.
type CommandHashStore interface {
	HashGet(ctx context.Context, key, field string) ([]byte, error)
	HashSet(ctx context.Context, key, field string, value []byte) error
}

// newRedisCommandCache encapsulates the new redis command cache rule so callers share one consistent package implementation.
func newRedisCommandCache(store CommandHashStore) commandHashCache {
	if store == nil {
		return noopCommandCache{}
	}
	return redisCommandCache{store: store}
}

// Get retrieves get without exposing the underlying adapter implementation.
func (c redisCommandCache) Get(ctx context.Context, scope, commandName string) (*commandCacheEntry, error) {
	body, err := c.store.HashGet(ctx, commandCacheKey(scope), commandName)
	if err != nil {
		if errors.Is(err, r.Nil) {
			return nil, nil
		}
		return nil, fmt.Errorf("read command cache: %w", err)
	}

	var entry commandCacheEntry
	if err := json.Unmarshal(body, &entry); err != nil {
		return nil, fmt.Errorf("decode command cache: %w", err)
	}
	return &entry, nil
}

// Set encapsulates the set rule so callers share one consistent package implementation.
func (c redisCommandCache) Set(ctx context.Context, scope, commandName string, entry commandCacheEntry) error {
	body, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("encode command cache: %w", err)
	}
	if err := c.store.HashSet(ctx, commandCacheKey(scope), commandName, body); err != nil {
		return fmt.Errorf("write command cache: %w", err)
	}
	return nil
}

// commandCacheKey encapsulates the command cache key rule so callers share one consistent package implementation.
func commandCacheKey(scope string) string {
	return "discord:commands:" + scope + ":hashes"
}
