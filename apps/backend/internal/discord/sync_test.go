package discord

import (
	"context"
	"slices"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/bwmarrin/discordgo"
	"github.com/redis/go-redis/v9"
)

// TestCommandDefinitionsAreUnchanged pins the exact command definitions
// Discord has registered. A change here re-registers the commands for every
// guild, so it must be deliberate.
func TestCommandDefinitionsAreUnchanged(t *testing.T) {
	want := map[string]string{
		caseCommandName:        "58f25940bf8fe38cc382d4d057563eb1c8dad9c91f5c9fd01a39a00008e1aefa",
		messageCaseCommandName: "9eeaed148c9e935ad4b6cfaa628be6fc4887e21f8457c130813f2c2ec96d2b51",
		templateCommandName:    "7094a68153fa4671fe89ecf8372fcef92a53215fe92446b14cc255771d37c51b",
		appealsCommandName:     "c4c28da3d479feb6128a58df54a86f3ec144841a812275f3ba017e6c5bbb09ca",
		helpCommandName:        "45656e62a33c545f254b43453d75d86f8113fed9d75cb2189186fcab7e3469e8",
		uiPreviewCommandName:   "f796b81f7fecdc7372a908d5cf7ed78392e310cb78ac5d65559e6a8227e5e82a",
	}
	for _, command := range syncedCommands(true) {
		hash, body, err := fingerprint(command)
		if err != nil {
			t.Fatal(err)
		}
		if hash != want[command.Name] {
			t.Errorf("%s changed: %s\n%s", command.Name, hash, body)
		}
	}
}

func TestUIPreviewIsSyncedOnlyInDev(t *testing.T) {
	has := func(list []*discordgo.ApplicationCommand) bool {
		return slices.ContainsFunc(list, func(c *discordgo.ApplicationCommand) bool { return c.Name == uiPreviewCommandName })
	}
	if has(syncedCommands(false)) || !has(syncedCommands(true)) {
		t.Fatal("/ui-preview must be registered on development bots only")
	}
}

func TestCaseCommandShape(t *testing.T) {
	command := caseCommand()
	add := command.Options[0]
	if add.Name != "add" || len(add.Options) != 4 || !add.Options[0].Autocomplete {
		t.Fatalf("unexpected add subcommand: %+v", add)
	}
	for position, option := range add.Options {
		if option.Required != (position < 2) {
			t.Fatalf("required options must come first: %+v", add.Options)
		}
	}
	for _, option := range command.Options {
		switch option.Name {
		case "warn", "timeout", "kick", "ban":
			t.Fatalf("legacy direct punishment command remains: %s", option.Name)
		}
	}
}

func TestFingerprintNormalizesDiscordDefaults(t *testing.T) {
	nsfwFalse, nsfwTrue := false, true
	bothInstalls := []discordgo.ApplicationIntegrationType{discordgo.ApplicationIntegrationUserInstall, discordgo.ApplicationIntegrationGuildInstall}
	guildInstall := []discordgo.ApplicationIntegrationType{discordgo.ApplicationIntegrationGuildInstall}
	tests := []struct {
		name   string
		change func(*discordgo.ApplicationCommand)
		same   bool
	}{
		{"generated fields", func(c *discordgo.ApplicationCommand) {
			c.ID, c.ApplicationID, c.GuildID, c.Version = "id", "app", "guild", "v"
		}, true},
		{"explicit chat type", func(c *discordgo.ApplicationCommand) { c.Type = discordgo.ChatApplicationCommand }, true},
		{"nsfw false", func(c *discordgo.ApplicationCommand) { c.NSFW = &nsfwFalse }, true},
		{"default integration types", func(c *discordgo.ApplicationCommand) { c.IntegrationTypes = &bothInstalls }, true},
		{"nsfw true", func(c *discordgo.ApplicationCommand) { c.NSFW = &nsfwTrue }, false},
		{"guild install only", func(c *discordgo.ApplicationCommand) { c.IntegrationTypes = &guildInstall }, false},
		{"option description", func(c *discordgo.ApplicationCommand) { c.Options[0].Options[0].Description = "Changed" }, false},
	}
	base, _, _ := fingerprint(caseCommand())
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := caseCommand()
			test.change(command)
			hash, _, err := fingerprint(command)
			if err != nil {
				t.Fatal(err)
			}
			if (hash == base) != test.same {
				t.Fatalf("hash equal=%v, want %v", hash == base, test.same)
			}
		})
	}
}

type fakeCommands struct {
	remote  []*discordgo.ApplicationCommand
	created []*discordgo.ApplicationCommand
	edited  []*discordgo.ApplicationCommand
	deleted []string
}

func (f *fakeCommands) list(context.Context, string, string) ([]*discordgo.ApplicationCommand, error) {
	return f.remote, nil
}

func (f *fakeCommands) create(_ context.Context, _, _ string, command *discordgo.ApplicationCommand) (*discordgo.ApplicationCommand, error) {
	created := *command
	created.ID = "created-" + command.Name
	f.created = append(f.created, &created)
	return &created, nil
}

