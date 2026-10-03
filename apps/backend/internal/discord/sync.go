package discord

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/redis/go-redis/v9"
)

// SyncOptions configures SyncCommands.
type SyncOptions struct {
	// AppID is the Discord application ID.
	AppID string
	// GuildID, when set, registers the commands in that guild only, which
	// Discord applies instantly; global commands can take an hour.
	GuildID string
	// Prune deletes registered commands Quack no longer defines.
	Prune bool
}

// SyncCommands makes Discord's registered commands match Quack's. A command
// is only written when its definition changed, which keeps restarts clear of
// Discord's command rate limits. Fingerprints of what was last written are
// cached in Redis.
func SyncCommands(ctx context.Context, bot *Bot, cache redis.UniversalClient, opts SyncOptions) error {
	appID := strings.TrimSpace(opts.AppID)
	if appID == "" {
		return errors.New("discord app id is not configured")
	}
	s := syncer{
		client: sessionCommands{bot.Session},
		cache:  redisCommandCache{cache},
		appID:  appID,
		guild:  strings.TrimSpace(opts.GuildID),
		prune:  opts.Prune,
	}
	return s.sync(ctx, commands())
}

// commandClient is the part of Discord's command API the syncer uses.
type commandClient interface {
	list(ctx context.Context, appID, guildID string) ([]*discordgo.ApplicationCommand, error)
	create(ctx context.Context, appID, guildID string, command *discordgo.ApplicationCommand) (*discordgo.ApplicationCommand, error)
	edit(ctx context.Context, appID, guildID, commandID string, command *discordgo.ApplicationCommand) (*discordgo.ApplicationCommand, error)
	delete(ctx context.Context, appID, guildID, commandID string) error
}

type sessionCommands struct{ session *discordgo.Session }

func (c sessionCommands) list(ctx context.Context, appID, guildID string) ([]*discordgo.ApplicationCommand, error) {
	return c.session.ApplicationCommands(appID, guildID, rest(ctx)...)
}

func (c sessionCommands) create(ctx context.Context, appID, guildID string, command *discordgo.ApplicationCommand) (*discordgo.ApplicationCommand, error) {
	return c.session.ApplicationCommandCreate(appID, guildID, command, rest(ctx)...)
}

func (c sessionCommands) edit(ctx context.Context, appID, guildID, commandID string, command *discordgo.ApplicationCommand) (*discordgo.ApplicationCommand, error) {
	return c.session.ApplicationCommandEdit(appID, guildID, commandID, command, rest(ctx)...)
}

func (c sessionCommands) delete(ctx context.Context, appID, guildID, commandID string) error {
	return c.session.ApplicationCommandDelete(appID, guildID, commandID, rest(ctx)...)
}

// cachedCommand is what the cache remembers about the last write of one
// command.
type cachedCommand struct {
	DiscordCommandID string `json:"discord_command_id"`
	Hash             string `json:"hash"`
}

// commandCache stores cachedCommand entries per scope ("global" or
// "guild:<id>") and command name.
type commandCache interface {
	get(ctx context.Context, scope, name string) (*cachedCommand, error)
	set(ctx context.Context, scope, name string, entry cachedCommand) error
}

// redisCommandCache keeps one Redis hash per scope, keyed by command name.
type redisCommandCache struct{ client redis.UniversalClient }

func (c redisCommandCache) get(ctx context.Context, scope, name string) (*cachedCommand, error) {
	body, err := c.client.HGet(ctx, commandCacheKey(scope), name).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read command cache: %w", err)
	}
	var entry cachedCommand
	if err := json.Unmarshal(body, &entry); err != nil {
		return nil, fmt.Errorf("decode command cache: %w", err)
	}
	return &entry, nil
}

func (c redisCommandCache) set(ctx context.Context, scope, name string, entry cachedCommand) error {
	body, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("encode command cache: %w", err)
	}
	if err := c.client.HSet(ctx, commandCacheKey(scope), name, body).Err(); err != nil {
		return fmt.Errorf("write command cache: %w", err)
	}
	return nil
}

func commandCacheKey(scope string) string {
	return "discord:commands:" + scope + ":hashes"
}

// syncer reconciles local command definitions with one Discord scope.
type syncer struct {
	client commandClient
	cache  commandCache
	appID  string
	guild  string
	prune  bool
}

func (s syncer) scope() string {
	if s.guild == "" {
		return "global"
	}
	return "guild:" + s.guild
}

