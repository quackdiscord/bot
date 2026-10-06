package discord

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

func TestEvidenceDownloadRejectsUnsafeSourcesBeforeUpload(t *testing.T) {
	calls := 0
	bot := testBot(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("an upload was attempted")
		return nil, nil
	})
	bot.httpClient = &http.Client{Transport: roundTripper(func(request *http.Request) (*http.Response, error) {
		calls++
		return textResponse(request, http.StatusBadGateway, "unavailable"), nil
	})}
	ctx := context.Background()
	preserve := func(url string) error {
		_, err := bot.PreserveEvidenceAttachment(ctx, "guild", "channel", quack.DiscordAttachmentSnapshot{URL: url, SizeBytes: 3})
		return err
	}
	if err := preserve("https://cdn.discordapp.com/attachments/1/2/file.png"); err == nil || calls != 1 {
		t.Fatalf("failed download was accepted: calls=%d err=%v", calls, err)
	}
	for _, raw := range []string{
		"http://cdn.discordapp.com/attachments/1/2/x",
		"https://localhost/attachments/1/2/x",
		"https://cdn.discordapp.com.evil.test/attachments/x",
		"https://user:secret@cdn.discordapp.com/attachments/x",
	} {
		if err := preserve(raw); err == nil || calls != 1 {
			t.Fatalf("unsafe download %s: calls=%d err=%v", raw, calls, err)
		}
	}
}

func TestEvidenceChecksModeratorAccessBeforeBotRead(t *testing.T) {
	allowed, reads := false, 0
	guild := &discordgo.Guild{ID: "guild", OwnerID: "owner", Roles: []*discordgo.Role{
		{ID: "guild", Permissions: discordgo.PermissionViewChannel | discordgo.PermissionReadMessageHistory},
		{ID: "staff", Permissions: discordgo.PermissionModerateMembers},
	}}
	bot := testBot(t, func(request *http.Request) (*http.Response, error) {
		var body any
		switch path := request.URL.Path; {
		case strings.HasSuffix(path, "/channels/channel"):
			channel := &discordgo.Channel{ID: "channel", GuildID: "guild", Type: discordgo.ChannelTypeGuildText}
			if !allowed {
				channel.PermissionOverwrites = []*discordgo.PermissionOverwrite{{
					ID:   "moderator",
					Type: discordgo.PermissionOverwriteTypeMember,
					Deny: discordgo.PermissionViewChannel,
				}}
			}
			body = channel
		case strings.HasSuffix(path, "/guilds/guild"):
			body = guild
		case strings.HasSuffix(path, "/members/moderator"):
			body = &discordgo.Member{User: &discordgo.User{ID: "moderator"}, Roles: []string{"staff"}}
		case strings.HasSuffix(path, "/messages/message"):
			reads++
			body = &discordgo.Message{ID: "message", ChannelID: "channel", GuildID: "guild", Author: &discordgo.User{ID: "target"}}
		default:
			t.Fatalf("unexpected request %s", path)
		}
		return jsonResponse(request, body), nil
	})
	// Permissive gateway data must not grant access.
	_ = bot.Session.State.GuildAdd(&discordgo.Guild{ID: "guild", OwnerID: "moderator"})
	ref := quack.DiscordMessageReference{GuildID: "guild", ChannelID: "channel", MessageID: "message", ActorDiscordUserID: "moderator"}
	if _, err := bot.FetchMessageEvidence(context.Background(), ref); err == nil || reads != 0 {
		t.Fatal("bot read inaccessible evidence")
	}
	allowed = true
	if _, err := bot.FetchMessageEvidence(context.Background(), ref); err != nil || reads != 1 {
		t.Fatalf("readable evidence failed: %v", err)
	}
}

