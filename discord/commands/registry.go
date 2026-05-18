package commands

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/app"
	"github.com/quackdiscord/bot/lib"
	"github.com/rs/zerolog/log"
)

type Handler func(ctx context.Context, services *app.Services, session *discordgo.Session, interaction *discordgo.InteractionCreate) *discordgo.InteractionResponse

type CommandSpec struct {
	Definition *discordgo.ApplicationCommand
	Handler    Handler
}

type Registry struct {
	commands map[string]CommandSpec
}

var defaultRegistry = NewRegistry()

func NewRegistry() *Registry {
	return &Registry{commands: map[string]CommandSpec{}}
}

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

func (r *Registry) Lookup(name string) (CommandSpec, bool) {
	if r == nil {
		return CommandSpec{}, false
	}
	spec, ok := r.commands[name]
	return spec, ok
}

func registerCommand(spec CommandSpec) {
	if err := defaultRegistry.Register(spec); err != nil {
		panic(err)
	}
}

func Register(session *discordgo.Session, services *app.Services) error {
	if session == nil {
		return errors.New("discord session is not configured")
	}
	if services == nil {
		return errors.New("app services are not configured")
	}

	registry := defaultRegistry
	session.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		if i == nil || i.Interaction == nil {
			return
		}
		if i.Type != discordgo.InteractionApplicationCommand && i.Type != discordgo.InteractionApplicationCommandAutocomplete {
			return
		}

		data := i.ApplicationCommandData()
		spec, ok := registry.Lookup(data.Name)
		if !ok {
			return
		}

		response := spec.Handler(context.Background(), services, s, i)
		if response == nil {
			return
		}
		if err := s.InteractionRespond(i.Interaction, response); err != nil {
			log.Error().Err(err).Str("command", data.Name).Msg("failed to respond to command interaction")
		}
	})

	appID := strings.TrimSpace(lib.Config.Discord.AppID)
	if appID == "" && session.State != nil && session.State.User != nil {
		appID = session.State.User.ID
	}
	if appID == "" {
		return errors.New("discord app id is not configured")
	}

	syncer := CommandSyncer{
		Client:       sessionCommandClient{session: session},
		Cache:        newRedisCommandCache(services.Store),
		AppID:        appID,
		PruneEnabled: lib.Config.Discord.CommandPrune,
		GuildID: strings.TrimSpace(
			lib.Config.Discord.CommandGuildID,
		),
	}
	return syncer.Sync(context.Background(), registry.Specs())
}
