package quack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Evidence capture limits. They keep one case from storing an unbounded
// amount of Discord content.
const (
	maxEvidenceContentRunes     = 4000
	maxEvidenceEmbeds           = 10
	maxEvidenceAttachments      = 10
	maxEvidenceMessages         = 10
	maxEvidenceTotalAttachments = 20
	// MaxPreservedAttachmentBytes is the largest attachment copied into the
	// managed evidence channel.
	MaxPreservedAttachmentBytes int64 = 25 << 20
)

var discordMessageLinkPattern = regexp.MustCompile(`^/channels/([0-9]{2,32})/([0-9]{2,32})/([0-9]{2,32})$`)

// EvidenceClient reads messages from Discord and manages the per-guild
// evidence channel where attachments are copied so they outlive the
// original message.
type EvidenceClient interface {
	FetchMessageEvidence(context.Context, DiscordMessageReference) (*DiscordMessageSnapshot, error)
	PreserveEvidenceAttachment(ctx context.Context, guildID, channelID string, attachment DiscordAttachmentSnapshot) (*PreservedDiscordAttachment, error)
	// EnsureEvidenceChannel returns the evidence channel, creating it if
	// currentChannelID is empty or gone.
	EnsureEvidenceChannel(ctx context.Context, discordGuildID, currentChannelID string) (string, error)
}

// DiscordMessageReference identifies a linked message and who asked to
// capture it.
type DiscordMessageReference struct {
	GuildID, ChannelID, MessageID, URL string
	ActorDiscordUserID                 string
	// SystemCapture is set for cases Quack opens itself, which have no
	// actor whose channel access could be checked. Request input never sets
	// it.
	SystemCapture bool
}

// DiscordAttachmentSnapshot is an attachment on a fetched message.
type DiscordAttachmentSnapshot struct {
	ID, Filename, ContentType, URL string
	SizeBytes                      int64
}

// DiscordMessageSnapshot is a fetched message.
type DiscordMessageSnapshot struct {
	GuildID, ChannelID, MessageID, AuthorDiscordUserID, URL, Content string
	CreatedAt                                                        time.Time
	EditedAt                                                         *time.Time
	Embeds                                                           []map[string]any
	Attachments                                                      []DiscordAttachmentSnapshot
}

// PreservedDiscordAttachment is an attachment's copy in the evidence
// channel.
type PreservedDiscordAttachment struct{ URL, MessageID, AttachmentID string }

// CapturedEvidence is the evidence for a case, ready to store with it.
type CapturedEvidence struct {
	Snapshots   []CaseEvidenceSnapshot
	Attachments []CaseEvidenceAttachment
	// Warnings lists anything that was truncated or could not be copied.
	Warnings []string
}

// EvidenceService captures linked Discord messages as case evidence and
// maintains each guild's evidence channel.
type EvidenceService struct {
	store  EvidenceStore
	client EvidenceClient
}

// NewEvidenceService returns an EvidenceService. Without client, every
// capture fails with ErrEvidenceValidation.
func NewEvidenceService(store EvidenceStore, client EvidenceClient) *EvidenceService {
	return &EvidenceService{store: store, client: client}
}

// EnsureGuildEvidenceChannel makes sure the guild has an evidence channel
// and records it in settings if it changed.
func (s *EvidenceService) EnsureGuildEvidenceChannel(ctx context.Context, guild Guild, settings GuildSettings) (string, error) {
	if s.client == nil || s.store == nil {
		return "", errors.New("evidence service is not configured")
	}
	channelID, err := s.client.EnsureEvidenceChannel(ctx, guild.DiscordGuildID, settings.ManagedEvidenceChannelDiscordID)
	if err != nil {
		return "", err
	}
	if channelID == settings.ManagedEvidenceChannelDiscordID {
		return channelID, nil
	}
	settings.ManagedEvidenceChannelDiscordID = channelID
	_, err = s.store.UpdateGuildSettings(ctx, UpdateGuildSettingsParams{
		Settings: settings,
		Audit: &AuditLogEntry{
			GuildID:      guild.ID,
			Source:       AuditSourceSystem,
			Action:       "evidence_channel.ensure",
			ResourceType: "guild_settings",
			ResourceID:   settings.ID,
			Result:       AuditResultSuccess,
			MetadataJSON: "{}",
		},
	})
	return channelID, err
}

