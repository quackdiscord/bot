package discord

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

func TestLaunchAnnouncementFitsAndLinksTheDocs(t *testing.T) {
	message := launchAnnouncement(quack.NewDashboardLinks("https://quack.bot"), "123")
	if n := len([]rune(message.Content)); n > contentLimit {
		t.Fatalf("announcement is %d characters, over Discord's %d", n, contentLimit)
	}
	for _, removed := range []string{"/warn", "/timeout", "/kick", "/ban"} {
		if !strings.Contains(message.Content, "`"+removed+"`") {
			t.Errorf("%s is not in a code span, so it could render as a live command", removed)
		}
	}
	row := message.Components[0].(discordgo.ActionsRow)
	docs, dashboard := row.Components[0].(discordgo.Button), row.Components[1].(discordgo.Button)
	if docs.URL != "https://quack.bot/docs/getting-started" || dashboard.URL != "https://quack.bot/guilds/123" {
		t.Fatalf("buttons link %q and %q", docs.URL, dashboard.URL)
	}
	if bare := launchAnnouncement(quack.DashboardLinks{}, "123"); len(bare.Components) != 0 {
		t.Fatalf("no dashboard still added buttons: %+v", bare.Components)
	}
}

// announcementBot answers guild lookups with publicUpdates and system
// channels, refuses posts to the channels in denied, and records where each
// message went.
func announcementBot(t *testing.T, denied map[string]bool, posted *[]string) *Bot {
	return testBot(t, func(r *http.Request) (*http.Response, error) {
		path := r.URL.Path
		switch {
		case r.Method == http.MethodGet && strings.HasSuffix(path, "/guilds/g1"):
			return jsonResponse(r, discordgo.Guild{ID: "g1", PublicUpdatesChannelID: "updates", SystemChannelID: "system"}), nil
		case r.Method == http.MethodPost && strings.HasSuffix(path, "/users/@me/channels"):
			return jsonResponse(r, discordgo.Channel{ID: "dm"}), nil
		case r.Method == http.MethodPost && strings.HasPrefix(path, "/api/v9/channels/") && strings.HasSuffix(path, "/messages"):
			channel := strings.Split(path, "/")[4]
			if denied[channel] {
				return textResponse(r, http.StatusForbidden, `{"message":"Missing Access","code":50001}`), nil
			}
			*posted = append(*posted, channel)
			return jsonResponse(r, discordgo.Message{ID: "m", ChannelID: channel}), nil
		}
		return textResponse(r, http.StatusNotFound, `{}`), nil
	})
}

func TestSendLaunchAnnouncementFallsBackInOrder(t *testing.T) {
	target := quack.LaunchAnnouncementTarget{DiscordGuildID: "g1", OwnerDiscordUserID: "owner", AuditMirrorChannelDiscordID: "audit"}
	tests := []struct {
		name   string
		denied []string
		want   string
	}{
		{"audit channel first", nil, "audit"},
		{"then community updates", []string{"audit"}, "updates"},
		{"then system messages", []string{"audit", "updates"}, "system"},
		{"then the owner", []string{"audit", "updates", "system"}, "dm"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			denied := map[string]bool{}
			for _, id := range test.denied {
				denied[id] = true
			}
			var posted []string
			bot := announcementBot(t, denied, &posted)
			if err := bot.SendLaunchAnnouncement(context.Background(), target); err != nil {
				t.Fatal(err)
			}
			if len(posted) != 1 || posted[0] != test.want {
				t.Fatalf("posted to %v, want only %s", posted, test.want)
			}
		})
	}
}

func TestSendLaunchAnnouncementReportsTotalFailure(t *testing.T) {
	var posted []string
	bot := announcementBot(t, map[string]bool{"audit": true, "updates": true, "system": true, "dm": true}, &posted)
	target := quack.LaunchAnnouncementTarget{DiscordGuildID: "g1", OwnerDiscordUserID: "owner", AuditMirrorChannelDiscordID: "audit"}
	if err := bot.SendLaunchAnnouncement(context.Background(), target); err == nil || len(posted) != 0 {
		t.Fatalf("err = %v, posted %v; want an error and nothing posted", err, posted)
	}
}
