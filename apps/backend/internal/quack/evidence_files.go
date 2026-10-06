package quack

import (
	"context"
	"net/url"
	"strings"
)

// EvidenceFileURL returns a URL a staff member's browser can load for one
// of a case's evidence attachments: a freshly signed link to Quack's copy,
// or the original upload when there is no copy or it cannot be read.
func (s *CaseService) EvidenceFileURL(ctx context.Context, guildContext *GuildStaffContext, caseRef, attachmentID string) (string, error) {
	item, err := s.readCase(ctx, guildContext, caseRef)
	if err != nil {
		return "", err
	}
	return s.evidenceFileURL(ctx, item.ID, attachmentID, false)
}

// MemberEvidenceFileURL is EvidenceFileURL for the member a case targets.
// Members never get the original upload when Quack kept a copy, matching
// the member case view.
func (s *CaseService) MemberEvidenceFileURL(ctx context.Context, caseID, memberDiscordUserID, attachmentID string) (string, error) {
	item, err := s.store.GetCaseByID(ctx, strings.TrimSpace(caseID))
	if err != nil {
		return "", err
	}
	if item == nil || item.TargetDiscordUserID != strings.TrimSpace(memberDiscordUserID) {
		return "", ErrCaseNotFound
	}
	return s.evidenceFileURL(ctx, item.ID, attachmentID, true)
}

// evidenceFileURL resolves attachmentID among the case's evidence. A
// missing attachment, or one with nothing to show, is ErrCaseNotFound.
func (s *CaseService) evidenceFileURL(ctx context.Context, caseID, attachmentID string, member bool) (string, error) {
	_, attachments, err := s.store.ListCaseEvidence(ctx, caseID)
	if err != nil {
		return "", err
	}
	for _, item := range attachments {
		if item.ID != attachmentID {
			continue
		}
		if channelID, ok := evidenceCopyChannel(item); ok && s.evidence != nil && s.evidence.client != nil {
			fresh, err := s.evidence.client.EvidenceAttachmentURL(ctx, channelID, item.PreservedMessageDiscordID, item.PreservedAttachmentDiscordID)
			if err == nil && fresh != "" {
				return fresh, nil
			}
			if member {
				return "", err
			}
		}
		if item.OriginalURL == "" || (member && item.PreservedURL != "") {
			return "", ErrCaseNotFound
		}
		return item.OriginalURL, nil
	}
	return "", ErrCaseNotFound
}

// evidenceCopyChannel returns the evidence channel holding Quack's copy of
// item, read from the copy's message link.
func evidenceCopyChannel(item CaseEvidenceAttachment) (string, bool) {
	if item.PreservedMessageDiscordID == "" || item.PreservedAttachmentDiscordID == "" {
		return "", false
	}
	link, err := url.Parse(item.PreservedURL)
	if err != nil {
		return "", false
	}
	// https://discord.com/channels/{guild}/{channel}/{message}
	parts := strings.Split(strings.Trim(link.Path, "/"), "/")
	if len(parts) != 4 || parts[0] != "channels" || parts[3] != item.PreservedMessageDiscordID {
		return "", false
	}
	return parts[2], true
}