func (s syncer) sync(ctx context.Context, local []*discordgo.ApplicationCommand) error {
	remote, err := s.client.list(ctx, s.appID, s.guild)
	if err != nil {
		return fmt.Errorf("list discord application commands: %w", err)
	}
	scope := s.scope()
	slog.Info("Syncing Discord application commands",
		"scope", scope, "app_id", s.appID, "local_command_count", len(local),
		"remote_command_count", len(remote), "prune_enabled", s.prune)

	remoteByName := make(map[string]*discordgo.ApplicationCommand, len(remote))
	for _, command := range remote {
		if command != nil {
			remoteByName[command.Name] = command
		}
	}
	local = slices.Clone(local)
	slices.SortFunc(local, func(a, b *discordgo.ApplicationCommand) int { return strings.Compare(a.Name, b.Name) })
	localNames := make(map[string]bool, len(local))
	for _, command := range local {
		localNames[command.Name] = true
		if err := s.syncOne(ctx, command, remoteByName[command.Name]); err != nil {
			return err
		}
	}

	var stale []*discordgo.ApplicationCommand
	for _, command := range remote {
		if command != nil && !localNames[command.Name] {
			stale = append(stale, command)
		}
	}
	if len(stale) == 0 {
		return nil
	}
	if !s.prune {
		names := make([]string, 0, len(stale))
		for _, command := range stale {
			names = append(names, command.Name)
		}
		slices.Sort(names)
		slog.Info("Remote Discord application commands are not registered locally; pruning disabled",
			"scope", scope, "remote_only_command_count", len(stale), "remote_only_commands", names)
		return nil
	}
	for _, command := range stale {
		slog.Info("Deleting remote Discord application command missing from local registry",
			"scope", scope, "command", command.Name, "remote_command_id", command.ID)
		if err := s.client.delete(ctx, s.appID, s.guild, command.ID); err != nil {
			return fmt.Errorf("delete remote-only discord application command %s: %w", command.Name, err)
		}
	}
	return nil
}

// syncOne creates, edits, or skips one command. It skips when the remote
// definition already matches, and refreshes the cache if it was stale.
func (s syncer) syncOne(ctx context.Context, command, remote *discordgo.ApplicationCommand) error {
	name, scope := command.Name, s.scope()
	localHash, localBody, err := fingerprint(command)
	if err != nil {
		return fmt.Errorf("hash command %s: %w", name, err)
	}
	cached, err := s.cache.get(ctx, scope, name)
	if err != nil {
		slog.Warn("Command cache read failed; falling back to remote comparison", "error", err, "command", name)
	}
	var cachedID, cachedHash string
	if cached != nil {
		cachedID, cachedHash = cached.DiscordCommandID, cached.Hash
	}

	if remote == nil {
		slog.Info("Discord application command missing remotely; creating",
			"command", name, "scope", scope, "local_hash", localHash, "cached_command_id", cachedID, "cached_hash", cachedHash)
		created, err := s.client.create(ctx, s.appID, s.guild, command)
		if err != nil {
			return fmt.Errorf("create discord application command %s: %w", name, err)
		}
		s.remember(ctx, name, created, localHash)
		return nil
	}

	remoteHash, remoteBody, err := fingerprint(remote)
	if err != nil {
		return fmt.Errorf("hash remote command %s: %w", name, err)
	}
	switch {
	case remoteHash == localHash && cachedID == remote.ID && cachedHash == localHash:
		slog.Info("Discord application command is unchanged; skipping",
			"command", name, "scope", scope, "remote_command_id", remote.ID, "local_hash", localHash)
		return nil
	case remoteHash == localHash:
		slog.Info("Discord application command matches remote definition; refreshing cache only",
			"command", name, "scope", scope, "remote_command_id", remote.ID, "local_hash", localHash, "cached_hash", cachedHash)
		s.remember(ctx, name, remote, localHash)
		return nil
	}
	slog.Info("Discord application command definition hash changed; updating",
		"command", name, "scope", scope, "remote_command_id", remote.ID, "local_hash", localHash, "remote_hash", remoteHash)
	slog.Debug("Discord application command canonical definitions differ",
		"command", name, "local_definition", localBody, "remote_definition", remoteBody)
	updated, err := s.client.edit(ctx, s.appID, s.guild, remote.ID, command)
	if err != nil {
		return fmt.Errorf("update discord application command %s: %w", name, err)
	}
	s.remember(ctx, name, updated, localHash)
	return nil
}

// remember caches the written command's ID and hash. A cache failure only
// costs an extra comparison on the next start.
func (s syncer) remember(ctx context.Context, name string, command *discordgo.ApplicationCommand, hash string) {
	id := ""
	if command != nil {
		id = command.ID
	}
	if err := s.cache.set(ctx, s.scope(), name, cachedCommand{DiscordCommandID: id, Hash: hash}); err != nil {
		slog.Warn("Command cache write failed", "error", err, "command", name)
	}
}

