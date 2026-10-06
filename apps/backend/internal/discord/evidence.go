package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
)

// FetchMessageEvidence reads a linked message for a case. Unless the capture
// is system-initiated, the moderator must currently be able to read the
// message themselves; the bot never reads on their behalf otherwise.
func (b *Bot) FetchMessageEvidence(ctx context.Context, ref quack.DiscordMessageReference) (*quack.DiscordMessageSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := b.authorizeEvidenceRead(ctx, ref); err != nil {
		return nil, err
	}
	message, err := b.Session.ChannelMessage(ref.ChannelID, ref.MessageID, rest(ctx)...)
	if err != nil {
		switch statusCode(err) {
		case http.StatusNotFound:
			return nil, &quack.EvidenceUnavailableError{Outcome: "deleted", Message: "linked message was deleted or does not exist"}
		case http.StatusForbidden:
			return nil, &quack.EvidenceUnavailableError{Outcome: "inaccessible", Message: "Quack cannot access the linked message"}
		}
		return nil, classify("evidence_fetch", err, false)
	}
	if message == nil || message.Author == nil {
		return nil, &quack.EvidenceUnavailableError{Outcome: "unavailable", Message: "linked message is unavailable"}
	}
	if message.GuildID == "" {
		channel, err := b.Session.Channel(ref.ChannelID, rest(ctx)...)
		if err != nil {
			return nil, classify("evidence_channel", err, false)
		}
		message.GuildID = channel.GuildID
	}
	embeds := make([]map[string]any, 0, len(message.Embeds))
	for _, embed := range message.Embeds {
		body, _ := json.Marshal(embed)
		var value map[string]any
		_ = json.Unmarshal(body, &value)
		embeds = append(embeds, value)
	}
	attachments := make([]quack.DiscordAttachmentSnapshot, 0, len(message.Attachments))
	for _, item := range message.Attachments {
		attachments = append(attachments, quack.DiscordAttachmentSnapshot{
			ID: item.ID, Filename: item.Filename, ContentType: item.ContentType,
			SizeBytes: int64(item.Size), URL: item.URL,
		})
	}
	return &quack.DiscordMessageSnapshot{
		GuildID: message.GuildID, ChannelID: message.ChannelID, MessageID: message.ID,
		AuthorDiscordUserID: message.Author.ID, URL: ref.URL, Content: message.Content,
		CreatedAt: message.Timestamp, EditedAt: message.EditedTimestamp,
		Embeds: embeds, Attachments: attachments,
	}, nil
}

// authorizeEvidenceRead checks, from fresh REST data only, that the moderator
// can read the linked channel, including membership of a private thread.
func (b *Bot) authorizeEvidenceRead(ctx context.Context, ref quack.DiscordMessageReference) error {
	channel, err := b.Session.Channel(ref.ChannelID, rest(ctx)...)
	if err != nil || channel.GuildID != ref.GuildID {
		return quack.ErrEvidenceValidation
	}
	if ref.SystemCapture {
		return nil
	}
	if ref.ActorDiscordUserID == "" {
		return quack.ErrEvidenceValidation
	}
	// Threads take their permissions from the parent channel.
	permissionChannel := channel
	if channel.IsThread() {
		permissionChannel, err = b.Session.Channel(channel.ParentID, rest(ctx)...)
		if err != nil || permissionChannel.GuildID != ref.GuildID {
			return quack.ErrAuthorizationUnavailable
		}
	}
	guild, err := b.Session.Guild(ref.GuildID, rest(ctx)...)
	if err != nil {
		return quack.ErrAuthorizationUnavailable
	}
	member, err := b.Session.GuildMember(ref.GuildID, ref.ActorDiscordUserID, rest(ctx)...)
	if err != nil {
		return quack.ErrAuthorizationUnavailable
	}
	permissions, err := channelPermissions(guild, permissionChannel, member)
	required := int64(discordgo.PermissionViewChannel | discordgo.PermissionReadMessageHistory)
	if err != nil || permissions&required != required {
		return errors.New("moderator cannot read the evidence channel")
	}
	if channel.Type == discordgo.ChannelTypeGuildPrivateThread && permissions&discordgo.PermissionManageThreads == 0 {
		if _, err := b.Session.ThreadMember(channel.ID, ref.ActorDiscordUserID, false, rest(ctx)...); err != nil {
			return errors.New("moderator cannot read the evidence thread")
		}
	}
	return nil
}

// channelPermissions computes member's permissions in channel with
// discordgo's overwrite rules, using a throwaway state that holds only the
// given REST results so the gateway cache never influences the answer.
func channelPermissions(guild *discordgo.Guild, channel *discordgo.Channel, member *discordgo.Member) (int64, error) {
	state := discordgo.NewState()
	scoped := *guild
	scoped.Channels = []*discordgo.Channel{channel}
	scoped.Members = []*discordgo.Member{member}
	if err := state.GuildAdd(&scoped); err != nil {
		return 0, err
	}
	return state.UserChannelPermissions(member.User.ID, channel.ID)
}

