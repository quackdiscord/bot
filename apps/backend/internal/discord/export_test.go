package discord

import (
	"context"
	"os"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

// Hooks for the tests in package discord_test. Those tests need a real
// store, and the store may import module packages that import this one, so
// they cannot be internal tests.

const MessageCaseCommandName = messageCaseCommandName

// Cases exposes the case handlers.
type Cases struct{ c *cases }

func NewCases(services *quack.Services) Cases { return Cases{newCases(services)} }

func (c Cases) Command(ctx context.Context, i *discordgo.InteractionCreate) Result {
	return c.c.command(ctx, i)
}

func (c Cases) MessageCommand(ctx context.Context, i *discordgo.InteractionCreate) Result {
	return c.c.messageCommand(ctx, i)
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

// AssertGolden fails t unless got matches the golden file at path.
func AssertGolden(t *testing.T, path, got string) {
	t.Helper()
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("%s differs.\ngot:\n%s", path, got)
	}
}