// RepairDiscordGuildEvidenceChannel re-checks a guild's evidence channel
// after Discord reports channel changes.
func (s *EvidenceService) RepairDiscordGuildEvidenceChannel(ctx context.Context, discordGuildID string) (string, error) {
	if s.store == nil {
		return "", errors.New("evidence service is not configured")
	}
	guild, err := s.store.GetGuildByDiscordID(ctx, discordGuildID)
	if err != nil || guild == nil {
		return "", err
	}
	settings, err := s.store.GetGuildSettings(ctx, guild.ID)
	if err != nil || settings == nil {
		return "", err
	}
	return s.EnsureGuildEvidenceChannel(ctx, *guild, *settings)
}

// ParseDiscordMessageLink parses an https Discord message link. Only
// discord.com and its ptb, canary, and www hosts are accepted.
func ParseDiscordMessageLink(raw string) (DiscordMessageReference, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" {
		return DiscordMessageReference{}, fmt.Errorf("%w: invalid Discord message link", ErrEvidenceValidation)
	}
	switch parsed.Host {
	case "discord.com", "www.discord.com", "ptb.discord.com", "canary.discord.com":
	default:
		return DiscordMessageReference{}, fmt.Errorf("%w: invalid Discord message link", ErrEvidenceValidation)
	}
	match := discordMessageLinkPattern.FindStringSubmatch(parsed.EscapedPath())
	if len(match) != 4 {
		return DiscordMessageReference{}, fmt.Errorf("%w: invalid Discord message link path", ErrEvidenceValidation)
	}
	return DiscordMessageReference{GuildID: match[1], ChannelID: match[2], MessageID: match[3], URL: parsed.String()}, nil
}

// Capture snapshots each linked message and copies supported attachments
// into evidenceChannelID. Messages must be in guildID and, when
// targetDiscordUserID is set, written by the target. With allowUnavailable,
// a deleted or inaccessible message is recorded as unavailable instead of
// failing the capture.
func (s *EvidenceService) Capture(ctx context.Context, guildID, actorDiscordUserID, targetDiscordUserID, evidenceChannelID string, links []string, allowUnavailable bool) (*CapturedEvidence, error) {
	if actorDiscordUserID == "" && len(links) > 0 {
		return nil, fmt.Errorf("%w: evidence actor is required", ErrEvidenceValidation)
	}
	return s.capture(ctx, guildID, actorDiscordUserID, targetDiscordUserID, evidenceChannelID, links, allowUnavailable)
}

