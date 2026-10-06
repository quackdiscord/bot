package quack

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// maxFreeTextContextRunes bounds the free-text context UpdateContext saves.
const maxFreeTextContextRunes = 4000

// AppendCaseEvidenceParams adds evidence to an existing case.
type AppendCaseEvidenceParams struct {
	GuildID, CaseID string
	Evidence        []CaseEvidenceSnapshot
	Attachments     []CaseEvidenceAttachment
	Audit           *AuditLogEntry
}

// UpdateCaseContextParams replaces a case's context values. CaseRef is a
// case ID or guild case number.
type UpdateCaseContextParams struct {
	GuildID, CaseRef  string
	ContextValuesJSON string
	Audit             *AuditLogEntry
}

// AddEvidence captures message links and uploaded files and attaches them to
// an existing case. It changes nothing else: the level, validity, and
// enforcement stay as they were, even on a voided case. caseRef is a case ID
// or number, and the caller needs case create access.
func (s *CaseService) AddEvidence(ctx context.Context, guildContext *GuildStaffContext, caseRef string, links []string, files []DiscordAttachmentSnapshot) (*CaseDetailResponse, error) {
	ctx = ensureTraceContext(ctx)
	if guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil || !guildContext.Can(PermissionActionCaseCreate) {
		return nil, ErrCasePermissionDenied
	}
	if len(links) == 0 && len(files) == 0 {
		return nil, caseValidationError("attach a file or provide a message link")
	}
	if s.evidence == nil {
		return nil, caseValidationError("evidence capture is not configured")
	}
	item, err := s.store.GetCaseByIDOrNumber(ctx, guildContext.Guild.ID, strings.TrimSpace(caseRef))
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, ErrCaseNotFound
	}
	channelID, err := s.evidenceChannel(ctx, guildContext.Guild.ID)
	if err != nil {
		return nil, err
	}
	actorID := guildContext.ActorDiscordUserID
	captured, err := s.evidence.Capture(ctx, guildContext.Guild.DiscordGuildID, actorID, channelID, links, false)
	if err != nil {
		_ = s.audit(ctx, guildContext, staffAttribution, string(AuditActionEvidenceCapture), "case", item.ID, AuditResultFailure, err.Error())
		return nil, caseValidationError(err.Error())
	}
	uploads, err := s.evidence.CaptureUploads(ctx, guildContext.Guild.DiscordGuildID, actorID, channelID, files)
	if err != nil {
		return nil, err
	}
	captured.append(uploads)
	err = s.store.AppendCaseEvidence(ctx, AppendCaseEvidenceParams{
		GuildID:     guildContext.Guild.ID,
		CaseID:      item.ID,
		Evidence:    captured.Snapshots,
		Attachments: captured.Attachments,
		Audit:       caseAudit(ctx, guildContext, staffAttribution, string(AuditActionCaseUpdate), "case", item.ID, AuditResultSuccess, ""),
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, guildContext, item.ID)
}

// UpdateContext replaces a case's context with free text, as written in
// Discord's legacy "What happened?" form for unstructured cases. An empty text
// clears it. Structured cases must use UpdateContextValues. The rule, level,
// validity, and enforcement are untouched. Discord message links in the text
// that the case does not already have are captured as evidence after the
// text is saved; if that fails, the text stays saved and the returned detail
// has EvidenceIncomplete set.
func (s *CaseService) UpdateContext(ctx context.Context, guildContext *GuildStaffContext, caseRef, text string) (*CaseDetailResponse, error) {
	ctx = ensureTraceContext(ctx)
	if guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil || !guildContext.Can(PermissionActionCaseCreate) {
		return nil, ErrCasePermissionDenied
	}
	item, err := s.store.GetCaseByIDOrNumber(ctx, guildContext.Guild.ID, strings.TrimSpace(caseRef))
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, ErrCaseNotFound
	}
	if snapshot := parseTemplateSnapshot(item.TemplateSnapshotJSON); snapshot != nil && len(snapshot.ContextFields) > 0 {
		return nil, caseValidationError("use the structured context form for this case")
	}
	text = strings.TrimSpace(text)
	if len([]rune(text)) > maxFreeTextContextRunes {
		return nil, caseValidationError(fmt.Sprintf("context must be %d characters or fewer", maxFreeTextContextRunes))
	}
	values := []CaseContextValueResponse{}
	if text != "" {
		values = append(values, CaseContextValueResponse{
			Key:       "context",
			Label:     "Context",
			FieldType: ContextFieldLongText,
			Value:     text,
		})
	}
	body, err := json.Marshal(values)
	if err != nil {
		return nil, fmt.Errorf("marshal case context: %w", err)
	}
	item, err = s.store.UpdateCaseContext(ctx, UpdateCaseContextParams{
		GuildID:           guildContext.Guild.ID,
		CaseRef:           strings.TrimSpace(caseRef),
		ContextValuesJSON: string(body),
		Audit:             caseAudit(ctx, guildContext, staffAttribution, string(AuditActionCaseUpdate), "case", "", AuditResultSuccess, ""),
	})
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, ErrCaseNotFound
	}
	detail, err := s.Get(ctx, guildContext, item.ID)
	if err != nil {
		return nil, err
	}
	return s.captureContextLinks(ctx, guildContext, detail, text), nil
}

