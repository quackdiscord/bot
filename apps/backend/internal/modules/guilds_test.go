package modules_test

import (
	"context"
	"testing"

	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/testutil"
)

func TestGuildsResolveOnlyActiveGuilds(t *testing.T) {
	ctx := context.Background()
	store := testutil.NewSQLiteStore(t)
	guild, err := store.UpsertGuild(ctx, quack.UpsertGuildParams{
		DiscordGuildID: "discord-guild", Name: "Guild", OwnerDiscordUserID: "owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	guilds := modules.NewGuilds(store)
	if id, err := guilds.InternalID(ctx, "discord-guild"); err != nil || id != guild.ID {
		t.Fatalf("InternalID = %q, %v; want %q", id, err, guild.ID)
	}
	if id, err := guilds.DiscordID(ctx, guild.ID); err != nil || id != "discord-guild" {
		t.Fatalf("DiscordID = %q, %v; want discord-guild", id, err)
	}
	if _, err := store.DeactivateGuild(ctx, "discord-guild", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := guilds.InternalID(ctx, "discord-guild"); err == nil {
		t.Fatal("InternalID resolved a departed guild")
	}
	if id, err := guilds.InternalIDAny(ctx, "discord-guild"); err != nil || id != guild.ID {
		t.Fatalf("InternalIDAny = %q, %v; want %q", id, err, guild.ID)
	}
	if _, err := guilds.InternalIDAny(ctx, "unknown"); err == nil {
		t.Fatal("InternalIDAny resolved an unknown guild")
	}
}
