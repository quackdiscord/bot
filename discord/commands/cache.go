package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/quackdiscord/bot/storage"
	r "github.com/redis/go-redis/v9"
)

type commandCacheEntry struct {
	DiscordCommandID string `json:"discord_command_id"`
	Hash             string `json:"hash"`
}

type commandHashCache interface {
	Get(ctx context.Context, scope, commandName string) (*commandCacheEntry, error)
	Set(ctx context.Context, scope, commandName string, entry commandCacheEntry) error
}

type noopCommandCache struct{}

func (noopCommandCache) Get(ctx context.Context, scope, commandName string) (*commandCacheEntry, error) {
	return nil, nil
}

func (noopCommandCache) Set(ctx context.Context, scope, commandName string, entry commandCacheEntry) error {
	return nil
}

type redisCommandCache struct {
	store *storage.Store
}

func newRedisCommandCache(store *storage.Store) commandHashCache {
	if store == nil || store.Redis() == nil {
		return noopCommandCache{}
	}
	return redisCommandCache{store: store}
}

func (c redisCommandCache) Get(ctx context.Context, scope, commandName string) (*commandCacheEntry, error) {
	body, err := c.store.Redis().HGet(ctx, commandCacheKey(scope), commandName).Bytes()
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

func (c redisCommandCache) Set(ctx context.Context, scope, commandName string, entry commandCacheEntry) error {
	body, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("encode command cache: %w", err)
	}
	if err := c.store.Redis().HSet(ctx, commandCacheKey(scope), commandName, body).Err(); err != nil {
		return fmt.Errorf("write command cache: %w", err)
	}
	return nil
}

func commandCacheKey(scope string) string {
	return "discord:commands:" + scope + ":hashes"
}
