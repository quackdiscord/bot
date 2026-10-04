package discord

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

func TestSidebarOrder(t *testing.T) {
	channels := []*discordgo.Channel{
		{ID: "30", Name: "voice-b", Type: discordgo.ChannelTypeGuildVoice, ParentID: "20", Position: 0},
		{ID: "20", Name: "Staff", Type: discordgo.ChannelTypeGuildCategory, Position: 2},
		{ID: "10", Name: "General", Type: discordgo.ChannelTypeGuildCategory, Position: 1},
		{ID: "31", Name: "mod-log", Type: discordgo.ChannelTypeGuildText, ParentID: "20", Position: 5},
		{ID: "32", Name: "appeals", Type: discordgo.ChannelTypeGuildForum, ParentID: "20", Position: 1},
		{ID: "11", Name: "chat", Type: discordgo.ChannelTypeGuildText, ParentID: "10", Position: 0},
		{ID: "1", Name: "rules", Type: discordgo.ChannelTypeGuildText, Position: 3},
		{ID: "2", Name: "news", Type: discordgo.ChannelTypeGuildNews, Position: 0},
		{ID: "3", Name: "orphan", Type: discordgo.ChannelTypeGuildText, ParentID: "999", Position: 9},
		{ID: "40", Name: "a-thread", Type: discordgo.ChannelTypeGuildPublicThread, ParentID: "11"},
		{ID: "41", Name: "stage", Type: discordgo.ChannelTypeGuildStageVoice, ParentID: "10", Position: 0},
		// Same position: the older (shorter) snowflake comes first.
		{ID: "100", Name: "later", Type: discordgo.ChannelTypeGuildText, ParentID: "10", Position: 0},
		nil,
	}
	var names []string
	for _, channel := range sidebarOrder(channels) {
		names = append(names, channel.Name+":"+channel.Type)
	}
	want := []string{
		"news:announcement", "rules:text", "orphan:text",
		"General:category", "chat:text", "later:text", "stage:stage",
		"Staff:category", "appeals:forum", "mod-log:text", "voice-b:voice",
	}
	if !slices.Equal(names, want) {
		t.Errorf("order = %v\nwant    %v", names, want)
	}
}

func TestDirectoryUserNamesAndAvatars(t *testing.T) {
	user := &discordgo.User{ID: "80351110224678912", Username: "duck", GlobalName: "Duck"}
	member := memberDirectoryUser("guild", &discordgo.Member{User: user, Nick: "Quacker", Avatar: "a_hash"})
	if member.DisplayName != "Quacker" || member.Nick != "Quacker" || !member.InGuild {
		t.Errorf("member = %+v", member)
	}
	if member.AvatarURL != "https://cdn.discordapp.com/guilds/guild/users/80351110224678912/avatars/a_hash.gif" {
		t.Errorf("guild avatar = %s", member.AvatarURL)
	}

	noNick := memberDirectoryUser("guild", &discordgo.Member{User: user})
	if noNick.DisplayName != "Duck" || noNick.Nick != "" {
		t.Errorf("member without nick = %+v", noNick)
	}
	plain := plainDirectoryUser(&discordgo.User{ID: "1", Username: "duck", Avatar: "hash", Bot: true})
	if plain.DisplayName != "duck" || plain.InGuild || !plain.Bot ||
		plain.AvatarURL != "https://cdn.discordapp.com/avatars/1/hash.png" {
		t.Errorf("plain user = %+v", plain)
	}
	// The index is (id >> 22) % 6: the ID's timestamp in milliseconds.
	for id, want := range map[string]int{
		"80351110224678912": 5, // 19157197529 ms
		"80351110228869120": 0, // 19157197530 ms
		"80351110233063424": 1, // 19157197531 ms
		"not-a-snowflake":   0,
	} {
		if got := defaultAvatarIndex(id); got != want {
			t.Errorf("defaultAvatarIndex(%s) = %d, want %d", id, got, want)
		}
	}
	if noNick.AvatarURL != "https://cdn.discordapp.com/embed/avatars/5.png" {
		t.Errorf("default avatar = %s", noNick.AvatarURL)
	}
}

func TestTTLCacheExpiresAndStaysBounded(t *testing.T) {
	var cache ttlCache[int]
	now := time.Now()
	cache.put("a", 1, now, time.Minute, 2)
	if v, ok := cache.get("a", now.Add(59*time.Second)); !ok || v != 1 {
		t.Fatalf("live entry = %d, %v", v, ok)
	}
	if _, ok := cache.get("a", now.Add(time.Minute)); ok {
		t.Fatal("expired entry was returned")
	}
	cache.put("b", 2, now, time.Minute, 2)
	cache.put("c", 3, now, time.Minute, 2)
	if len(cache.entries) != 2 {
		t.Fatalf("cache holds %d entries, want at most 2", len(cache.entries))
	}
	if v, ok := cache.get("c", now); !ok || v != 3 {
		t.Fatalf("newest entry = %d, %v", v, ok)
	}
}

// TestLookupUsersFallsBackAndCaches checks the member, user, and unknown
// paths, the order of results, and that a second lookup hits the cache.
func TestLookupUsersFallsBackAndCaches(t *testing.T) {
	var calls atomic.Int32
	bot := testBot(t, func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		path := request.URL.Path
		switch {
		case strings.HasSuffix(path, "/members/1"):
			return jsonResponse(request, discordgo.Member{User: &discordgo.User{ID: "1", Username: "member"}, Nick: "Nick"}), nil
		case strings.Contains(path, "/members/"):
			return textResponse(request, http.StatusNotFound, `{"code":10007,"message":"Unknown Member"}`), nil
		case strings.HasSuffix(path, "/users/2"):
			return jsonResponse(request, discordgo.User{ID: "2", Username: "left"}), nil
		default:
			return textResponse(request, http.StatusNotFound, `{"code":10013,"message":"Unknown User"}`), nil
		}
	})
	for range 2 {
		users, err := bot.LookupUsers(context.Background(), "guild", []string{"3", "2", "1"})
		if err != nil {
			t.Fatal(err)
		}
		if len(users) != 2 || users[0].ID != "2" || users[0].InGuild || users[1].DisplayName != "Nick" || !users[1].InGuild {
			t.Fatalf("users = %+v", users)
		}
	}
	// 1: member. 2: member miss, then user. 3: member miss, user miss.
	if n := calls.Load(); n != 5 {
		t.Errorf("Discord saw %d requests, want 5 with the second lookup cached", n)
	}
}

func TestLookupUsersReportsDiscordFailure(t *testing.T) {
	bot := testBot(t, func(request *http.Request) (*http.Response, error) {
		return textResponse(request, http.StatusInternalServerError, `{}`), nil
	})
	if _, err := bot.LookupUsers(context.Background(), "guild", []string{"1", "2"}); err == nil {
		t.Fatal("a Discord outage was not reported")
	}
}