// canonicalCommand is the part of a command definition that matters for
// sync. Fields Discord generates (ID, version, application) are left out,
// and Discord's defaults are normalized, so a command read back from Discord
// hashes the same as the definition that created it.
type canonicalCommand struct {
	Type                     discordgo.ApplicationCommandType        `json:"type"`
	Name                     string                                  `json:"name"`
	NameLocalizations        *map[discordgo.Locale]string            `json:"name_localizations,omitempty"`
	Description              string                                  `json:"description,omitempty"`
	DescriptionLocalizations *map[discordgo.Locale]string            `json:"description_localizations,omitempty"`
	DefaultPermission        *bool                                   `json:"default_permission,omitempty"`
	DefaultMemberPermissions *int64                                  `json:"default_member_permissions,omitempty"`
	DMPermission             *bool                                   `json:"dm_permission,omitempty"`
	NSFW                     *bool                                   `json:"nsfw,omitempty"`
	Contexts                 *[]discordgo.InteractionContextType     `json:"contexts,omitempty"`
	IntegrationTypes         *[]discordgo.ApplicationIntegrationType `json:"integration_types,omitempty"`
	Options                  []canonicalOption                       `json:"options,omitempty"`
}

type canonicalOption struct {
	Type                     discordgo.ApplicationCommandOptionType `json:"type"`
	Name                     string                                 `json:"name"`
	NameLocalizations        map[discordgo.Locale]string            `json:"name_localizations,omitempty"`
	Description              string                                 `json:"description,omitempty"`
	DescriptionLocalizations map[discordgo.Locale]string            `json:"description_localizations,omitempty"`
	ChannelTypes             []discordgo.ChannelType                `json:"channel_types,omitempty"`
	Required                 bool                                   `json:"required,omitempty"`
	Options                  []canonicalOption                      `json:"options,omitempty"`
	Autocomplete             bool                                   `json:"autocomplete,omitempty"`
	Choices                  []canonicalChoice                      `json:"choices,omitempty"`
	MinValue                 *float64                               `json:"min_value,omitempty"`
	MaxValue                 float64                                `json:"max_value,omitempty"`
	MinLength                *int                                   `json:"min_length,omitempty"`
	MaxLength                int                                    `json:"max_length,omitempty"`
}

type canonicalChoice struct {
	Name              string                      `json:"name"`
	NameLocalizations map[discordgo.Locale]string `json:"name_localizations,omitempty"`
	Value             any                         `json:"value"`
}

// fingerprint returns the SHA-256 of command's canonical JSON, and the JSON
// itself for debug logs.
func fingerprint(command *discordgo.ApplicationCommand) (string, string, error) {
	commandType := command.Type
	if commandType == 0 {
		commandType = discordgo.ChatApplicationCommand
	}
	nsfw := command.NSFW
	if nsfw != nil && !*nsfw {
		nsfw = nil
	}
	body, err := json.Marshal(canonicalCommand{
		Type:                     commandType,
		Name:                     command.Name,
		NameLocalizations:        command.NameLocalizations,
		Description:              command.Description,
		DescriptionLocalizations: command.DescriptionLocalizations,
		DefaultPermission:        command.DefaultPermission,
		DefaultMemberPermissions: command.DefaultMemberPermissions,
		DMPermission:             command.DMPermission,
		NSFW:                     nsfw,
		Contexts:                 command.Contexts,
		IntegrationTypes:         integrationTypes(command.IntegrationTypes),
		Options:                  canonicalOptions(command.Options),
	})
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), string(body), nil
}

func canonicalOptions(options []*discordgo.ApplicationCommandOption) []canonicalOption {
	var out []canonicalOption
	for _, option := range options {
		if option == nil {
			continue
		}
		var choices []canonicalChoice
		for _, choice := range option.Choices {
			if choice != nil {
				choices = append(choices, canonicalChoice{Name: choice.Name, NameLocalizations: choice.NameLocalizations, Value: choice.Value})
			}
		}
		out = append(out, canonicalOption{
			Type:                     option.Type,
			Name:                     option.Name,
			NameLocalizations:        option.NameLocalizations,
			Description:              option.Description,
			DescriptionLocalizations: option.DescriptionLocalizations,
			ChannelTypes:             option.ChannelTypes,
			Required:                 option.Required,
			Options:                  canonicalOptions(option.Options),
			Autocomplete:             option.Autocomplete,
			Choices:                  choices,
			MinValue:                 option.MinValue,
			MaxValue:                 option.MaxValue,
			MinLength:                option.MinLength,
			MaxLength:                option.MaxLength,
		})
	}
	return out
}

// integrationTypes sorts the list and drops Discord's default of both
// install types, which Discord reports even when it was never set.
func integrationTypes(value *[]discordgo.ApplicationIntegrationType) *[]discordgo.ApplicationIntegrationType {
	if value == nil || len(*value) == 0 {
		return nil
	}
	sorted := slices.Clone(*value)
	slices.Sort(sorted)
	if slices.Equal(sorted, []discordgo.ApplicationIntegrationType{discordgo.ApplicationIntegrationGuildInstall, discordgo.ApplicationIntegrationUserInstall}) {
		return nil
	}
	return &sorted
}