// capture is Capture without the actor check, so system cases can capture
// without an actor.
func (s *EvidenceService) capture(ctx context.Context, guildID, actorDiscordUserID, targetDiscordUserID, evidenceChannelID string, links []string, allowUnavailable bool) (*CapturedEvidence, error) {
	if len(links) > maxEvidenceMessages {
		return nil, fmt.Errorf("%w: at most %d message links can be captured", ErrEvidenceValidation, maxEvidenceMessages)
	}
	result := &CapturedEvidence{}
	seen := map[string]bool{}
	totalAttachments := 0
	for _, raw := range links {
		ref, err := ParseDiscordMessageLink(raw)
		if err != nil {
			return nil, err
		}
		if ref.GuildID != guildID {
			return nil, fmt.Errorf("%w: message belongs to another guild", ErrEvidenceValidation)
		}
		if seen[ref.MessageID] {
			continue
		}
		seen[ref.MessageID] = true
		if s.client == nil {
			return nil, fmt.Errorf("%w: Discord evidence capture is unavailable", ErrEvidenceValidation)
		}
		ref.ActorDiscordUserID = actorDiscordUserID
		ref.SystemCapture = actorDiscordUserID == ""
		message, err := s.client.FetchMessageEvidence(ctx, ref)
		if err != nil {
			var unavailable *EvidenceUnavailableError
			if !allowUnavailable || !errors.As(err, &unavailable) {
				return nil, err
			}
			outcome, warning := "unavailable", "linked message could not be captured; moderator supplied other visible context"
			if unavailable != nil {
				outcome, warning = unavailable.Outcome, unavailable.Error()
			}
			result.Snapshots = append(result.Snapshots, CaseEvidenceSnapshot{
				ULIDModel:        ULIDModel{ID: NewID()},
				GuildID:          guildID,
				ChannelDiscordID: ref.ChannelID,
				MessageDiscordID: ref.MessageID,
				MessageURL:       ref.URL,
				MessageCreatedAt: time.Now().UTC(),
				CaptureOutcome:   outcome,
				CaptureWarning:   warning,
				EmbedsJSON:       "[]",
			})
			result.Warnings = append(result.Warnings, warning)
			continue
		}
		if message == nil || message.GuildID != guildID || message.MessageID != ref.MessageID || message.ChannelID != ref.ChannelID {
			return nil, fmt.Errorf("%w: Discord returned mismatched message identity", ErrEvidenceValidation)
		}
		if strings.TrimSpace(targetDiscordUserID) != "" && message.AuthorDiscordUserID != targetDiscordUserID {
			return nil, fmt.Errorf("%w: captured message author does not match case target", ErrEvidenceValidation)
		}

		var warnings []string
		warn := func(warning string) {
			result.Warnings = append(result.Warnings, warning)
			warnings = append(warnings, warning)
		}
		if len([]rune(message.Content)) > maxEvidenceContentRunes {
			warn("message content snapshot was truncated")
		}
		embeds := message.Embeds
		if len(embeds) > maxEvidenceEmbeds {
			embeds = embeds[:maxEvidenceEmbeds]
			warn("embed snapshot was truncated")
		}
		embedJSON, _ := json.Marshal(embeds)
		evidenceID := NewID()
		snapshotIndex := len(result.Snapshots)
		result.Snapshots = append(result.Snapshots, CaseEvidenceSnapshot{
			ULIDModel:           ULIDModel{ID: evidenceID},
			GuildID:             guildID,
			ChannelDiscordID:    message.ChannelID,
			MessageDiscordID:    message.MessageID,
			AuthorDiscordUserID: message.AuthorDiscordUserID,
			MessageURL:          message.URL,
			Content:             truncateRunes(message.Content, maxEvidenceContentRunes),
			MessageCreatedAt:    message.CreatedAt,
			MessageEditedAt:     message.EditedAt,
			EmbedsJSON:          string(embedJSON),
			CaptureOutcome:      "captured",
		})

		attachments := message.Attachments
		if len(attachments) > maxEvidenceAttachments {
			attachments = attachments[:maxEvidenceAttachments]
			warn("attachment snapshot was truncated")
		}
		remaining := maxEvidenceTotalAttachments - totalAttachments
		if remaining <= 0 {
			attachments = nil
			warn("total attachment snapshot limit reached")
		} else if len(attachments) > remaining {
			attachments = attachments[:remaining]
			warn("total attachment snapshot was truncated")
		}
		totalAttachments += len(attachments)
		for _, attachment := range attachments {
			record := s.preserve(ctx, guildID, evidenceChannelID, evidenceID, attachment)
			if record.Warning != "" {
				warn(record.Warning)
			}
			result.Attachments = append(result.Attachments, record)
		}
		result.Snapshots[snapshotIndex].CaptureWarning = strings.Join(warnings, "; ")
	}
	if len(result.Warnings) > 0 {
		slog.WarnContext(ctx, "Evidence capture incomplete", "discord_guild_id", guildID,
			"messages", len(result.Snapshots), "attachments", len(result.Attachments), "warnings", len(result.Warnings))
	}
	return result, nil
}

// preserve copies an attachment into the evidence channel when it is small
// enough and of a displayable type. Otherwise, or if copying fails, only its
// metadata is kept and Warning says why.
func (s *EvidenceService) preserve(ctx context.Context, guildID, evidenceChannelID, evidenceID string, attachment DiscordAttachmentSnapshot) CaseEvidenceAttachment {
	record := CaseEvidenceAttachment{
		EvidenceID:  evidenceID,
		Filename:    truncateRunes(attachment.Filename, 255),
		ContentType: truncateRunes(attachment.ContentType, 191),
		SizeBytes:   attachment.SizeBytes,
		OriginalURL: attachment.URL,
		CopyOutcome: "metadata_only",
	}
	switch {
	case evidenceChannelID == "":
		record.Warning = "managed evidence channel is unavailable"
	case attachment.SizeBytes < 0 || attachment.SizeBytes > MaxPreservedAttachmentBytes:
		record.Warning = "attachment exceeds the managed copy size limit"
	case !supportedEvidenceContentType(attachment.ContentType):
		record.Warning = "attachment type is not eligible for managed copying"
	default:
		preserved, err := s.client.PreserveEvidenceAttachment(ctx, guildID, evidenceChannelID, attachment)
		switch {
		case err != nil:
			record.Warning = "attachment copy failed; original metadata retained"
		case preserved != nil && preserved.URL != "" && preserved.AttachmentID != "" && preserved.MessageID != "":
			record.CopyOutcome = "preserved"
			record.PreservedURL = preserved.URL
			record.PreservedMessageDiscordID = preserved.MessageID
			record.PreservedAttachmentDiscordID = preserved.AttachmentID
		default:
			record.Warning = "attachment copy could not be confirmed; original metadata retained"
		}
	}
	return record
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return value
}

