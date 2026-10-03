package discord_test

import (
	"context"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discord"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/testutil"
)

func TestLifecycleCreateUpdateDeleteChannelLeaveAndRejoin(t *testing.T) {
	ctx := context.Background()
	repository := testutil.NewSQLiteStore(t)
	if err := repository.Migrate(); err != nil {
		t.Fatalf("migrate store: %v", err)
	}
	l := discord.NewLifecycle(quack.NewGuildService(repository, nil), quack.NewEvidenceService(repository, nil))

	l.GuildCreate(&discordgo.GuildCreate{Guild: &discordgo.Guild{
		ID: "discord-guild", Name: "Initial", Icon: "icon-one", OwnerID: "owner-1",
		Channels: []*discordgo.Channel{{ID: "audit-channel", GuildID: "discord-guild"}, {ID: "evidence-channel", GuildID: "discord-guild"}},
	}})
	guild, err := repository.GetGuildByDiscordID(ctx, "discord-guild")
	if err != nil || guild == nil || !guild.IsActive {
		t.Fatalf("guild create did not bootstrap active guild: guild=%+v err=%v", guild, err)
	}
	settings, err := repository.GetGuildSettings(ctx, guild.ID)
	if err != nil || settings == nil || settings.StarterPolicyTemplateID == "" {
		t.Fatalf("guild create did not create settings/starter: settings=%+v err=%v", settings, err)
	}
	starterID := settings.StarterPolicyTemplateID
	settings.AuditMirrorChannelDiscordID = "audit-channel"
	settings.ManagedEvidenceChannelDiscordID = "evidence-channel"
	if _, err := repository.UpdateGuildSettings(ctx, quack.UpdateGuildSettingsParams{Settings: *settings}); err != nil {
		t.Fatalf("configure lifecycle channels: %v", err)
	}

	l.GuildUpdate(&discordgo.GuildUpdate{Guild: &discordgo.Guild{ID: "discord-guild", Name: "Renamed", Icon: "icon-two", OwnerID: "owner-2"}})
	refreshed, err := repository.GetGuildByDiscordID(ctx, "discord-guild")
	if err != nil || refreshed.Name != "Renamed" || refreshed.OwnerDiscordUserID != "owner-2" || !refreshed.IsActive {
		t.Fatalf("guild update did not refresh metadata: guild=%+v err=%v", refreshed, err)
	}
	settings, _ = repository.GetGuildSettings(ctx, guild.ID)
	if settings.AuditMirrorChannelDiscordID != "audit-channel" || settings.ManagedEvidenceChannelDiscordID != "evidence-channel" {
		t.Fatalf("partial guild update cleared channels: %+v", settings)
	}

	l.ChannelDelete(&discordgo.ChannelDelete{Channel: &discordgo.Channel{ID: "evidence-channel", GuildID: "discord-guild"}})
	settings, _ = repository.GetGuildSettings(ctx, guild.ID)
	if settings.ManagedEvidenceChannelDiscordID != "" || settings.AuditMirrorChannelDiscordID != "audit-channel" {
		t.Fatalf("channel delete did not narrowly clear matching reference: %+v", settings)
	}

	l.GuildDelete(&discordgo.GuildDelete{Guild: &discordgo.Guild{ID: "discord-guild", Unavailable: true}})
	if stillActive, _ := repository.GetGuildByDiscordID(ctx, "discord-guild"); !stillActive.IsActive {
		t.Fatal("temporary Discord unavailability marked guild inactive")
	}
	l.GuildDelete(&discordgo.GuildDelete{Guild: &discordgo.Guild{ID: "discord-guild"}})
	if departed, _ := repository.GetGuildByDiscordID(ctx, "discord-guild"); departed.IsActive {
		t.Fatal("true guild leave did not mark guild inactive")
	}

	l.GuildCreate(&discordgo.GuildCreate{Guild: &discordgo.Guild{
		ID: "discord-guild", Name: "Rejoined", OwnerID: "owner-3",
		Channels: []*discordgo.Channel{{ID: "new-channel", GuildID: "discord-guild"}},
	}})
	rejoined, _ := repository.GetGuildByDiscordID(ctx, "discord-guild")
	settings, _ = repository.GetGuildSettings(ctx, guild.ID)
	if !rejoined.IsActive || rejoined.ID != guild.ID || settings.StarterPolicyTemplateID != starterID || settings.AuditMirrorChannelDiscordID != "" {
		t.Fatalf("rejoin did not preserve identity/starter and repair stale channel: guild=%+v settings=%+v", rejoined, settings)
	}
	var templates int64
	if err := repository.DB().Table("case_templates").Where("guild_id = ?", guild.ID).Count(&templates).Error; err != nil || templates != 1 {
		t.Fatalf("rejoin duplicated starter template: count=%d err=%v", templates, err)
	}
}