func (f *fakeCommands) edit(_ context.Context, _, _, commandID string, command *discordgo.ApplicationCommand) (*discordgo.ApplicationCommand, error) {
	updated := *command
	updated.ID = commandID
	f.edited = append(f.edited, &updated)
	return &updated, nil
}

func (f *fakeCommands) delete(_ context.Context, _, _, commandID string) error {
	f.deleted = append(f.deleted, commandID)
	return nil
}

type memoryCommandCache map[string]cachedCommand

func (c memoryCommandCache) get(_ context.Context, scope, name string) (*cachedCommand, error) {
	entry, ok := c[scope+"|"+name]
	if !ok {
		return nil, nil
	}
	return &entry, nil
}

func (c memoryCommandCache) set(_ context.Context, scope, name string, entry cachedCommand) error {
	c[scope+"|"+name] = entry
	return nil
}

func remoteCase(change func(*discordgo.ApplicationCommand)) *discordgo.ApplicationCommand {
	command := caseCommand()
	command.ID = "remote-case"
	if change != nil {
		change(command)
	}
	return command
}

func TestSyncer(t *testing.T) {
	localHash, _, _ := fingerprint(caseCommand())
	extra := &discordgo.ApplicationCommand{ID: "remote-extra", Name: "extra", Description: "Extra"}
	tests := []struct {
		name         string
		remote       []*discordgo.ApplicationCommand
		cache        memoryCommandCache
		prune        bool
		wantCreated  int
		wantEdited   int
		wantDeleted  []string
		wantCacheID  string
		wantMentions string
	}{
		{name: "creates missing", wantCreated: 1, wantCacheID: "created-case", wantMentions: "</case view:created-case> /extra"},
		{
			name:         "skips unchanged cached",
			remote:       []*discordgo.ApplicationCommand{remoteCase(nil)},
			cache:        memoryCommandCache{"global|case": {DiscordCommandID: "remote-case", Hash: localHash}},
			wantMentions: "</case view:remote-case> /extra",
		},
		{
			name:         "refreshes stale cache for identical remote",
			remote:       []*discordgo.ApplicationCommand{remoteCase(nil)},
			wantCacheID:  "remote-case",
			wantMentions: "</case view:remote-case> /extra",
		},
		{
			name:         "edits changed",
			remote:       []*discordgo.ApplicationCommand{remoteCase(func(c *discordgo.ApplicationCommand) { c.Description = "Old" })},
			wantEdited:   1,
			wantCacheID:  "remote-case",
			wantMentions: "</case view:remote-case> /extra",
		},
		{
			name:         "keeps remote-only without prune",
			remote:       []*discordgo.ApplicationCommand{remoteCase(nil), extra},
			wantMentions: "</case view:remote-case> </extra:remote-extra>",
		},
		{
			name:         "prunes remote-only",
			remote:       []*discordgo.ApplicationCommand{remoteCase(nil), extra},
			prune:        true,
			wantDeleted:  []string{"remote-extra"},
			wantMentions: "</case view:remote-case> /extra",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeCommands{remote: test.remote}
			cache := test.cache
			if cache == nil {
				cache = memoryCommandCache{}
			}
			appID := "sync-" + test.name
			s := syncer{client: client, cache: cache, appID: appID, prune: test.prune}
			if err := s.sync(context.Background(), []*discordgo.ApplicationCommand{caseCommand()}); err != nil {
				t.Fatal(err)
			}
			if len(client.created) != test.wantCreated || len(client.edited) != test.wantEdited || len(client.deleted) != len(test.wantDeleted) {
				t.Fatalf("created=%d edited=%d deleted=%v", len(client.created), len(client.edited), client.deleted)
			}
			for i, id := range test.wantDeleted {
				if client.deleted[i] != id {
					t.Fatalf("deleted %v, want %v", client.deleted, test.wantDeleted)
				}
			}
			if test.wantCacheID != "" && cache["global|case"].DiscordCommandID != test.wantCacheID {
				t.Fatalf("cache = %+v, want command %s", cache["global|case"], test.wantCacheID)
			}
			// Synced IDs make command references clickable; a pruned
			// command's reference goes back to plain text.
			if got := ResolveCommandMentions("/case view /extra", appID); got != test.wantMentions {
				t.Fatalf("mentions = %q, want %q", got, test.wantMentions)
			}
		})
	}
}

func TestRedisCommandCacheRoundTrip(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := redisCommandCache{client}
	ctx := context.Background()
	if entry, err := cache.get(ctx, "global", "case"); entry != nil || err != nil {
		t.Fatalf("empty cache = %+v, %v", entry, err)
	}
	if err := cache.set(ctx, "guild:1", "case", cachedCommand{DiscordCommandID: "id", Hash: "hash"}); err != nil {
		t.Fatal(err)
	}
	if !server.Exists("discord:commands:guild:1:hashes") {
		t.Fatal("cache key layout changed")
	}
	entry, err := cache.get(ctx, "guild:1", "case")
	if err != nil || entry == nil || *entry != (cachedCommand{DiscordCommandID: "id", Hash: "hash"}) {
		t.Fatalf("cache round trip = %+v, %v", entry, err)
	}
}