// UpdateContextValues edits structured context against the case's original rule
// fields. Later rule versions cannot change field labels, types, or requirements.
// Evidence snapshots remain intact; newly supplied links are captured separately.
func (s *CaseService) UpdateContextValues(ctx context.Context, guildContext *GuildStaffContext, caseRef string, values []CaseContextValueInput) (*CaseDetailResponse, error) {
	ctx = ensureTraceContext(ctx)
	if guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil || !guildContext.Can(PermissionActionCaseCreate) {
		return nil, ErrCasePermissionDenied
	}
	item, err := s.store.GetCaseByIDOrNumber(ctx, guildContext.Guild.ID, strings.TrimSpace(caseRef))
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, ErrCaseNotFound
	}
	snapshot := parseTemplateSnapshot(item.TemplateSnapshotJSON)
	if snapshot == nil || len(snapshot.ContextFields) == 0 {
		return nil, caseValidationError("case has no structured context fields")
	}
	fields := make([]CaseTemplateContextField, 0, len(snapshot.ContextFields))
	for _, field := range snapshot.ContextFields {
		fields = append(fields, CaseTemplateContextField{Key: field.Key, Label: field.Label, FieldType: field.FieldType, Required: field.Required})
	}
	body, links, _, err := validateContextValues(fields, values)
	if err != nil {
		return nil, err
	}
	for _, link := range links {
		if _, err := ParseDiscordMessageLink(link); err != nil {
			return nil, caseValidationError("context message link is invalid")
		}
	}
	_, err = s.store.UpdateCaseContext(ctx, UpdateCaseContextParams{
		GuildID: guildContext.Guild.ID, CaseRef: item.ID, ContextValuesJSON: body,
		Audit: caseAudit(ctx, guildContext, staffAttribution, string(AuditActionCaseUpdate), "case", item.ID, AuditResultSuccess, ""),
	})
	if err != nil {
		return nil, err
	}
	detail, err := s.Get(ctx, guildContext, item.ID)
	if err != nil {
		return nil, err
	}
	var text []string
	for _, value := range detail.ContextValues {
		if textValue, ok := value.Value.(string); ok {
			text = append(text, textValue)
		}
	}
	return s.captureContextLinks(ctx, guildContext, detail, strings.Join(text, "\n")), nil
}

// contextURLPattern finds URLs in prose, Markdown links, and angle brackets.
// ParseDiscordMessageLink still decides what is a message link.
var contextURLPattern = regexp.MustCompile(`https://[^\s<>()]+`)

// contextMessageLinks returns the Discord message links in text, one per
// message, so query strings or alternate hosts cannot capture a message
// twice.
func contextMessageLinks(text string) []DiscordMessageReference {
	var links []DiscordMessageReference
	seen := map[string]bool{}
	for _, raw := range contextURLPattern.FindAllString(text, -1) {
		ref, err := ParseDiscordMessageLink(strings.TrimRight(raw, ".,;!?\"'"))
		if err != nil || seen[ref.key()] {
			continue
		}
		seen[ref.key()] = true
		links = append(links, ref)
	}
	return links
}

// ContextContainsMessageLinks reports whether text has any Discord message
// links, so an adapter can tell a moderator why evidence capture was tried.
func ContextContainsMessageLinks(text string) bool { return len(contextMessageLinks(text)) > 0 }

// key identifies the referenced message.
func (r DiscordMessageReference) key() string {
	return r.GuildID + "/" + r.ChannelID + "/" + r.MessageID
}

