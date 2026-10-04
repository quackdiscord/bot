package modules_test

import (
	"context"
	"testing"

	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/testutil"
)

func TestRegistryKeepsGuildsAndModulesIndependent(t *testing.T) {
	registry := modules.NewRegistry(testutil.NewSQLiteDB(t))
	ctx := context.Background()
	if _, err := registry.SetConfiguration(ctx, modules.Configuration{GuildID: "guild-a", ModuleID: modules.Tickets, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.SetConfiguration(ctx, modules.Configuration{GuildID: "guild-a", ModuleID: modules.GeneralLogging}); err != nil {
		t.Fatal(err)
	}
	if got, err := registry.Configuration(ctx, "guild-a", modules.Tickets); err != nil || got == nil || !got.Enabled {
		t.Fatalf("tickets: %+v, %v", got, err)
	}
	if got, err := registry.Configuration(ctx, "guild-a", modules.GeneralLogging); err != nil || got == nil || got.Enabled {
		t.Fatalf("logging: %+v, %v", got, err)
	}
	if got, err := registry.Configuration(ctx, "guild-b", modules.Tickets); err != nil || got != nil {
		t.Fatalf("guild leak: %+v, %v", got, err)
	}
}

func TestSettingsRoundTripOverDefaults(t *testing.T) {
	type settings struct {
		Channel string `json:"channel"`
		Limit   int    `json:"limit"`
	}
	registry := modules.NewRegistry(testutil.NewSQLiteDB(t))
	ctx := context.Background()
	defaults := settings{Limit: 3}
	got, enabled, err := modules.LoadSettings(ctx, registry, "guild-a", modules.Tickets, defaults)
	if err != nil || enabled || got != defaults {
		t.Fatalf("unconfigured = %+v, %v, %v; want defaults, off", got, enabled, err)
	}
	if _, err := registry.SetConfiguration(ctx, modules.Configuration{
		GuildID: "guild-a", ModuleID: modules.Tickets, Enabled: true, ConfigJSON: `{"channel":"entry"}`,
	}); err != nil {
		t.Fatal(err)
	}
	got, enabled, err = modules.LoadSettings(ctx, registry, "guild-a", modules.Tickets, defaults)
	if want := (settings{Channel: "entry", Limit: 3}); err != nil || !enabled || got != want {
		t.Fatalf("partial settings = %+v, %v, %v; want %+v, on", got, enabled, err, want)
	}
	saved := settings{Channel: "other", Limit: 5}
	if _, err := registry.SaveSettings(ctx, "guild-a", modules.Tickets, false, saved); err != nil {
		t.Fatal(err)
	}
	got, enabled, err = modules.LoadSettings(ctx, registry, "guild-a", modules.Tickets, defaults)
	if err != nil || enabled || got != saved {
		t.Fatalf("saved settings = %+v, %v, %v; want %+v, off", got, enabled, err, saved)
	}
}

func TestModuleStatesKeepSettings(t *testing.T) {
	registry := modules.NewRegistry(testutil.NewSQLiteDB(t))
	ctx := context.Background()
	if _, err := registry.SetConfiguration(ctx, modules.Configuration{
		GuildID: "guild-a", ModuleID: modules.Honeypots, ConfigJSON: `{"channel_discord_id":"trap"}`,
	}); err != nil {
		t.Fatal(err)
	}
	want := quack.ModuleStates{Tickets: true, Honeypot: true}
	if err := registry.SetModuleStates(ctx, "guild-a", want); err != nil {
		t.Fatal(err)
	}
	if got, err := registry.ModuleStates(ctx, "guild-a"); err != nil || got != want {
		t.Fatalf("states = %+v, %v; want %+v", got, err, want)
	}
	honeypot, err := registry.Configuration(ctx, "guild-a", modules.Honeypots)
	if err != nil || honeypot.ConfigJSON != `{"channel_discord_id":"trap"}` {
		t.Fatalf("toggle lost honeypot settings: %+v, %v", honeypot, err)
	}
	if logging, err := registry.Configuration(ctx, "guild-a", modules.GeneralLogging); err != nil || logging != nil {
		t.Fatalf("turning nothing on created a logging row: %+v, %v", logging, err)
	}
	if got, err := registry.ModuleStates(ctx, "guild-b"); err != nil || got != (quack.ModuleStates{}) {
		t.Fatalf("guild leak: %+v, %v", got, err)
	}
	if enabled, err := registry.AnyEnabled(ctx, modules.Honeypots); err != nil || !enabled {
		t.Fatalf("AnyEnabled = %v, %v", enabled, err)
	}
}
