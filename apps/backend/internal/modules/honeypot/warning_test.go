package honeypot_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/modules"
	"github.com/quackdiscord/bot/internal/modules/honeypot"
)

// warningDiscord is a fake Discord for the warning post: the trap channel,
// and edits and posts answered by the test.
type warningDiscord struct {
	mu            sync.Mutex
	edits, posts  int
	latest        string
	editStatus    int
	postStatus    int
	lastPostBody  string
	unexpectedHit string
}

func (d *warningDiscord) serve(r *http.Request) (int, any) {
	d.mu.Lock()
	defer d.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/channels/trap"):
		return 200, `{"id":"trap","guild_id":"guild","type":0}`
	case r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/channels/trap/messages/old"):
		d.edits++
		body, _ := io.ReadAll(r.Body)
		d.latest = string(body)
		switch d.editStatus {
		case 0, 200:
			return 200, `{"id":"old"}`
		case 404:
			return 404, `{"code":10008,"message":"Unknown Message"}`
		default:
			return d.editStatus, `{"code":0,"message":"Server error"}`
		}
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/channels/trap/messages"):
		d.posts++
		body, _ := io.ReadAll(r.Body)
		d.lastPostBody = string(body)
		if d.postStatus != 0 {
			return d.postStatus, `{}`
		}
		return 200, `{"id":"replacement"}`
	}
	d.unexpectedHit = r.Method + " " + r.URL.Path
	return 500, `{}`
}

func (d *warningDiscord) counts() (edits, posts int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.edits, d.posts
}

// warningFixture is an enabled honeypot in Discord guild "guild" with a
// warning post "old" in #trap.
func warningFixture(t *testing.T, text string) (moduleFixture, *warningDiscord, string) {
	t.Helper()
	discord := &warningDiscord{}
	f := newDiscordModule(t, discordSession(discord.serve))
	guildID := f.guild(t, "guild")
	f.set(t, guildID, honeypot.Settings{ChannelDiscordID: "trap", TemplateID: "template", WarningText: text, WarningMessageID: "old"})
	t.Cleanup(func() {
		if discord.unexpectedHit != "" {
			t.Errorf("unexpected Discord request %s", discord.unexpectedHit)
		}
	})
	return f, discord, guildID
}

// requestRefresh queues a warning refresh the way a saved case does.
func (f moduleFixture) requestRefresh(t *testing.T, guildID string) {
	t.Helper()
	service := honeypot.NewService(f.registry, honeypot.NewStore(f.store.DB()), nil, nil, nil, nil)
	if err := service.RequestWarningRefresh(context.Background(), guildID); err != nil {
		t.Fatal(err)
	}
}

// pendingRefreshes counts the guild refreshes still due within an hour.
func (f moduleFixture) pendingRefreshes(t *testing.T) int {
	t.Helper()
	service := honeypot.NewService(f.registry, honeypot.NewStore(f.store.DB()), nil, nil, nil, nil)
	rows, err := service.WarningRefreshes(context.Background(), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return len(rows)
}

// The warning is edited in place; only a deleted one is reposted, with the
// admin's text and the count, and its new ID saved. A server error posts
// nothing.
func TestWarningRefreshRepairsOnlyMissingWarnings(t *testing.T) {
	for _, status := range []int{200, 404, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			f, discord, guildID := warningFixture(t, "Custom warning")
			discord.editStatus = status
			ctx := context.Background()
			honeypot.OnMessageDelete(f.module, nil, &discordgo.MessageDelete{Message: &discordgo.Message{ID: "unrelated", ChannelID: "trap", GuildID: "guild"}})
			if f.pendingRefreshes(t) != 0 {
				t.Fatal("an unrelated deletion queued a refresh")
			}
			honeypot.OnMessageDeleteBulk(f.module, nil, &discordgo.MessageDeleteBulk{GuildID: "guild", ChannelID: "trap", Messages: []string{"another", "old"}})
			if f.pendingRefreshes(t) != 1 {
				t.Fatal("deleting the warning queued no refresh")
			}
			err := honeypot.RefreshWarning(ctx, f.module, guildID)
			_, posts := discord.counts()
			if (err == nil) != (status != 500) || posts != map[int]int{200: 0, 404: 1, 500: 0}[status] {
				t.Fatalf("posts=%d err=%v", posts, err)
			}
			want := "old"
			if status == 404 {
				want = "replacement"
				if !strings.Contains(discord.lastPostBody, "Custom warning") || !strings.Contains(discord.lastPostBody, "0 incidents caught.") {
					t.Fatalf("wrong warning posted: %s", discord.lastPostBody)
				}
			}
			saved, _ := f.settings(t, guildID)
			if saved.WarningMessageID != want || saved.WarningText != "Custom warning" {
				t.Fatalf("saved %+v, want warning %s", saved, want)
			}
		})
	}
}

