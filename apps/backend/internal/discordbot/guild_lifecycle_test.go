package discordbot_test

import (
	"context"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/discordbot"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/testutil"
)

func TestGuildLifecycleHandlerCreateUpdateDeleteChannelLeaveAndRejoin(t *testing.T) {
	ctx := context.Background()
	repositories := testutil.NewSQLiteStore(t)
	if err := repositories.Migrate(); err != nil {
		t.Fatalf("migrate store: %v", err)
	}
	handler := &discordbot.GuildLifecycleHandler{Guilds: quack.NewGuildService(repositories, nil)}
	handler.HandleGuildCreate(nil, &discordgo.GuildCreate{Guild: &discordgo.Guild{
		ID: "discord-guild", Name: "Initial", Icon: "icon-one", OwnerID: "owner-1",
		Channels: []*discordgo.Channel{{ID: "audit-channel", GuildID: "discord-guild"}, {ID: "evidence-channel", GuildID: "discord-guild"}},
	}})
	guild, err := repositories.GetGuildByDiscordID(ctx, "discord-guild")
	if err != nil || guild == nil || !guild.IsActive {
		t.Fatalf("guild create did not bootstrap active guild: guild=%+v err=%v", guild, err)
	}
	settings, err := repositories.GetGuildSettings(ctx, guild.ID)
	if err != nil || settings == nil || settings.StarterPolicyTemplateID == "" {
		t.Fatalf("guild create did not create settings/starter: settings=%+v err=%v", settings, err)
	}
	starterID := settings.StarterPolicyTemplateID
	settings.AuditMirrorChannelDiscordID = "audit-channel"
	settings.ManagedEvidenceChannelDiscordID = "evidence-channel"
	if _, err := repositories.UpdateGuildSettings(ctx, quack.UpdateGuildSettingsParams{Settings: *settings}); err != nil {
		t.Fatalf("configure lifecycle channels: %v", err)
	}

	handler.HandleGuildUpdate(nil, &discordgo.GuildUpdate{Guild: &discordgo.Guild{
		ID: "discord-guild", Name: "Renamed", Icon: "icon-two", OwnerID: "owner-2",
	}})
	refreshed, err := repositories.GetGuildByDiscordID(ctx, "discord-guild")
	if err != nil || refreshed.Name != "Renamed" || refreshed.OwnerDiscordUserID != "owner-2" || !refreshed.IsActive {
		t.Fatalf("guild update did not refresh metadata: guild=%+v err=%v", refreshed, err)
	}
	settings, _ = repositories.GetGuildSettings(ctx, guild.ID)
	if settings.AuditMirrorChannelDiscordID != "audit-channel" || settings.ManagedEvidenceChannelDiscordID != "evidence-channel" {
		t.Fatalf("partial guild update incorrectly cleared channels: %+v", settings)
	}

	handler.HandleChannelDelete(nil, &discordgo.ChannelDelete{Channel: &discordgo.Channel{ID: "evidence-channel", GuildID: "discord-guild"}})
	settings, _ = repositories.GetGuildSettings(ctx, guild.ID)
	if settings.ManagedEvidenceChannelDiscordID != "" || settings.AuditMirrorChannelDiscordID != "audit-channel" {
		t.Fatalf("channel delete did not narrowly clear matching reference: %+v", settings)
	}

	handler.HandleGuildDelete(nil, &discordgo.GuildDelete{Guild: &discordgo.Guild{ID: "discord-guild", Unavailable: true}})
	stillActive, _ := repositories.GetGuildByDiscordID(ctx, "discord-guild")
	if !stillActive.IsActive {
		t.Fatal("temporary Discord unavailability marked guild inactive")
	}
	handler.HandleGuildDelete(nil, &discordgo.GuildDelete{Guild: &discordgo.Guild{ID: "discord-guild"}})
	departed, _ := repositories.GetGuildByDiscordID(ctx, "discord-guild")
	if departed.IsActive {
		t.Fatal("true guild leave did not mark guild inactive")
	}

	handler.HandleGuildCreate(nil, &discordgo.GuildCreate{Guild: &discordgo.Guild{
		ID: "discord-guild", Name: "Rejoined", OwnerID: "owner-3",
		Channels: []*discordgo.Channel{{ID: "new-channel", GuildID: "discord-guild"}},
	}})
	rejoined, _ := repositories.GetGuildByDiscordID(ctx, "discord-guild")
	settings, _ = repositories.GetGuildSettings(ctx, guild.ID)
	if !rejoined.IsActive || rejoined.ID != guild.ID || settings.StarterPolicyTemplateID != starterID || settings.AuditMirrorChannelDiscordID != "" {
		t.Fatalf("rejoin did not preserve identity/starter and repair stale channel: guild=%+v settings=%+v", rejoined, settings)
	}
	var templateCount int64
	if err := repositories.DB().Table("case_templates").Where("guild_id = ?", guild.ID).Count(&templateCount).Error; err != nil || templateCount != 1 {
		t.Fatalf("rejoin duplicated starter template: count=%d err=%v", templateCount, err)
	}
}
