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
	allowed, reads, author := false, 0, "target"
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
			body = &discordgo.Message{ID: "message", ChannelID: "channel", GuildID: "guild", Author: &discordgo.User{ID: author}}
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
	if message, err := bot.FetchMessageEvidence(context.Background(), ref); err != nil || reads != 1 || message.FromQuack {
		t.Fatalf("readable evidence = %+v, %v", message, err)
	}
	author = "bot"
	if message, err := bot.FetchMessageEvidence(context.Background(), ref); err != nil || !message.FromQuack {
		t.Fatalf("Quack's own post was not marked: %+v, %v", message, err)
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
// TestRefreshAttachmentURLAsksDiscordToResign checks that an expired
// original link is sent to Discord's refresh endpoint and that only a
// Discord CDN link is accepted back.
func TestRefreshAttachmentURLAsksDiscordToResign(t *testing.T) {
	const original = "https://cdn.discordapp.com/attachments/1/2/proof.png?ex=old"
	reply := "https://cdn.discordapp.com/attachments/1/2/proof.png?ex=new"
	bot := testBot(t, func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || !strings.HasSuffix(request.URL.Path, "/attachments/refresh-urls") {
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
		var body struct {
			AttachmentURLs []string `json:"attachment_urls"`
		}
		raw, _ := io.ReadAll(request.Body)
		if err := json.Unmarshal(raw, &body); err != nil || len(body.AttachmentURLs) != 1 || body.AttachmentURLs[0] != original {
			t.Fatalf("refresh body = %s", raw)
		}
		return jsonResponse(request, map[string]any{"refreshed_urls": []map[string]string{{"original": original, "refreshed": reply}}}), nil
	})
	ctx := context.Background()
	if got, err := bot.RefreshAttachmentURL(ctx, original); err != nil || got != reply {
		t.Fatalf("refreshed = %q, %v", got, err)
	}
	reply = "https://evil.example/proof.png"
	if _, err := bot.RefreshAttachmentURL(ctx, original); err == nil {
		t.Fatal("accepted a refreshed link outside Discord's CDN")
	}
	if _, err := bot.RefreshAttachmentURL(ctx, "https://example.com/file.png"); err == nil {
		t.Fatal("refreshed a link that is not a Discord attachment")
	}
}
