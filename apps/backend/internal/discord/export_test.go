package discord

import (
	"context"
	"flag"
	"os"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

// Hooks for the tests in package discord_test. Those tests need a real
// store, and the store may import module packages that import this one, so
// they cannot be internal tests.

const (
	MessageCaseCommandName = messageCaseCommandName
	UserCaseCommandName    = userCaseCommandName
)

// ChannelPoster is what case handlers post channel messages through.
type ChannelPoster = channelPoster

// Cases exposes the case handlers.
type Cases struct {
	c      *cases
	router *Router
}

func NewCases(services *quack.Services, poster ChannelPoster, dashboardURL string) Cases {
	c := newCases(services, poster, dashboardURL)
	router := newRouter(nil, nil)
	c.register(router)
	return Cases{c, router}
}

// Component returns the handler routed for the case component action.
func (c Cases) Component(action string) Handler {
	handler, _ := lookup(c.router.components, MustCustomID(CustomID{Namespace: "case", Action: action, Version: "v1"}))
	return handler
}

// Modal returns the handler routed for the case modal action.
func (c Cases) Modal(action string) Handler {
	handler, _ := lookup(c.router.modals, MustCustomID(CustomID{Namespace: "case", Action: action, Version: "v1"}))
	return handler
}

func (c Cases) UserCommand(ctx context.Context, i *discordgo.InteractionCreate) Result {
	return c.c.userCommand(ctx, i)
}

func (c Cases) Command(ctx context.Context, i *discordgo.InteractionCreate) Result {
	return c.c.command(ctx, i)
}

func (c Cases) MessageCommand(ctx context.Context, i *discordgo.InteractionCreate) Result {
	return c.c.messageCommand(ctx, i)
}

func (c Cases) MessageTemplate(ctx context.Context, i *discordgo.InteractionCreate) Result {
	return c.c.messageTemplate(ctx, i)
}

func (c Cases) ContextModal(ctx context.Context, i *discordgo.InteractionCreate) Result {
	return c.c.contextModal(ctx, i)
}

func (c Cases) ContextNext(ctx context.Context, i *discordgo.InteractionCreate) Result {
	return c.c.contextNext(ctx, i)
}

func (c Cases) VoidButton(ctx context.Context, i *discordgo.InteractionCreate) Result {
	return c.c.voidButton(ctx, i)
}

func (c Cases) ReverseButton(ctx context.Context, i *discordgo.InteractionCreate) Result {
	return c.c.reverseButton(ctx, i)
}

// Lifecycle exposes the guild lifecycle gateway handlers.
type Lifecycle struct{ *lifecycle }

func NewLifecycle(guilds *quack.GuildService, evidence *quack.EvidenceService) Lifecycle {
	return Lifecycle{&lifecycle{guilds: guilds, evidence: evidence}}
}

func (l Lifecycle) GuildCreate(e *discordgo.GuildCreate)     { l.guildCreate(nil, e) }
func (l Lifecycle) GuildUpdate(e *discordgo.GuildUpdate)     { l.guildUpdate(nil, e) }
func (l Lifecycle) GuildDelete(e *discordgo.GuildDelete)     { l.guildDelete(nil, e) }
func (l Lifecycle) ChannelDelete(e *discordgo.ChannelDelete) { l.channelDelete(nil, e) }

// updateGolden rewrites golden files instead of comparing: go test
// ./internal/discord -update. Review the diff; only deliberate copy or
// layout changes belong in it.
var updateGolden = flag.Bool("update", false, "rewrite golden files")

// AssertGolden fails t unless got matches the golden file at path.
func AssertGolden(t *testing.T, path, got string) {
	t.Helper()
	if *updateGolden {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("%s differs.\ngot:\n%s", path, got)
	}
}
