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

// EvidenceClient reads messages from Discord and manages the per-guild
// evidence channel where attachments are copied so they outlive the
// original message.
type EvidenceClient interface {
	FetchMessageEvidence(context.Context, DiscordMessageReference) (*DiscordMessageSnapshot, error)
	PreserveEvidenceAttachment(ctx context.Context, guildID, channelID string, attachment DiscordAttachmentSnapshot) (*PreservedDiscordAttachment, error)
	// EvidenceAttachmentURL returns a freshly signed CDN URL for an
	// attachment Quack copied into an evidence channel. Discord expires
	// these URLs, so they are fetched when needed rather than stored.
	EvidenceAttachmentURL(ctx context.Context, channelID, messageID, attachmentID string) (string, error)
	// RefreshAttachmentURL re-signs an attachment URL Quack did not copy,
	// which works only while the original attachment still exists.
	RefreshAttachmentURL(ctx context.Context, original string) (string, error)
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
	// FromQuack is set when Quack itself wrote the message, such as a log
	// post, which staff may cite as evidence against any member.
	FromQuack bool
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
	// ID names the attachment in the evidence file routes, which redirect
	// to a viewable copy.
	ID           string `json:"id"`
	Filename     string `json:"filename"`
	ContentType  string `json:"content_type"`
	OriginalURL  string `json:"original_url"`
	PreservedURL string `json:"preserved_url,omitempty"`
	CopyOutcome  string `json:"copy_outcome"`
	Warning      string `json:"warning,omitempty"`
	SizeBytes    int64  `json:"size_bytes"`
}

// EvidenceService captures linked Discord messages as case evidence and
// maintains each guild's evidence channel.
type EvidenceService struct {
	client EvidenceClient
}

// NewEvidenceService returns an EvidenceService. Without client, every
// capture fails with ErrEvidenceValidation.
func NewEvidenceService(client EvidenceClient) *EvidenceService {
	return &EvidenceService{client: client}
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
// targetDiscordUserID is set, written by the target or by Quack. An empty
// actorDiscordUserID marks a system capture, which skips the actor's
// channel access check. With allowUnavailable, a deleted or inaccessible
// message is recorded as unavailable instead of failing the capture.
func (s *EvidenceService) Capture(ctx context.Context, guildID, actorDiscordUserID, targetDiscordUserID, evidenceChannelID string, links []string, allowUnavailable bool) (*CapturedEvidence, error) {
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
			result.Snapshots = append(result.Snapshots, CaseEvidenceSnapshot{
				ULIDModel:        ULIDModel{ID: NewID()},
				GuildID:          guildID,
				ChannelDiscordID: ref.ChannelID,
				MessageDiscordID: ref.MessageID,
				MessageURL:       ref.URL,
				MessageCreatedAt: time.Now().UTC(),
				CaptureOutcome:   unavailable.Outcome,
				CaptureWarning:   unavailable.Error(),
				EmbedsJSON:       "[]",
			})
			result.Warnings = append(result.Warnings, unavailable.Error())
			continue
		}
		if message == nil || message.GuildID != guildID || message.MessageID != ref.MessageID || message.ChannelID != ref.ChannelID {
			return nil, fmt.Errorf("%w: Discord returned mismatched message identity", ErrEvidenceValidation)
		}
		if strings.TrimSpace(targetDiscordUserID) != "" && message.AuthorDiscordUserID != targetDiscordUserID && !message.FromQuack {
			return nil, fmt.Errorf("%w: linked message must be from the case target or Quack", ErrEvidenceValidation)
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
		snapshot := CaseEvidenceSnapshot{
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
			CaptureOutcome:      captureOutcomeCaptured,
		}

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
		snapshot.CaptureWarning = strings.Join(warnings, "; ")
		result.Snapshots = append(result.Snapshots, snapshot)
	}
	if len(result.Warnings) > 0 {
		slog.WarnContext(ctx, "Evidence capture incomplete", "discord_guild_id", guildID,
			"messages", len(result.Snapshots), "attachments", len(result.Attachments), "warnings", len(result.Warnings))
	}
	return result, nil
}

// captureEvidence captures a new case's linked messages and uploaded files
// into the guild's evidence channel. A message that can't be captured is
// only acceptable when the moderator gave other context the member can see.
func (s *CaseService) captureEvidence(ctx context.Context, guildContext *GuildStaffContext, targetID string, links []string, files []DiscordAttachmentSnapshot, hasOtherContext bool, attribution caseAttribution) (CapturedEvidence, error) {
	if len(links) == 0 && len(files) == 0 {
		return CapturedEvidence{}, nil
	}
	if s.evidence == nil {
		return CapturedEvidence{}, caseValidationError("evidence capture is not configured")
	}
	channelID, err := s.evidenceChannel(ctx, guildContext.Guild.ID)
	if err != nil {
		return CapturedEvidence{}, err
	}
	actorID := guildContext.ActorDiscordUserID
	if attribution.system {
		actorID = ""
	} else if actorID == "" {
		return CapturedEvidence{}, caseValidationError("evidence actor is required")
	}
	captured, err := s.evidence.Capture(ctx, guildContext.Guild.DiscordGuildID, actorID, targetID, channelID, links, hasOtherContext)
	if err != nil {
		_ = s.audit(ctx, guildContext, attribution, string(AuditActionEvidenceCapture),
			"case_evidence", "unknown", AuditResultFailure, err.Error())
		return CapturedEvidence{}, caseValidationError(err.Error())
	}
	uploads, err := s.evidence.CaptureUploads(ctx, guildContext.Guild.DiscordGuildID, actorID, channelID, files)
	if err != nil {
		return CapturedEvidence{}, err
	}
	captured.append(uploads)
	return *captured, nil
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
		// The guild has not set up an evidence channel, so nothing is
		// copied. That is a choice, not a problem worth a warning.
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

// supportedEvidenceContentType reports whether an attachment is a type staff
// can view in Discord: images, video, audio, plain text, and PDF.
func supportedEvidenceContentType(contentType string) bool {
	mediaType, _, _ := strings.Cut(contentType, ";")
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	return strings.HasPrefix(mediaType, "image/") || strings.HasPrefix(mediaType, "video/") ||
		strings.HasPrefix(mediaType, "audio/") || mediaType == "text/plain" || mediaType == "application/pdf"
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
			ID:           item.ID,
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

// truncateRunes cuts value to at most limit runes, never splitting one.
func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return value
}