// TestEvidenceCopyKeepsConvertedFileAndReturnsJumpLink accepts a download
// whose size differs from the metadata, as Discord's converted images do,
// and links the copy by its message, which outlives signed CDN URLs.
func TestEvidenceCopyKeepsConvertedFileAndReturnsJumpLink(t *testing.T) {
	var uploaded string
	bot := testBot(t, func(request *http.Request) (*http.Response, error) {
		switch path := request.URL.Path; {
		case strings.HasSuffix(path, "/channels/evidence") && request.Method == http.MethodGet:
			return jsonResponse(request, &discordgo.Channel{ID: "evidence", GuildID: "guild", Type: discordgo.ChannelTypeGuildText,
				PermissionOverwrites: []*discordgo.PermissionOverwrite{{ID: "guild", Type: discordgo.PermissionOverwriteTypeRole, Deny: discordgo.PermissionViewChannel}}}), nil
		case strings.HasSuffix(path, "/guilds/guild"):
			return jsonResponse(request, &discordgo.Guild{ID: "guild", Roles: []*discordgo.Role{{ID: "guild"}}}), nil
		case strings.HasSuffix(path, "/channels/evidence/messages"):
			body, _ := io.ReadAll(request.Body)
			uploaded = string(body)
			return jsonResponse(request, &discordgo.Message{ID: "copy", Attachments: []*discordgo.MessageAttachment{{ID: "file", URL: "https://cdn.discordapp.com/attachments/signed"}}}), nil
		}
		t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		return nil, nil
	})
	bot.httpClient = &http.Client{Transport: roundTripper(func(request *http.Request) (*http.Response, error) {
		return textResponse(request, http.StatusOK, "converted bytes"), nil
	})}
	copied, err := bot.PreserveEvidenceAttachment(context.Background(), "guild", "evidence",
		quack.DiscordAttachmentSnapshot{URL: "https://cdn.discordapp.com/attachments/1/2/a.png", Filename: "a.png", SizeBytes: 3})
	if err != nil {
		t.Fatal(err)
	}
	if copied.URL != "https://discord.com/channels/guild/evidence/copy" || copied.MessageID != "copy" {
		t.Fatalf("copy = %+v", copied)
	}
	if !strings.Contains(uploaded, `filename="SPOILER_a.png"`) {
		t.Fatalf("evidence copy was not uploaded as a spoiler: %q", uploaded)
	}
}

func TestSpoilerName(t *testing.T) {
	for in, want := range map[string]string{
		"a.png":         "SPOILER_a.png",
		"SPOILER_a.png": "SPOILER_a.png",
		"spoiler_a.png": "spoiler_a.png",
		"":              "SPOILER_evidence",
	} {
		if got := spoilerName(in); got != want {
			t.Errorf("spoilerName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestEvidenceChannelLeavesExistingAndOpensNewToStaff keeps an existing
// channel as administrators set it up, and creates a missing one readable
// by staff roles only.
func TestEvidenceChannelLeavesExistingAndOpensNewToStaff(t *testing.T) {
	var created discordgo.GuildChannelCreateData
	bot := testBot(t, func(request *http.Request) (*http.Response, error) {
		switch path := request.URL.Path; {
		case strings.HasSuffix(path, "/channels/existing"):
			if request.Method != http.MethodGet {
				t.Fatal("existing evidence channel was modified")
			}
			return jsonResponse(request, &discordgo.Channel{ID: "existing", GuildID: "guild"}), nil
		case strings.HasSuffix(path, "/channels/gone"):
			return textResponse(request, http.StatusNotFound, `{"code":10003}`), nil
		case strings.HasSuffix(path, "/guilds/guild/roles"):
			return jsonResponse(request, []*discordgo.Role{
				{ID: "guild"}, {ID: "mods", Permissions: discordgo.PermissionModerateMembers}, {ID: "managers", Permissions: discordgo.PermissionManageGuild},
			}), nil
		case strings.HasSuffix(path, "/guilds/guild/channels"):
			body, _ := io.ReadAll(request.Body)
			_ = json.Unmarshal(body, &created)
			return jsonResponse(request, &discordgo.Channel{ID: "new", GuildID: "guild"}), nil
		case strings.HasSuffix(path, "/channels/new/messages"):
			return jsonResponse(request, &discordgo.Message{ID: "intro"}), nil
		}
		t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		return nil, nil
	})
	ctx := context.Background()
	if id, err := bot.EnsureEvidenceChannel(ctx, "guild", "existing"); err != nil || id != "existing" {
		t.Fatalf("existing channel = %q, %v", id, err)
	}
	if id, err := bot.EnsureEvidenceChannel(ctx, "guild", "gone"); err != nil || id != "new" {
		t.Fatalf("new channel = %q, %v", id, err)
	}
	var readers []string
	for _, overwrite := range created.PermissionOverwrites {
		if overwrite.Allow&discordgo.PermissionViewChannel != 0 {
			readers = append(readers, overwrite.ID)
		}
	}
	if strings.Join(readers, ",") != "bot,mods" {
		t.Fatalf("new channel readers = %v, want the bot and staff roles", readers)
	}
}
