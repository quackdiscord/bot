package commands

import (
	"context"
	"testing"

	"github.com/bwmarrin/discordgo"
)

type fakeCommandClient struct {
	commands []*discordgo.ApplicationCommand
	created  []*discordgo.ApplicationCommand
	edited   []*discordgo.ApplicationCommand
	deleted  []string
}

func (f *fakeCommandClient) ListCommands(ctx context.Context, appID, guildID string) ([]*discordgo.ApplicationCommand, error) {
	return f.commands, nil
}

func (f *fakeCommandClient) CreateCommand(ctx context.Context, appID, guildID string, command *discordgo.ApplicationCommand) (*discordgo.ApplicationCommand, error) {
	created := cloneCommand(command)
	created.ID = "created-" + command.Name
	f.created = append(f.created, created)
	f.commands = append(f.commands, created)
	return created, nil
}

func (f *fakeCommandClient) EditCommand(ctx context.Context, appID, guildID, commandID string, command *discordgo.ApplicationCommand) (*discordgo.ApplicationCommand, error) {
	updated := cloneCommand(command)
	updated.ID = commandID
	f.edited = append(f.edited, updated)
	return updated, nil
}

func (f *fakeCommandClient) DeleteCommand(ctx context.Context, appID, guildID, commandID string) error {
	f.deleted = append(f.deleted, commandID)
	return nil
}

type memoryCommandCache struct {
	entries map[string]commandCacheEntry
}

func newMemoryCommandCache() *memoryCommandCache {
	return &memoryCommandCache{entries: map[string]commandCacheEntry{}}
}

func (c *memoryCommandCache) Get(ctx context.Context, scope, commandName string) (*commandCacheEntry, error) {
	entry, ok := c.entries[scope+"|"+commandName]
	if !ok {
		return nil, nil
	}
	return &entry, nil
}

func (c *memoryCommandCache) Set(ctx context.Context, scope, commandName string, entry commandCacheEntry) error {
	c.entries[scope+"|"+commandName] = entry
	return nil
}

func TestCommandSyncerCreatesMissingCommand(t *testing.T) {
	client := &fakeCommandClient{}
	cache := newMemoryCommandCache()
	syncer := testCommandSyncer(client, cache)

	if err := syncer.Sync(context.Background(), []CommandSpec{{Definition: CommandDefinition(), Handler: noopHandler}}); err != nil {
		t.Fatalf("sync commands: %v", err)
	}

	if len(client.created) != 1 || len(client.edited) != 0 {
		t.Fatalf("expected one create and no edits, got creates=%d edits=%d", len(client.created), len(client.edited))
	}
	if _, ok := cache.entries["global|case"]; !ok {
		t.Fatalf("expected command hash cache to be written")
	}
}

func TestCommandSyncerSkipsUnchangedCachedCommand(t *testing.T) {
	remote := cloneCommand(CommandDefinition())
	remote.ID = "remote-case"
	localHash, err := CommandHash(CommandDefinition())
	if err != nil {
		t.Fatalf("hash command: %v", err)
	}

	client := &fakeCommandClient{commands: []*discordgo.ApplicationCommand{remote}}
	cache := newMemoryCommandCache()
	cache.entries["global|case"] = commandCacheEntry{DiscordCommandID: remote.ID, Hash: localHash}
	syncer := testCommandSyncer(client, cache)

	if err := syncer.Sync(context.Background(), []CommandSpec{{Definition: CommandDefinition(), Handler: noopHandler}}); err != nil {
		t.Fatalf("sync commands: %v", err)
	}

	if len(client.created) != 0 || len(client.edited) != 0 {
		t.Fatalf("expected no create/edit, got creates=%d edits=%d", len(client.created), len(client.edited))
	}
	if len(client.deleted) != 0 {
		t.Fatalf("expected no deletes, got %d", len(client.deleted))
	}
}

