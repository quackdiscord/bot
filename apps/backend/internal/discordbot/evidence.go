package discordbot

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

// FetchMessageEvidence fetches a live message and maps it into the bounded core capture contract.
func (b *Bot) FetchMessageEvidence(ctx context.Context, ref quack.DiscordMessageReference) (*quack.DiscordMessageSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := b.authorizeEvidenceSource(ctx, ref); err != nil {
		return nil, err
	}
	message, err := b.Session.ChannelMessage(ref.ChannelID, ref.MessageID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil {
		var restErr *discordgo.RESTError
		if errors.As(err, &restErr) && restErr.Response != nil {
			if restErr.Response.StatusCode == http.StatusNotFound {
				return nil, &quack.EvidenceUnavailableError{Outcome: "deleted", Message: "linked message was deleted or does not exist"}
			}
			if restErr.Response.StatusCode == http.StatusForbidden {
				return nil, &quack.EvidenceUnavailableError{Outcome: "inaccessible", Message: "Quack cannot access the linked message"}
			}
		}
		return nil, classifyDiscordOperation("evidence_fetch", err, false)
	}
	if message == nil || message.Author == nil {
		return nil, &quack.EvidenceUnavailableError{Outcome: "unavailable", Message: "linked message is unavailable"}
	}
	if message.GuildID == "" {
		channel, channelErr := b.Session.Channel(ref.ChannelID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
		if channelErr != nil {
			return nil, classifyDiscordOperation("evidence_channel", channelErr, false)
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
		attachments = append(attachments, quack.DiscordAttachmentSnapshot{ID: item.ID, Filename: item.Filename, ContentType: item.ContentType, SizeBytes: int64(item.Size), URL: item.URL})
	}
	return &quack.DiscordMessageSnapshot{GuildID: message.GuildID, ChannelID: message.ChannelID, MessageID: message.ID, AuthorDiscordUserID: message.Author.ID, URL: ref.URL, Content: message.Content, CreatedAt: message.Timestamp, EditedAt: message.EditedTimestamp, Embeds: embeds, Attachments: attachments}, nil
}

// PreserveEvidenceAttachment copies supported bytes into the guild's managed staff-only evidence channel.
func (b *Bot) PreserveEvidenceAttachment(ctx context.Context, guildID, channelID string, item quack.DiscordAttachmentSnapshot) (*quack.PreservedDiscordAttachment, error) {
	if item.SizeBytes < 0 || item.SizeBytes > quack.MaxPreservedAttachmentBytes || !discordAttachmentURL(item.URL) {
		return nil, errors.New("attachment is not eligible for managed copying")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, item.URL, nil)
	if err != nil {
		return nil, err
	}
	client := b.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	// Copy the client before restricting redirects; the shared OAuth client
	// must retain its own policy. Attachment redirects never leave Discord CDN.
	downloadClient := *client
	downloadClient.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !discordAttachmentURL(request.URL.String()) {
			return errors.New("attachment redirect is not allowed")
		}
		return nil
	}
	response, err := downloadClient.Do(request)
	if err != nil {
		return nil, classifyDiscordOperation("evidence_download", err, false)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, quack.DiscordError{Code: "evidence_download_failed", Message: "attachment download failed", Retryable: response.StatusCode >= 500}
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, item.SizeBytes+1))
	if err != nil || int64(len(content)) != item.SizeBytes {
		return nil, errors.New("attachment download size did not match its metadata")
	}
	if err := b.ValidateStaffChannel(ctx, guildID, channelID); err != nil {
		return nil, err
	}
	sent, err := b.Session.ChannelFileSend(channelID, item.Filename, bytes.NewReader(content), discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil {
		return nil, classifyDiscordOperation("evidence_upload", err, false)
	}
	if sent == nil || len(sent.Attachments) == 0 || sent.Attachments[0] == nil || sent.Attachments[0].URL == "" {
		return nil, errors.New("Discord did not confirm an attachment copy")
	}
	return &quack.PreservedDiscordAttachment{MessageID: sent.ID, AttachmentID: sent.Attachments[0].ID, URL: sent.Attachments[0].URL}, nil
}

// discordAttachmentURL restricts managed downloads to Discord attachment CDNs.
func discordAttachmentURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" {
		return false
	}
	if parsed.Host != "cdn.discordapp.com" && parsed.Host != "media.discordapp.net" {
		return false
	}
	return strings.HasPrefix(parsed.Path, "/attachments/") || strings.HasPrefix(parsed.Path, "/ephemeral-attachments/")
}

// EnsureEvidenceChannel creates or repairs a staff-only evidence channel owned by Quack.
func (b *Bot) EnsureEvidenceChannel(ctx context.Context, guildID, currentChannelID string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	botID := ""
	if b.Session.State != nil && b.Session.State.User != nil {
		botID = b.Session.State.User.ID
	}
	if botID == "" {
		user, err := b.Session.User("@me", discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
		if err != nil || user == nil {
			return "", errors.New("Discord bot identity is unavailable")
		}
		botID = user.ID
	}
	overwrites := []*discordgo.PermissionOverwrite{{ID: guildID, Type: discordgo.PermissionOverwriteTypeRole, Deny: discordgo.PermissionViewChannel}, {ID: botID, Type: discordgo.PermissionOverwriteTypeMember, Allow: discordgo.PermissionViewChannel | discordgo.PermissionSendMessages | discordgo.PermissionAttachFiles | discordgo.PermissionReadMessageHistory}}
	if currentChannelID != "" {
		channel, err := b.Session.Channel(currentChannelID, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
		if err != nil {
			var restErr *discordgo.RESTError
			if !errors.As(err, &restErr) || restErr.Response == nil || restErr.Response.StatusCode != http.StatusNotFound {
				return "", classifyDiscordOperation("evidence_channel_lookup", err, false)
			}
		}
		if err == nil && channel != nil && channel.GuildID == guildID {
			_, editErr := b.Session.ChannelEditComplex(channel.ID, &discordgo.ChannelEdit{Name: "quack-evidence", Topic: "Quack-managed immutable moderation evidence", PermissionOverwrites: overwrites}, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
			if editErr != nil {
				return "", classifyDiscordOperation("evidence_channel_repair", editErr, false)
			}
			return channel.ID, nil
		}
	}
	created, err := b.Session.GuildChannelCreateComplex(guildID, discordgo.GuildChannelCreateData{Name: "quack-evidence", Type: discordgo.ChannelTypeGuildText, Topic: "Quack-managed immutable moderation evidence", PermissionOverwrites: overwrites}, discordgo.WithContext(ctx), discordgo.WithRestRetries(0), discordgo.WithRetryOnRatelimit(false))
	if err != nil {
		return "", classifyDiscordOperation("evidence_channel_create", err, false)
	}
	return created.ID, nil
}
