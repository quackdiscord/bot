package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/testutil"
)

func TestLaunchAnnouncementClaimsEachActiveGuildOnce(t *testing.T) {
	ctx := context.Background()
	s := testutil.NewSQLiteStore(t)
	var ids []string
	for _, discordID := range []string{"one", "two", "gone"} {
		result, err := s.BootstrapGuild(ctx, quack.BootstrapGuildParams{
			Starter: quack.StarterTemplate(), DiscordGuildID: discordID, Name: discordID, OwnerDiscordUserID: "owner-" + discordID,
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, result.Guild.ID)
	}
	if _, err := s.DeactivateGuild(ctx, "gone", nil); err != nil {
		t.Fatal(err)
	}

	targets, err := s.ListLaunchAnnouncementTargets(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 || targets[0].GuildID != ids[0] || targets[0].DiscordGuildID != "one" ||
		targets[0].OwnerDiscordUserID != "owner-one" || targets[1].GuildID != ids[1] {
		t.Fatalf("targets = %+v; want the two active guilds in install order", targets)
	}

	now := time.Now()
	if claimed, err := s.ClaimLaunchAnnouncement(ctx, ids[0], now); err != nil || !claimed {
		t.Fatalf("first claim = %v, %v; want true", claimed, err)
	}
	if claimed, err := s.ClaimLaunchAnnouncement(ctx, ids[0], now); err != nil || claimed {
		t.Fatalf("second claim = %v, %v; want false", claimed, err)
	}
	if targets, _ := s.ListLaunchAnnouncementTargets(ctx, 10); len(targets) != 1 || targets[0].GuildID != ids[1] {
		t.Fatalf("targets after claim = %+v; want only the second guild", targets)
	}

	if err := s.ReleaseLaunchAnnouncement(ctx, ids[0]); err != nil {
		t.Fatal(err)
	}
	if targets, _ := s.ListLaunchAnnouncementTargets(ctx, 1); len(targets) != 1 || targets[0].GuildID != ids[0] {
		t.Fatalf("targets after release = %+v; want the first guild back, limited to 1", targets)
	}
}

func TestSettingsUpdateKeepsLaunchAnnouncementClaim(t *testing.T) {
	ctx := context.Background()
	s := testutil.NewSQLiteStore(t)
	result, err := s.BootstrapGuild(ctx, quack.BootstrapGuildParams{
		Starter: quack.StarterTemplate(), DiscordGuildID: "guild", Name: "Guild", OwnerDiscordUserID: "owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimLaunchAnnouncement(ctx, result.Guild.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	settings := result.Settings
	settings.NotificationFooter = "Be kind"
	if _, err := s.UpdateGuildSettings(ctx, quack.UpdateGuildSettingsParams{Settings: settings}); err != nil {
		t.Fatal(err)
	}
	if targets, _ := s.ListLaunchAnnouncementTargets(ctx, 10); len(targets) != 0 {
		t.Fatalf("a settings update cleared the claim: %+v", targets)
	}
}
