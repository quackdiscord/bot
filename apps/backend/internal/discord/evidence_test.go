package discord

import (
	"context"
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
		return textResponse(request, http.StatusOK, "too many bytes"), nil
	})}
	ctx := context.Background()
	preserve := func(url string) error {
		_, err := bot.PreserveEvidenceAttachment(ctx, "guild", "channel", quack.DiscordAttachmentSnapshot{URL: url, SizeBytes: 3})
		return err
	}
	if err := preserve("https://cdn.discordapp.com/attachments/1/2/file.png"); err == nil || calls != 1 {
		t.Fatalf("size mismatch was accepted: calls=%d err=%v", calls, err)
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
