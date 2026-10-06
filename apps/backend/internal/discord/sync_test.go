package discord

import (
	"context"
	"slices"
	"strings"
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
		caseCommandName:        "3dd34455ab6f53897bb837b02466099d5682eb603798a87ba5e73d0000c31b64",
		messageCaseCommandName: "57ff5e9c60b08d46c0742deaad1239e86ee00415a67b5b5a54f31c2852797d46",
		userCaseCommandName:    "8deda4a790a677cbccc1b8192e176be4c5bbda9fe5ea7540de64b8a8c5f09d8c",
		templateCommandName:    "7094a68153fa4671fe89ecf8372fcef92a53215fe92446b14cc255771d37c51b",
		appealsCommandName:     "c4c28da3d479feb6128a58df54a86f3ec144841a812275f3ba017e6c5bbb09ca",
		helpCommandName:        "45656e62a33c545f254b43453d75d86f8113fed9d75cb2189186fcab7e3469e8",
		uiPreviewCommandName:   "f796b81f7fecdc7372a908d5cf7ed78392e310cb78ac5d65559e6a8227e5e82a",
		setupCommandName:       "fbaf8b51b42c0f65648f9a632cb802becfbf43de64744220eb1dd159da2d7c1a",
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
		if option.Name == "context" {
			t.Fatal("add still takes raw JSON context")
		}
	}
	if file := add.Options[3]; file.Name != "file" || file.Type != discordgo.ApplicationCommandOptionAttachment {
		t.Fatalf("add has no file option: %+v", file)
	}
	var names []string
	for _, option := range command.Options {
		names = append(names, option.Name)
	}
	want := "add evidence view list user failures retry dismiss void reverse"
	if got := strings.Join(names, " "); got != want {
		t.Fatalf("subcommands = %q, want %q", got, want)
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

// TestGuildSyncIgnoresDMPermission pins the restart loop that hit Discord's
// rate limit: guild commands come back without dm_permission, which must not
// count as a change.
func TestGuildSyncIgnoresDMPermission(t *testing.T) {
	local := caseCommand()
	//lint:ignore SA1019 the test needs the field the commands set.
	if local.DMPermission == nil {
		t.Fatal("caseCommand no longer sets DMPermission; this test is moot")
	}
	remote := remoteCase(func(c *discordgo.ApplicationCommand) {
		//lint:ignore SA1019 Discord omits it for guild commands.
		c.DMPermission = nil
	})
	client := &fakeCommands{remote: []*discordgo.ApplicationCommand{remote}}
	s := syncer{client: client, cache: memoryCommandCache{}, appID: "sync-guild-dm", guild: "guild"}
	for range 2 {
		if err := s.sync(context.Background(), []*discordgo.ApplicationCommand{local}); err != nil {
			t.Fatal(err)
		}
	}
	if len(client.created) != 0 || len(client.edited) != 0 {
		t.Fatalf("created=%d edited=%d, want no writes", len(client.created), len(client.edited))
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

// TestSyncRetiresRenamedContextMenus checks that the old "Create moderation
// case" and "Create case for member" registrations go once their
// replacements sync, even with pruning off, and only then.
func TestSyncRetiresRenamedContextMenus(t *testing.T) {
	old := func() []*discordgo.ApplicationCommand {
		return []*discordgo.ApplicationCommand{
			{ID: "old-message", Type: discordgo.MessageApplicationCommand, Name: "Create moderation case"},
			{ID: "old-user", Type: discordgo.UserApplicationCommand, Name: "Create case for member"},
		}
	}
	client := &fakeCommands{remote: old()}
	s := syncer{client: client, cache: memoryCommandCache{}, appID: "sync-rename"}
	if err := s.sync(context.Background(), commands()); err != nil {
		t.Fatal(err)
	}
	if len(client.deleted) != 2 || client.deleted[0] != "old-message" || client.deleted[1] != "old-user" {
		t.Fatalf("deleted %v, want both old context menus", client.deleted)
	}
	client = &fakeCommands{remote: old()}
	s = syncer{client: client, cache: memoryCommandCache{}, appID: "sync-rename-missing"}
	if err := s.sync(context.Background(), []*discordgo.ApplicationCommand{caseCommand()}); err != nil {
		t.Fatal(err)
	}
	if len(client.deleted) != 0 {
		t.Fatalf("retired %v without its replacement", client.deleted)
	}
}