// A failed edit backs off, survives a restart, and then shows the current
// wording.
func TestWarningRefreshRetriesAfterRestart(t *testing.T) {
	f, discord, guildID := warningFixture(t, "Original")
	ctx := context.Background()
	discord.editStatus = 500
	for range 5 {
		f.requestRefresh(t, guildID)
	}
	if err := honeypot.ProcessWarnings(ctx, f.module); err != nil {
		t.Fatal(err)
	}
	if edits, _ := discord.counts(); edits != 1 {
		t.Fatalf("edits = %d, want 1", edits)
	}
	if err := honeypot.ProcessWarnings(ctx, f.module); err != nil {
		t.Fatal(err)
	}
	if edits, _ := discord.counts(); edits != 1 {
		t.Fatal("no retry backoff")
	}
	f.set(t, guildID, honeypot.Settings{ChannelDiscordID: "trap", TemplateID: "template", WarningText: "Changed", WarningMessageID: "old"})
	discord.editStatus = 200
	if err := f.store.DB().Model(&honeypot.WarningRefresh{}).Where("guild_id = ?", guildID).
		Update("next_attempt_at", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	restarted := honeypot.New(f.store.DB(), f.registry, nil, modules.NewGuilds(f.store), discordSession(discord.serve), f.store, nil, f.templates)
	if err := honeypot.ProcessWarnings(ctx, restarted); err != nil {
		t.Fatal(err)
	}
	if edits, _ := discord.counts(); edits != 2 || !strings.Contains(discord.latest, "Changed") {
		t.Fatalf("edits=%d latest=%s", edits, discord.latest)
	}
	if f.pendingRefreshes(t) != 0 {
		t.Fatal("the refresh stayed due")
	}
}

// A replacement post whose response was lost is never repeated, even after
// a restart asks for a fresh refresh.
func TestWarningReplacementUncertaintySurvivesRestart(t *testing.T) {
	f, discord, guildID := warningFixture(t, "Original")
	discord.editStatus = 404
	session := discordSession(func(r *http.Request) (int, any) {
		if r.Method == http.MethodPost {
			discord.mu.Lock()
			discord.posts++
			discord.mu.Unlock()
			return -1, nil
		}
		return discord.serve(r)
	})
	module := honeypot.New(f.store.DB(), f.registry, nil, modules.NewGuilds(f.store), session, f.store, nil, f.templates)
	if err := honeypot.RefreshWarning(context.Background(), module, guildID); err == nil {
		t.Fatal("an unconfirmed post succeeded")
	}
	restarted := honeypot.New(f.store.DB(), f.registry, nil, modules.NewGuilds(f.store), session, f.store, nil, f.templates)
	f.requestRefresh(t, guildID)
	if err := honeypot.ProcessWarnings(context.Background(), restarted); err != nil {
		t.Fatal(err)
	}
	if _, posts := discord.counts(); posts != 1 {
		t.Fatalf("posts = %d, want 1", posts)
	}
}

// The first run after a restart refreshes every enabled warning without an
// incident; a disabled honeypot's queued refresh retires silently.
func TestWarningStartupAndDisabledRetirement(t *testing.T) {
	f, discord, guildID := warningFixture(t, "")
	if err := f.module.RefreshWarnings(context.Background()); err != nil {
		t.Fatal(err)
	}
	if edits, _ := discord.counts(); edits != 1 || !strings.Contains(discord.latest, "Posting here will ban you from this server.") {
		t.Fatalf("startup edits=%d latest=%s", edits, discord.latest)
	}
	if _, err := f.registry.SetConfiguration(context.Background(), modules.Configuration{
		GuildID: guildID, ModuleID: modules.Honeypots, Enabled: false, ConfigJSON: "{}",
	}); err != nil {
		t.Fatal(err)
	}
	f.requestRefresh(t, guildID)
	if err := f.module.RefreshWarnings(context.Background()); err != nil {
		t.Fatal(err)
	}
	if edits, _ := discord.counts(); edits != 1 || f.pendingRefreshes(t) != 0 {
		t.Fatalf("disabled guild: edits=%d pending=%d", edits, f.pendingRefreshes(t))
	}
}
