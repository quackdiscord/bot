package commands

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/rs/zerolog/log"
)

type DiscordCommandClient interface {
	ListCommands(ctx context.Context, appID, guildID string) ([]*discordgo.ApplicationCommand, error)
	CreateCommand(ctx context.Context, appID, guildID string, command *discordgo.ApplicationCommand) (*discordgo.ApplicationCommand, error)
	EditCommand(ctx context.Context, appID, guildID, commandID string, command *discordgo.ApplicationCommand) (*discordgo.ApplicationCommand, error)
	DeleteCommand(ctx context.Context, appID, guildID, commandID string) error
}

type CommandSyncer struct {
	Client DiscordCommandClient
	Cache  commandHashCache

	AppID        string
	GuildID      string
	PruneEnabled bool
}

type sessionCommandClient struct {
	session *discordgo.Session
}

func (c sessionCommandClient) ListCommands(ctx context.Context, appID, guildID string) ([]*discordgo.ApplicationCommand, error) {
	_ = ctx
	return c.session.ApplicationCommands(appID, guildID)
}

func (c sessionCommandClient) CreateCommand(ctx context.Context, appID, guildID string, command *discordgo.ApplicationCommand) (*discordgo.ApplicationCommand, error) {
	_ = ctx
	return c.session.ApplicationCommandCreate(appID, guildID, command)
}

func (c sessionCommandClient) EditCommand(ctx context.Context, appID, guildID, commandID string, command *discordgo.ApplicationCommand) (*discordgo.ApplicationCommand, error) {
	_ = ctx
	return c.session.ApplicationCommandEdit(appID, guildID, commandID, command)
}

func (c sessionCommandClient) DeleteCommand(ctx context.Context, appID, guildID, commandID string) error {
	_ = ctx
	return c.session.ApplicationCommandDelete(appID, guildID, commandID)
}

func (s CommandSyncer) Sync(ctx context.Context, specs []CommandSpec) error {
	if s.Client == nil {
		return errors.New("discord command client is not configured")
	}
	if strings.TrimSpace(s.AppID) == "" {
		return errors.New("discord app id is not configured")
	}
	cache := s.Cache
	if cache == nil {
		cache = noopCommandCache{}
	}

	existing, err := s.Client.ListCommands(ctx, s.AppID, s.GuildID)
	if err != nil {
		return fmt.Errorf("list discord application commands: %w", err)
	}

	scope := commandScope(s.GuildID)
	log.Info().
		Str("scope", scope).
		Str("app_id", s.AppID).
		Int("local_command_count", len(specs)).
		Int("remote_command_count", len(existing)).
		Bool("prune_enabled", s.PruneEnabled).
		Msg("Syncing Discord application commands")

	existingByName := make(map[string]*discordgo.ApplicationCommand, len(existing))
	for _, command := range existing {
		if command == nil {
			continue
		}
		existingByName[command.Name] = command
	}

	localCommandNames := make(map[string]struct{}, len(specs))
	for _, spec := range sortedSpecs(specs) {
		if spec.Definition == nil {
			continue
		}
		localCommandNames[spec.Definition.Name] = struct{}{}
		if err := s.syncOne(ctx, cache, scope, existingByName[spec.Definition.Name], spec); err != nil {
			return err
		}
	}

	return s.pruneRemoteOnlyCommands(ctx, scope, existing, localCommandNames)
}