// captureContextLinks adds the message links in text that the case does not
// already have as evidence. Existing evidence is never removed or recaptured,
// so deleting a link from the text keeps its snapshot. Links to other guilds,
// links past the per-case limit, and failed captures mark the detail
// EvidenceIncomplete instead of failing the update.
func (s *CaseService) captureContextLinks(ctx context.Context, guildContext *GuildStaffContext, detail *CaseDetailResponse, text string) *CaseDetailResponse {
	links := contextMessageLinks(text)
	if len(links) == 0 {
		return detail
	}
	seen := map[string]bool{}
	for _, evidence := range detail.Evidence {
		if ref, err := ParseDiscordMessageLink(evidence.MessageURL); err == nil {
			seen[ref.key()] = true
		}
	}
	incomplete, attempts := false, 0
	for _, ref := range links {
		if seen[ref.key()] {
			continue
		}
		if attempts >= maxEvidenceMessages || ref.GuildID != guildContext.Guild.DiscordGuildID {
			incomplete = true
			continue
		}
		attempts++
		updated, err := s.AddEvidence(ctx, guildContext, detail.ID, []string{ref.URL}, nil)
		if err != nil {
			incomplete = true
			continue
		}
		detail = updated
		seen[ref.key()] = true
	}
	detail.EvidenceIncomplete = detail.EvidenceIncomplete || incomplete
	return detail
}

// CaptureUploads stores files a moderator uploaded as evidence, copying each
// into the evidence channel. Each file becomes its own evidence item,
// attributed to the moderator rather than the case target. Only the first
// ten files are kept; the first item says so when more were given. A file
// that could not be copied keeps its metadata and a warning.
func (s *EvidenceService) CaptureUploads(ctx context.Context, guildID, actorDiscordUserID, evidenceChannelID string, files []DiscordAttachmentSnapshot) (*CapturedEvidence, error) {
	result := &CapturedEvidence{}
	truncated := len(files) > maxEvidenceAttachments
	if truncated {
		files = files[:maxEvidenceAttachments]
	}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if s.client == nil {
			return nil, fmt.Errorf("%w: Discord evidence capture is unavailable", ErrEvidenceValidation)
		}
		evidenceID := NewID()
		record := s.preserve(ctx, guildID, evidenceChannelID, evidenceID, file)
		result.Snapshots = append(result.Snapshots, CaseEvidenceSnapshot{
			ULIDModel:           ULIDModel{ID: evidenceID},
			GuildID:             guildID,
			AuthorDiscordUserID: actorDiscordUserID,
			MessageDiscordID:    file.ID,
			MessageCreatedAt:    time.Now().UTC(),
			EmbedsJSON:          "[]",
			CaptureOutcome:      captureOutcomeUploaded,
			CaptureWarning:      record.Warning,
		})
		result.Attachments = append(result.Attachments, record)
		if record.Warning != "" {
			result.Warnings = append(result.Warnings, record.Warning)
		}
	}
	if truncated {
		const warning = "Only the first ten files were saved. Add the remaining files separately."
		result.Snapshots[0].CaptureWarning = strings.TrimSpace(result.Snapshots[0].CaptureWarning + " " + warning)
		result.Warnings = append(result.Warnings, warning)
	}
	return result, nil
}

// Capture outcomes recorded on evidence snapshots besides the unavailable
// outcomes the Discord adapter reports.
const (
	captureOutcomeCaptured = "captured"
	captureOutcomeUploaded = "uploaded"
)

// append adds other's snapshots, attachments, and warnings to c.
func (c *CapturedEvidence) append(other *CapturedEvidence) {
	c.Snapshots = append(c.Snapshots, other.Snapshots...)
	c.Attachments = append(c.Attachments, other.Attachments...)
	c.Warnings = append(c.Warnings, other.Warnings...)
}

// evidenceIncomplete reports whether any evidence item has a warning or
// could not be captured at all.
func evidenceIncomplete(evidence []CaseEvidenceSnapshot) bool {
	for _, item := range evidence {
		if item.incomplete() {
			return true
		}
	}
	return false
}

// incomplete reports whether the snapshot is missing content or files.
func (e CaseEvidenceSnapshot) incomplete() bool {
	return e.CaptureWarning != "" || (e.CaptureOutcome != captureOutcomeCaptured && e.CaptureOutcome != captureOutcomeUploaded)
}

// evidenceChannel returns the guild's managed evidence channel, or "" when
// it has none yet.
func (s *CaseService) evidenceChannel(ctx context.Context, guildID string) (string, error) {
	settings, err := s.store.GetGuildSettings(ctx, guildID)
	if err != nil || settings == nil {
		return "", err
	}
	return settings.ManagedEvidenceChannelDiscordID, nil
}