// supportedEvidenceContentType reports whether an attachment is a type staff
// can view in Discord: images, video, audio, plain text, and PDF.
func supportedEvidenceContentType(value string) bool {
	value = strings.ToLower(strings.TrimSpace(strings.Split(value, ";")[0]))
	return strings.HasPrefix(value, "image/") || strings.HasPrefix(value, "video/") ||
		strings.HasPrefix(value, "audio/") || value == "text/plain" || value == "application/pdf"
}

// CaseEvidenceResponse is a captured message.
type CaseEvidenceResponse struct {
	ID                  string                           `json:"id"`
	AuthorDiscordUserID string                           `json:"author_discord_user_id"`
	MessageURL          string                           `json:"message_url"`
	Content             string                           `json:"content"`
	CaptureOutcome      string                           `json:"capture_outcome"`
	CaptureWarning      string                           `json:"capture_warning,omitempty"`
	MessageCreatedAt    time.Time                        `json:"message_created_at"`
	MessageEditedAt     *time.Time                       `json:"message_edited_at,omitempty"`
	Embeds              any                              `json:"embeds"`
	Attachments         []CaseEvidenceAttachmentResponse `json:"attachments"`
}

// CaseEvidenceAttachmentResponse is an attachment on a captured message. The
// managed evidence channel's identity is never exposed.
type CaseEvidenceAttachmentResponse struct {
	Filename     string `json:"filename"`
	ContentType  string `json:"content_type"`
	OriginalURL  string `json:"original_url"`
	PreservedURL string `json:"preserved_url,omitempty"`
	CopyOutcome  string `json:"copy_outcome"`
	Warning      string `json:"warning,omitempty"`
	SizeBytes    int64  `json:"size_bytes"`
}

// caseEvidenceResponses builds evidence responses. Members see the preserved
// copy of an attachment instead of its original URL when one exists.
func caseEvidenceResponses(snapshots []CaseEvidenceSnapshot, attachments []CaseEvidenceAttachment, member bool) []CaseEvidenceResponse {
	byEvidence := map[string][]CaseEvidenceAttachmentResponse{}
	for _, item := range attachments {
		original := item.OriginalURL
		if member && item.PreservedURL != "" {
			original = ""
		}
		byEvidence[item.EvidenceID] = append(byEvidence[item.EvidenceID], CaseEvidenceAttachmentResponse{
			Filename:     item.Filename,
			ContentType:  item.ContentType,
			SizeBytes:    item.SizeBytes,
			OriginalURL:  original,
			PreservedURL: item.PreservedURL,
			CopyOutcome:  item.CopyOutcome,
			Warning:      item.Warning,
		})
	}
	out := make([]CaseEvidenceResponse, 0, len(snapshots))
	for _, item := range snapshots {
		out = append(out, CaseEvidenceResponse{
			ID:                  item.ID,
			AuthorDiscordUserID: item.AuthorDiscordUserID,
			MessageURL:          item.MessageURL,
			Content:             item.Content,
			MessageCreatedAt:    item.MessageCreatedAt,
			MessageEditedAt:     item.MessageEditedAt,
			Embeds:              parseJSON(item.EmbedsJSON),
			CaptureOutcome:      item.CaptureOutcome,
			CaptureWarning:      item.CaptureWarning,
			Attachments:         byEvidence[item.ID],
		})
	}
	return out
}

// CaseEvidenceSnapshot is a copy of a linked Discord message taken before the
// case was committed, so later edits or deletions don't change the record.
type CaseEvidenceSnapshot struct {
	ULIDModel
	CaseID              string
	GuildID             string
	ChannelDiscordID    string
	MessageDiscordID    string
	AuthorDiscordUserID string
	MessageURL          string
	Content             string
	MessageCreatedAt    time.Time
	MessageEditedAt     *time.Time
	EmbedsJSON          string
	CaptureOutcome      string
	CaptureWarning      string
}

// CaseEvidenceAttachment records an attachment on an evidence message and,
// when copying succeeded, its stable copy in the managed evidence channel.
type CaseEvidenceAttachment struct {
	ULIDModel
	EvidenceID                   string
	Filename                     string
	ContentType                  string
	SizeBytes                    int64
	OriginalURL                  string
	PreservedURL                 string
	PreservedMessageDiscordID    string
	PreservedAttachmentDiscordID string
	CopyOutcome                  string
	Warning                      string
}