func (s CommandSyncer) syncOne(ctx context.Context, cache commandHashCache, scope string, remote *discordgo.ApplicationCommand, spec CommandSpec) error {
	command := spec.Definition
	commandName := command.Name
	localHash, localDefinition, err := CommandFingerprint(command)
	if err != nil {
		return fmt.Errorf("hash command %s: %w", commandName, err)
	}

	cached, err := cache.Get(ctx, scope, commandName)
	if err != nil {
		log.Warn().Err(err).Str("command", commandName).Msg("Command cache read failed; falling back to remote comparison")
	}
	log.Debug().
		Str("command", commandName).
		Str("scope", scope).
		Str("local_hash", localHash).
		Str("cached_command_id", cachedCommandID(cached)).
		Str("cached_hash", cachedHash(cached)).
		Bool("remote_exists", remote != nil).
		Msg("Evaluating Discord application command sync")

	if remote == nil {
		log.Info().
			Str("command", commandName).
			Str("scope", scope).
			Str("local_hash", localHash).
			Str("cached_command_id", cachedCommandID(cached)).
			Str("cached_hash", cachedHash(cached)).
			Msg("Discord application command missing remotely; creating")
		created, err := s.Client.CreateCommand(ctx, s.AppID, s.GuildID, command)
		if err != nil {
			return fmt.Errorf("create discord application command %s: %w", commandName, err)
		}
		s.cacheCommand(ctx, cache, scope, commandName, commandID(created), localHash)
		log.Info().Str("command", commandName).Msg("Registered Discord application command")
		return nil
	}

	remoteHash, remoteDefinition, err := CommandFingerprint(remote)
	if err != nil {
		return fmt.Errorf("hash remote command %s: %w", commandName, err)
	}
	if shouldSkipCommandSync(cached, remote.ID, localHash, remoteHash) {
		log.Info().
			Str("command", commandName).
			Str("scope", scope).
			Str("remote_command_id", remote.ID).
			Str("local_hash", localHash).
			Str("remote_hash", remoteHash).
			Str("cached_hash", cachedHash(cached)).
			Msg("Discord application command is unchanged; skipping")
		return nil
	}
	if remoteHash == localHash {
		log.Info().
			Str("command", commandName).
			Str("scope", scope).
			Str("remote_command_id", remote.ID).
			Str("local_hash", localHash).
			Str("remote_hash", remoteHash).
			Str("cached_command_id", cachedCommandID(cached)).
			Str("cached_hash", cachedHash(cached)).
			Msg("Discord application command matches remote definition; refreshing cache only")
		s.cacheCommand(ctx, cache, scope, commandName, remote.ID, localHash)
		return nil
	}

	log.Info().
		Str("command", commandName).
		Str("scope", scope).
		Str("remote_command_id", remote.ID).
		Str("local_hash", localHash).
		Str("remote_hash", remoteHash).
		Str("cached_command_id", cachedCommandID(cached)).
		Str("cached_hash", cachedHash(cached)).
		Msg("Discord application command definition hash changed; updating")
	log.Debug().
		Str("command", commandName).
		Str("local_definition", localDefinition).
		Str("remote_definition", remoteDefinition).
		Msg("Discord application command canonical definitions differ")

	updated, err := s.Client.EditCommand(ctx, s.AppID, s.GuildID, remote.ID, command)
	if err != nil {
		return fmt.Errorf("update discord application command %s: %w", commandName, err)
	}
	s.cacheCommand(ctx, cache, scope, commandName, commandID(updated), localHash)
	log.Info().Str("command", commandName).Msg("Updated Discord application command")
	return nil
}

func (s CommandSyncer) pruneRemoteOnlyCommands(ctx context.Context, scope string, remoteCommands []*discordgo.ApplicationCommand, localCommandNames map[string]struct{}) error {
	remoteOnly := make([]*discordgo.ApplicationCommand, 0)
	for _, command := range remoteCommands {
		if command == nil {
			continue
		}
		if _, ok := localCommandNames[command.Name]; ok {
			continue
		}
		remoteOnly = append(remoteOnly, command)
	}
	if len(remoteOnly) == 0 {
		return nil
	}

	remoteOnlyNames := commandNames(remoteOnly)
	if !s.PruneEnabled {
		log.Info().
			Str("scope", scope).
			Int("remote_only_command_count", len(remoteOnly)).
			Strs("remote_only_commands", remoteOnlyNames).
			Msg("Remote Discord application commands are not registered locally; pruning disabled")
		return nil
	}

	for _, command := range remoteOnly {
		log.Info().
			Str("scope", scope).
			Str("command", command.Name).
			Str("remote_command_id", command.ID).
			Msg("Deleting remote Discord application command missing from local registry")
		if err := s.Client.DeleteCommand(ctx, s.AppID, s.GuildID, command.ID); err != nil {
			return fmt.Errorf("delete remote-only discord application command %s: %w", command.Name, err)
		}
	}

	return nil
}

func (s CommandSyncer) cacheCommand(ctx context.Context, cache commandHashCache, scope, commandName, commandID, hash string) {
	if err := cache.Set(ctx, scope, commandName, commandCacheEntry{
		DiscordCommandID: commandID,
		Hash:             hash,
	}); err != nil {
		log.Warn().Err(err).Str("command", commandName).Msg("Command cache write failed")
	}
}

func shouldSkipCommandSync(cached *commandCacheEntry, remoteCommandID, localHash, remoteHash string) bool {
	return cached != nil &&
		cached.DiscordCommandID == remoteCommandID &&
		cached.Hash == localHash &&
		remoteHash == localHash
}

func cachedCommandID(cached *commandCacheEntry) string {
	if cached == nil {
		return ""
	}
	return cached.DiscordCommandID
}

func cachedHash(cached *commandCacheEntry) string {
	if cached == nil {
		return ""
	}
	return cached.Hash
}

func commandScope(guildID string) string {
	guildID = strings.TrimSpace(guildID)
	if guildID == "" {
		return "global"
	}
	return "guild:" + guildID
}

func commandID(command *discordgo.ApplicationCommand) string {
	if command == nil {
		return ""
	}
	return command.ID
}

func sortedSpecs(specs []CommandSpec) []CommandSpec {
	out := make([]CommandSpec, 0, len(specs))
	out = append(out, specs...)
	sort.Slice(out, func(i, j int) bool {
		left := ""
		right := ""
		if out[i].Definition != nil {
			left = out[i].Definition.Name
		}
		if out[j].Definition != nil {
			right = out[j].Definition.Name
		}
		return left < right
	})
	return out
}

func commandNames(commands []*discordgo.ApplicationCommand) []string {
	names := make([]string, 0, len(commands))
	for _, command := range commands {
		if command == nil {
			continue
		}
		names = append(names, command.Name)
	}
	sort.Strings(names)
	return names
}