func TestCommandSyncerEditsChangedCommand(t *testing.T) {
	remote := cloneCommand(CommandDefinition())
	remote.ID = "remote-case"
	local := CommandDefinition()
	local.Description = "Updated description"

	client := &fakeCommandClient{commands: []*discordgo.ApplicationCommand{remote}}
	cache := newMemoryCommandCache()
	syncer := testCommandSyncer(client, cache)

	if err := syncer.Sync(context.Background(), []CommandSpec{{Definition: local, Handler: noopHandler}}); err != nil {
		t.Fatalf("sync commands: %v", err)
	}

	if len(client.created) != 0 || len(client.edited) != 1 {
		t.Fatalf("expected one edit, got creates=%d edits=%d", len(client.created), len(client.edited))
	}
	if client.edited[0].ID != remote.ID {
		t.Fatalf("expected existing command id to be edited, got %q", client.edited[0].ID)
	}
}

func TestCommandSyncerIgnoresRemoteOnlyCommandsWhenPruneDisabled(t *testing.T) {
	remote := cloneCommand(CommandDefinition())
	remote.ID = "remote-case"
	extra := &discordgo.ApplicationCommand{ID: "remote-extra", Name: "extra", Description: "Extra"}

	client := &fakeCommandClient{commands: []*discordgo.ApplicationCommand{remote, extra}}
	cache := newMemoryCommandCache()
	syncer := testCommandSyncer(client, cache)

	if err := syncer.Sync(context.Background(), []CommandSpec{{Definition: CommandDefinition(), Handler: noopHandler}}); err != nil {
		t.Fatalf("sync commands: %v", err)
	}

	if len(client.deleted) != 0 {
		t.Fatalf("expected no deletes with pruning disabled, got %+v", client.deleted)
	}
}

func TestCommandSyncerDeletesRemoteOnlyCommandsWhenPruneEnabled(t *testing.T) {
	remote := cloneCommand(CommandDefinition())
	remote.ID = "remote-case"
	extra := &discordgo.ApplicationCommand{ID: "remote-extra", Name: "extra", Description: "Extra"}

	client := &fakeCommandClient{commands: []*discordgo.ApplicationCommand{remote, extra}}
	cache := newMemoryCommandCache()
	syncer := testCommandSyncer(client, cache)
	syncer.PruneEnabled = true

	if err := syncer.Sync(context.Background(), []CommandSpec{{Definition: CommandDefinition(), Handler: noopHandler}}); err != nil {
		t.Fatalf("sync commands: %v", err)
	}

	if len(client.deleted) != 1 || client.deleted[0] != "remote-extra" {
		t.Fatalf("expected remote extra command to be deleted, got %+v", client.deleted)
	}
}

func TestCommandSyncerRefreshesMissingCacheForIdenticalRemote(t *testing.T) {
	remote := cloneCommand(CommandDefinition())
	remote.ID = "remote-case"

	client := &fakeCommandClient{commands: []*discordgo.ApplicationCommand{remote}}
	cache := newMemoryCommandCache()
	syncer := testCommandSyncer(client, cache)

	if err := syncer.Sync(context.Background(), []CommandSpec{{Definition: CommandDefinition(), Handler: noopHandler}}); err != nil {
		t.Fatalf("sync commands: %v", err)
	}

	if len(client.created) != 0 || len(client.edited) != 0 {
		t.Fatalf("expected no create/edit, got creates=%d edits=%d", len(client.created), len(client.edited))
	}
	entry, ok := cache.entries["global|case"]
	if !ok || entry.DiscordCommandID != remote.ID {
		t.Fatalf("expected cache refresh for remote command, got %+v", entry)
	}
}

func testCommandSyncer(client *fakeCommandClient, cache commandHashCache) CommandSyncer {
	return CommandSyncer{
		Client: client,
		Cache:  cache,
		AppID:  "app-1",
	}
}

func cloneCommand(command *discordgo.ApplicationCommand) *discordgo.ApplicationCommand {
	if command == nil {
		return nil
	}
	clone := *command
	if command.Options != nil {
		clone.Options = cloneOptions(command.Options)
	}
	return &clone
}

func cloneOptions(options []*discordgo.ApplicationCommandOption) []*discordgo.ApplicationCommandOption {
	out := make([]*discordgo.ApplicationCommandOption, 0, len(options))
	for _, option := range options {
		if option == nil {
			continue
		}
		clone := *option
		if option.Options != nil {
			clone.Options = cloneOptions(option.Options)
		}
		out = append(out, &clone)
	}
	return out
}