// PreserveEvidenceAttachment copies one attachment from Discord's CDN into
// the guild's staff-only evidence channel, so the evidence survives the
// original message being deleted.
func (b *Bot) PreserveEvidenceAttachment(
	ctx context.Context, guildID, channelID string, item quack.DiscordAttachmentSnapshot,
) (*quack.PreservedDiscordAttachment, error) {
	if item.SizeBytes < 0 || item.SizeBytes > quack.MaxPreservedAttachmentBytes || !attachmentURL(item.URL) {
		return nil, errors.New("attachment is not eligible for managed copying")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, item.URL, nil)
	if err != nil {
		return nil, err
	}
	// Copy the client so the redirect restriction does not leak into the
	// shared OAuth client. Redirects may never leave Discord's CDN.
	client := *b.httpClient
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !attachmentURL(request.URL.String()) {
			return errors.New("attachment redirect is not allowed")
		}
		return nil
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, classify("evidence_download", err, false)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, quack.DiscordError{
			Code:      "evidence_download_failed",
			Message:   "attachment download failed",
			Retryable: response.StatusCode >= 500,
		}
	}
	// Discord may serve a converted image whose size differs from the
	// message metadata, so the download is bounded on its own and the bytes
	// Discord returned are kept as they are.
	content, err := io.ReadAll(io.LimitReader(response.Body, quack.MaxPreservedAttachmentBytes+1))
	if err != nil {
		return nil, errors.New("attachment download could not be read")
	}
	if int64(len(content)) > quack.MaxPreservedAttachmentBytes {
		return nil, errors.New("attachment download exceeds the managed copy size limit")
	}
	if err := b.ValidateStaffChannel(ctx, guildID, channelID); err != nil {
		return nil, err
	}
	sent, err := b.Session.ChannelFileSend(channelID, spoilerName(item.Filename), bytes.NewReader(content), rest(ctx)...)
	if err != nil {
		return nil, classify("evidence_upload", err, false)
	}
	if sent == nil || len(sent.Attachments) == 0 || sent.Attachments[0] == nil || sent.Attachments[0].URL == "" {
		return nil, errors.New("discord did not confirm an attachment copy")
	}
	// The jump link outlives the signed CDN URL, which Discord expires.
	return &quack.PreservedDiscordAttachment{
		MessageID:    sent.ID,
		AttachmentID: sent.Attachments[0].ID,
		URL:          messageLink(guildID, channelID, sent.ID),
	}, nil
}

// EvidenceAttachmentURL returns a freshly signed CDN URL for an attachment
// in an evidence channel message. Reading the message makes Discord sign
// its attachment URLs again.
func (b *Bot) EvidenceAttachmentURL(ctx context.Context, channelID, messageID, attachmentID string) (string, error) {
	message, err := b.Session.ChannelMessage(channelID, messageID, rest(ctx)...)
	if err != nil {
		return "", classify("evidence_copy_read", err, false)
	}
	for _, attachment := range message.Attachments {
		if attachment != nil && attachment.ID == attachmentID && attachmentURL(attachment.URL) {
			return attachment.URL, nil
		}
	}
	return "", errors.New("evidence copy is no longer attached")
}

// RefreshAttachmentURL asks Discord to re-sign an expired attachment URL,
// for evidence Quack did not copy. It fails once the attachment is gone.
func (b *Bot) RefreshAttachmentURL(ctx context.Context, original string) (string, error) {
	if !attachmentURL(original) {
		return "", errors.New("not a Discord attachment URL")
	}
	endpoint := discordgo.EndpointAPI + "attachments/refresh-urls"
	body, err := b.Session.RequestWithBucketID(http.MethodPost, endpoint,
		map[string][]string{"attachment_urls": {original}}, endpoint, rest(ctx)...)
	if err != nil {
		return "", classify("evidence_refresh", err, false)
	}
	var response struct {
		RefreshedURLs []struct {
			Refreshed string `json:"refreshed"`
		} `json:"refreshed_urls"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return "", err
	}
	if len(response.RefreshedURLs) != 1 || !attachmentURL(response.RefreshedURLs[0].Refreshed) {
		return "", errors.New("discord did not refresh the attachment URL")
	}
	return response.RefreshedURLs[0].Refreshed, nil
}

// spoilerName marks an evidence copy as a spoiler, so Discord blurs images
// and video in the evidence channel until a moderator chooses to look.
func spoilerName(filename string) string {
	if filename == "" {
		filename = "evidence"
	}
	if strings.HasPrefix(strings.ToUpper(filename), "SPOILER_") {
		return filename
	}
	return "SPOILER_" + filename
}

// attachmentURL reports whether raw points at Discord's attachment CDN.
func attachmentURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" {
		return false
	}
	if parsed.Host != "cdn.discordapp.com" && parsed.Host != "media.discordapp.net" {
		return false
	}
	return strings.HasPrefix(parsed.Path, "/attachments/") || strings.HasPrefix(parsed.Path, "/ephemeral-attachments/")
}
