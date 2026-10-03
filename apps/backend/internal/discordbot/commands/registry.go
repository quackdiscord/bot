package commands

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot/interactions"
	"github.com/quackdiscord/bot/internal/discordbot/ui"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/redis/go-redis/v9"
)

// CommandSpec binds one Discord command definition to the handler that implements it.
type CommandSpec struct {
	Definition *discordgo.ApplicationCommand
	Handler    ui.Handler
}

// Registry stores command specifications by name for explicit synchronization and interaction dispatch.
type Registry struct {
	commands map[string]CommandSpec
}

// ComponentRegistrar installs integration-owned component and modal handlers
// into the same dispatcher as application commands.
type ComponentRegistrar func(*interactions.ComponentRegistry) error

// NewRegistry constructs registry with required dependencies explicit so callers control lifecycle and substitution.
func NewRegistry() *Registry {
	return &Registry{commands: map[string]CommandSpec{}}
}

// Register explicitly wires register so runtime behavior does not depend on init-time registration.
func (r *Registry) Register(spec CommandSpec) error {
	if r == nil {
		return errors.New("command registry is not configured")
	}
	if spec.Definition == nil {
		return errors.New("command definition is required")
	}

	name := strings.TrimSpace(spec.Definition.Name)
	if name == "" {
		return errors.New("command name is required")
	}
	if spec.Handler == nil {
		return fmt.Errorf("command %s handler is required", name)
	}
	if _, exists := r.commands[name]; exists {
		return fmt.Errorf("command %s is already registered", name)
	}

	r.commands[name] = spec
	return nil
}

// Specs returns a copy of registered command specifications so callers cannot mutate registry state.
func (r *Registry) Specs() []CommandSpec {
	if r == nil {
		return nil
	}

	names := make([]string, 0, len(r.commands))
	for name := range r.commands {
		names = append(names, name)
	}
	sort.Strings(names)

	specs := make([]CommandSpec, 0, len(names))
	for _, name := range names {
		specs = append(specs, r.commands[name])
	}
	return specs
}

// Lookup returns the handler registered for a command name.
func (r *Registry) Lookup(name string) (CommandSpec, bool) {
	if r == nil {
		return CommandSpec{}, false
	}
	spec, ok := r.commands[name]
	return spec, ok
}

// LookupCommand returns the Discord definition registered for a command name.
func (r *Registry) LookupCommand(name string) (ui.Handler, bool) {
	spec, ok := r.Lookup(name)
	if !ok {
		return nil, false
	}
	return spec.Handler, true
}

// Options configures command registration and synchronization.
type Options struct {
	// AppID is the Discord application ID. It defaults to the session user.
	AppID string
	// GuildID, when set, registers commands in one guild instead of globally.
	GuildID string
	// Prune deletes registered commands Quack no longer defines.
	Prune bool
	// Store caches command hashes so unchanged commands are not re-synced.
	// If it also exposes Redis(), interactions are deduplicated there.
	Store CommandHashStore
}

// Register explicitly wires register so runtime behavior does not depend on init-time registration.
func Register(session *discordgo.Session, services *quack.Services, opts Options, componentRegistrars ...ComponentRegistrar) error {
	if session == nil {
		return errors.New("discord session is not configured")
	}
	if services == nil {
		return errors.New("app services are not configured")
	}

	registry := NewRegistry()
	if err := registry.Register(CaseCommandSpec()); err != nil {
		return err
	}
	if err := registry.Register(MessageCaseCommandSpec()); err != nil {
		return err
	}
	dispatcher := interactions.NewDispatcher(services, registry)
	if provider, ok := opts.Store.(interface{ Redis() *redis.Client }); ok {
		dispatcher.Deduper = interactions.NewRedisInteractionDeduper(provider.Redis(), 15*time.Minute)
	}
	for _, register := range componentRegistrars {
		if register == nil {
			continue
		}
		if err := register(dispatcher.Components); err != nil {
			return fmt.Errorf("register Discord components: %w", err)
		}
	}
	session.AddHandler(dispatcher.Handle)

	appID := strings.TrimSpace(opts.AppID)
	if appID == "" && session.State != nil && session.State.User != nil {
		appID = session.State.User.ID
	}
	if appID == "" {
		return errors.New("discord app id is not configured")
	}

	syncer := CommandSyncer{
		Client:       sessionCommandClient{session: session},
		Cache:        newRedisCommandCache(opts.Store),
		AppID:        appID,
		PruneEnabled: opts.Prune,
		GuildID:      strings.TrimSpace(opts.GuildID),
	}
	return syncer.Sync(context.Background(), registry.Specs())
}
