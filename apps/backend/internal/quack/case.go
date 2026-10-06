package quack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
)

// errPreflightStale means the escalation level changed between the preflight
// and the locked transaction, usually because another case for the same
// member landed in between. Creation retries.
var errPreflightStale = errors.New("case preflight became stale")

const (
	// maxCreateAttempts bounds retries after errPreflightStale.
	maxCreateAttempts = 4
	// maxIdempotencyKeyLength is the width of the indexed column.
	maxIdempotencyKeyLength = 191
	// defaultRetryBackoffMS spaces automatic retries of a template action.
	defaultRetryBackoffMS = 1000
)

// CaseService creates, voids, and reads cases.
type CaseService struct {
	store CaseStore
	// guilds, when set, re-checks Discord before each case is stored.
	guilds *GuildService
	// evidence, when set, captures linked messages before each case is
	// stored. Without it, cases with message links are rejected.
	evidence  *EvidenceService
	scheduler Scheduler
}

// NewCaseService returns a CaseService. guilds, evidence, and scheduler may
// be nil; see the CaseService fields for what each enables.
func NewCaseService(store CaseStore, guilds *GuildService, evidence *EvidenceService, scheduler Scheduler) *CaseService {
	return &CaseService{store: store, guilds: guilds, evidence: evidence, scheduler: scheduler}
}

// CaseInput is a moderator's request to apply a template to a member. The
// reason, escalation, and enforcement all come from the template; the
// moderator only supplies context.
type CaseInput struct {
	TemplateID              string                  `json:"template_id"`
	TargetDiscordUserID     string                  `json:"target_discord_user_id"`
	Source                  CaseSource              `json:"source"`
	ContextChannelDiscordID string                  `json:"context_channel_discord_id"`
	ContextMessageDiscordID string                  `json:"context_message_discord_id"`
	ContextURL              string                  `json:"context_url"`
	Metadata                json.RawMessage         `json:"metadata"`
	ContextValues           []CaseContextValueInput `json:"context_values"`
	EvidenceLinks           []string                `json:"evidence_links"`
	// Attachments are files the moderator uploaded with the request. They
	// are copied into the evidence channel as their own evidence items.
	Attachments []DiscordAttachmentSnapshot `json:"-"`
	// ReplacesCaseID links the new case to a voided case it corrects.
	ReplacesCaseID string `json:"replaces_case_id,omitempty"`
	// IdempotencyKey makes creation safe to retry: a repeated request with
	// the same key returns the case created the first time.
	IdempotencyKey string `json:"-"`
}

// caseAttribution says who a case is created by: a staff member, or Quack
// itself for honeypot cases.
type caseAttribution struct {
	actorType string
	system    bool
}

var (
	staffAttribution  = caseAttribution{actorType: "staff"}
	systemAttribution = caseAttribution{actorType: "system", system: true}
)

// casePreflight is what was decided before taking the guild lock. The locked
// transaction re-selects the level and compares, so a case is never stored
// at a level its Discord checks did not cover.
type casePreflight struct {
	Source            CaseSource
	MetadataJSON      string
	TemplateVersion   uint
	SelectedLevelID   string
	ActionType        ActionType
	ContextValuesJSON string
	Captured          CapturedEvidence
}

// Create applies a template to a member on behalf of the staff member in
// guildContext. guildContext must already be resolved; Create re-checks the
// target against Discord but does not resolve the actor again.
//
// The case is written under the guild's case lock so numbering and
// escalation stay consistent. Enforcement is queued after commit.
func (s *CaseService) Create(ctx context.Context, guildContext *GuildStaffContext, input CaseInput) (*CaseResponse, error) {
	if input.Source == CaseSourceHoneypot {
		return nil, caseValidationError("honeypot cases require the system application boundary")
	}
	return s.create(ctx, guildContext, input, staffAttribution)
}

// CreateSystemHoneypot creates a honeypot case attributed to Quack itself.
// It goes through the same path as staff cases, minus the staff checks, and
// accepts no other source.
func (s *CaseService) CreateSystemHoneypot(ctx context.Context, guildID string, input CaseInput) (*CaseResponse, error) {
	if s.guilds == nil {
		return nil, ErrAuthorizationUnavailable
	}
	if input.Source != CaseSourceHoneypot {
		return nil, caseValidationError("system case creation is restricted to the honeypot source")
	}
	guild, err := s.store.GetGuildByID(ctx, strings.TrimSpace(guildID))
	if err != nil {
		return nil, err
	}
	if guild == nil || !guild.IsActive {
		return nil, caseValidationError("active guild is required")
	}
	systemContext := &GuildStaffContext{
		Guild:       guild,
		Staff:       &StaffMember{},
		Permissions: map[PermissionAction]bool{PermissionActionCaseCreate: true},
	}
	return s.create(ctx, systemContext, input, systemAttribution)
}

// Void marks a case invalid so it stops counting toward escalation. The
// case and the reason it was voided stay on record. Its succeeded timeouts
// and bans are reversed automatically. To correct a case, void it and create
// the replacement with ReplacesCaseID.
func (s *CaseService) Void(ctx context.Context, guildContext *GuildStaffContext, caseRef, reason string) (response *CaseResponse, err error) {
	if guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil {
		return nil, caseValidationError("missing guild context")
	}
	caseRef = strings.TrimSpace(caseRef)
	defer func() {
		if err == nil {
			return
		}
		result := AuditResultFailure
		if errors.Is(err, ErrCasePermissionDenied) || errors.Is(err, ErrAuthorizationDenied) {
			result = AuditResultDenied
		}
		_ = s.audit(ctx, guildContext, staffAttribution, string(AuditActionCaseVoid), "case", caseRef, result, err.Error())
	}()
	if !guildContext.Can(PermissionActionCaseVoid) {
		return nil, ErrCasePermissionDenied
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, caseValidationError("void reason is required")
	}
	item, err := s.store.GetCaseByIDOrNumber(ctx, guildContext.Guild.ID, caseRef)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, ErrCaseNotFound
	}
	voided, err := s.store.VoidCase(ctx, VoidCaseParams{
		GuildID:            guildContext.Guild.ID,
		CaseID:             item.ID,
		ActorDiscordUserID: guildContext.Staff.DiscordUserID,
		Reason:             reason,
		Audit:              caseAudit(ctx, guildContext, staffAttribution, string(AuditActionCaseVoid), "case", item.ID, AuditResultSuccess, ""),
	})
	if err != nil {
		return nil, err
	}
	if voided == nil {
		return nil, ErrCaseNotFound
	}
	slog.InfoContext(ctx, "Case voided", "guild_id", voided.GuildID, "case_id", voided.ID, "case_number", voided.CaseNumber)
	// Voiding may have queued reversals; run them now rather than at the
	// next poll.
	if s.scheduler != nil {
		s.scheduler.Submit(ctx, voided.ID)
	}
	actions, err := s.store.ListCaseActionExecutions(ctx, voided.ID)
	if err != nil {
		return nil, err
	}
	result := caseResponse(*voided, actions)
	return &result, nil
}

func (s *CaseService) create(ctx context.Context, guildContext *GuildStaffContext, input CaseInput, attribution caseAttribution) (*CaseResponse, error) {
	ctx = ensureTraceContext(ctx)
	ctx = ContextWithAuditSource(ctx, auditSourceForCaseSource(input.Source))
	if guildContext == nil || guildContext.Guild == nil {
		return nil, caseValidationError("missing guild context")
	}
	if guildContext.Staff == nil || !guildContext.Can(PermissionActionCaseCreate) {
		return nil, ErrCasePermissionDenied
	}
	input = trimCaseInput(input)
	if len(input.IdempotencyKey) > maxIdempotencyKeyLength {
		return nil, caseValidationError("idempotency key is too long")
	}
	// A replay must not repeat the preflight: evidence capture posts to
	// Discord.
	existing, err := s.replay(ctx, guildContext.Guild.ID, input)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		response := caseResponse(existing.Case, existing.ActionExecutions)
		return &response, nil
	}

	created, err := s.commit(ctx, guildContext, input, attribution)
	if err != nil {
		return nil, s.createFailed(ctx, guildContext, attribution, err)
	}
	if s.scheduler != nil && !s.scheduler.Submit(ctx, created.Case.ID) {
		slog.WarnContext(ctx, "Immediate action scheduling deferred to durable polling", "case_id", created.Case.ID)
	}
	slog.InfoContext(ctx, "Case created", "guild_id", created.Case.GuildID,
		"case_id", created.Case.ID, "case_number", created.Case.CaseNumber,
		"template_id", input.TemplateID, "source", created.Case.Source)
	response := caseResponse(created.Case, created.ActionExecutions)
	response.EvidenceIncomplete = evidenceIncomplete(created.Evidence)
	return &response, nil
}

// commit runs the preflight and then writes the case under the guild lock,
// starting over when the preflight went stale while waiting for the lock.
func (s *CaseService) commit(ctx context.Context, guildContext *GuildStaffContext, input CaseInput, attribution caseAttribution) (*CreatedCase, error) {
	for range maxCreateAttempts {
		preflight, err := s.preflight(ctx, guildContext, input, attribution)
		if err != nil {
			return nil, err
		}
		var created *CreatedCase
		err = s.store.WithGuildCaseLock(ctx, guildContext.Guild.ID, func(tx CaseStore) error {
			locked := *s
			locked.store = tx
			var err error
			created, err = locked.createLocked(ctx, guildContext, input, preflight, attribution)
			return err
		})
		if !errors.Is(err, errPreflightStale) {
			return created, err
		}
	}
	return nil, errPreflightStale
}

// createFailed audits a failed creation and returns the error to report.
// Discord denials are audited as such; validation and permission failures
// as a failed case.create.
func (s *CaseService) createFailed(ctx context.Context, guildContext *GuildStaffContext, attribution caseAttribution, err error) error {
	var denial *AuthorizationError
	if errors.As(err, &denial) && s.guilds != nil {
		_ = s.guilds.auditDenial(ctx, guildContext, denial.Capability, AuditSourceFromContext(ctx), denial.Reason, denial.MetadataJSON)
	}
	if errors.Is(err, ErrCaseValidation) || errors.Is(err, ErrCasePermissionDenied) ||
		errors.Is(err, ErrCaseTemplateNotAvailable) || errors.Is(err, errPreflightStale) {
		_ = s.audit(ctx, guildContext, attribution, string(AuditActionCaseCreate), "case", "unknown", AuditResultFailure, err.Error())
	}
	if errors.Is(err, errPreflightStale) {
		return caseValidationError("case state changed repeatedly; retry the request")
	}
	return err
}

// preflight does the slow work that must not happen inside the guild lock:
// it validates the request, re-checks Discord for the actor, target, and
// bot, and captures linked messages.
func (s *CaseService) preflight(ctx context.Context, guildContext *GuildStaffContext, input CaseInput, attribution caseAttribution) (*casePreflight, error) {
	if input.TemplateID == "" || input.TargetDiscordUserID == "" {
		return nil, caseValidationError("template_id and target_discord_user_id are required")
	}
	source := input.Source
	if source == "" {
		source = CaseSourceDashboard
	}
	if !validCaseSource(source) {
		return nil, caseValidationError("source is invalid")
	}
	metadataJSON, err := normalizeJSONObject(input.Metadata)
	if err != nil {
		return nil, caseValidationError("metadata must be a JSON object")
	}
	template, err := s.activeTemplate(ctx, guildContext.Guild.ID, input.TemplateID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(template.Template.ReasonTemplate) == "" {
		return nil, caseValidationError("reason is required")
	}
	level, _, err := s.selectLevel(ctx, guildContext.Guild.ID, input.TargetDiscordUserID, template)
	if err != nil {
		return nil, err
	}
	actionType := level.actionType()
	if s.guilds != nil {
		if attribution.system {
			err = s.guilds.PreflightSystemCase(ctx, guildContext, input.TargetDiscordUserID, actionType)
		} else {
			err = s.guilds.PreflightCase(ctx, guildContext, input.TargetDiscordUserID, actionType)
		}
		if err != nil {
			return nil, err
		}
	}
	valuesJSON, links, hasOtherContext, err := validateContextValues(template.ContextFields, input.ContextValues)
	if err != nil {
		return nil, err
	}
	links = append(links, input.EvidenceLinks...)
	if input.ContextURL != "" {
		links = append(links, input.ContextURL)
	}
	captured, err := s.captureEvidence(ctx, guildContext, links, input.Attachments, hasOtherContext, attribution)
	if err != nil {
		return nil, err
	}
	return &casePreflight{
		Source:            source,
		MetadataJSON:      metadataJSON,
		TemplateVersion:   template.Template.Version,
		SelectedLevelID:   level.Level.ID,
		ActionType:        actionType,
		ContextValuesJSON: valuesJSON,
		Captured:          captured,
	}, nil
}

// replay returns the case already created with input's idempotency key, or
// nil if there is none. Reusing a key for a different request is an error.
func (s *CaseService) replay(ctx context.Context, guildID string, input CaseInput) (*CreatedCase, error) {
	if input.IdempotencyKey == "" {
		return nil, nil
	}
	existing, err := s.store.GetCaseByIdempotencyKey(ctx, guildID, input.IdempotencyKey)
	if err != nil || existing == nil {
		return nil, err
	}
	if existing.TargetDiscordUserID != input.TargetDiscordUserID ||
		existing.TemplateID == nil || *existing.TemplateID != input.TemplateID {
		return nil, caseValidationError("idempotency key was already used for another case request")
	}
	actions, err := s.store.ListCaseActionExecutions(ctx, existing.ID)
	if err != nil {
		return nil, err
	}
	return &CreatedCase{Case: *existing, ActionExecutions: actions}, nil
}

// createLocked writes the case. It runs inside the guild lock, so the level
// selected here is the one that counts.
func (s *CaseService) createLocked(ctx context.Context, guildContext *GuildStaffContext, input CaseInput, preflight *casePreflight, attribution caseAttribution) (*CreatedCase, error) {
	// Check again under the lock: a concurrent request with the same key may
	// have committed after our first look.
	if existing, err := s.replay(ctx, guildContext.Guild.ID, input); err != nil || existing != nil {
		return existing, err
	}
	template, err := s.activeTemplate(ctx, guildContext.Guild.ID, input.TemplateID)
	if err != nil {
		return nil, err
	}
	level, caseCount, err := s.selectLevel(ctx, guildContext.Guild.ID, input.TargetDiscordUserID, template)
	if err != nil {
		return nil, err
	}
	if template.Template.Version != preflight.TemplateVersion ||
		level.Level.ID != preflight.SelectedLevelID ||
		level.actionType() != preflight.ActionType {
		return nil, errPreflightStale
	}
	snapshotJSON, err := buildTemplateSnapshot(template, level, caseCount, preflight.ContextValuesJSON)
	if err != nil {
		return nil, err
	}

	_, correlationID := TraceIDsFromContext(ctx)
	actorID := guildContext.Staff.DiscordUserID
	if attribution.system {
		actorID = ""
	}
	item := Case{
		GuildID:                 guildContext.Guild.ID,
		TemplateID:              &template.Template.ID,
		TemplateVersion:         template.Template.Version,
		TemplateSnapshotJSON:    snapshotJSON,
		TargetDiscordUserID:     input.TargetDiscordUserID,
		ModeratorDiscordUserID:  actorID,
		Reason:                  strings.TrimSpace(template.Template.ReasonTemplate),
		Validity:                CaseValidityValid,
		Source:                  preflight.Source,
		CorrelationID:           correlationID,
		ContextChannelDiscordID: input.ContextChannelDiscordID,
		ContextMessageDiscordID: input.ContextMessageDiscordID,
		ContextURL:              input.ContextURL,
		MetadataJSON:            preflight.MetadataJSON,
		ContextValuesJSON:       preflight.ContextValuesJSON,
	}
	if input.IdempotencyKey != "" {
		item.IdempotencyKey = &input.IdempotencyKey
	}
	if input.ReplacesCaseID != "" {
		prior, err := s.store.GetCaseByIDOrNumber(ctx, guildContext.Guild.ID, input.ReplacesCaseID)
		if err != nil {
			return nil, err
		}
		if prior == nil || prior.Validity != CaseValidityVoided {
			return nil, caseValidationError("replacement must reference a voided case in this guild")
		}
		item.ReplacesCaseID = &prior.ID
	}

	executions := make([]CaseActionExecution, 0, len(level.Actions))
	for _, action := range level.Actions {
		executions = append(executions, CaseActionExecution{
			TemplateActionID:   &action.ID,
			ActionType:         action.ActionType,
			Status:             ActionExecutionPending,
			ConfigSnapshotJSON: action.ConfigJSON,
			MaxRetries:         action.MaxRetries,
			RetryBackoffMS:     defaultRetryBackoffMS,
			SafeForRetry:       true,
			CorrelationID:      correlationID,
		})
	}
	var notification *CaseNotification
	if level.Level.NotifyUser {
		notification = &CaseNotification{Status: NotificationPending}
	}

	captured := preflight.Captured
	params := CreateCaseParams{
		Case: item,
		Event: CaseEvent{
			EventType:          CaseEventCreated,
			ActorDiscordUserID: actorID,
			ActorType:          attribution.actorType,
			Visibility:         EventVisibilityPublic,
			Body:               fmt.Sprintf("Case created from template %s", template.Template.Slug),
			MetadataJSON:       "{}",
		},
		ActionExecutions: executions,
		Evidence:         captured.Snapshots,
		Attachments:      captured.Attachments,
		Notification:     notification,
		Audit:            caseAudit(ctx, guildContext, attribution, string(AuditActionCaseCreate), "case", "", AuditResultSuccess, ""),
	}
	if len(captured.Snapshots) > 0 {
		result, failure := AuditResultSuccess, ""
		if len(captured.Warnings) > 0 {
			result, failure = AuditResultFailure, "partial evidence capture"
		}
		entry := caseAudit(ctx, guildContext, attribution, string(AuditActionEvidenceCapture), "case_evidence", "", result, failure)
		entry.MetadataJSON = marshalJSONObject(map[string]any{
			"snapshot_count":   len(captured.Snapshots),
			"attachment_count": len(captured.Attachments),
			"partial":          len(captured.Warnings) > 0,
		})
		params.AdditionalAudits = append(params.AdditionalAudits, *entry)
	}
	return s.store.CreateCase(ctx, params)
}

// activeTemplate loads a template that can still be applied to new cases.
func (s *CaseService) activeTemplate(ctx context.Context, guildID, templateID string) (*ExpandedCaseTemplate, error) {
	template, err := s.store.GetCaseTemplateExpanded(ctx, guildID, templateID)
	if err != nil {
		return nil, err
	}
	if template == nil || template.Template.ArchivedAt != nil {
		return nil, ErrCaseTemplateNotAvailable
	}
	return template, nil
}

// trimCaseInput trims every identifier in input so the preflight, the
// idempotency check, and the stored case all see the same values.
func trimCaseInput(input CaseInput) CaseInput {
	input.TemplateID = strings.TrimSpace(input.TemplateID)
	input.TargetDiscordUserID = strings.TrimSpace(input.TargetDiscordUserID)
	input.ContextChannelDiscordID = strings.TrimSpace(input.ContextChannelDiscordID)
	input.ContextMessageDiscordID = strings.TrimSpace(input.ContextMessageDiscordID)
	input.ContextURL = strings.TrimSpace(input.ContextURL)
	input.ReplacesCaseID = strings.TrimSpace(input.ReplacesCaseID)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	return input
}

// caseAudit builds a case audit entry. System cases have no actor or
// permission bits.
func caseAudit(ctx context.Context, guildContext *GuildStaffContext, attribution caseAttribution, action, resourceType, resourceID string, result AuditResult, failureReason string) *AuditLogEntry {
	entry := staffAudit(ctx, guildContext, action, resourceType, resourceID, result, failureReason)
	if entry != nil && attribution.system {
		entry.ActorDiscordUserID = ""
		entry.ActorPermissionBits = 0
	}
	return entry
}

func (s *CaseService) audit(ctx context.Context, guildContext *GuildStaffContext, attribution caseAttribution, action, resourceType, resourceID string, result AuditResult, failureReason string) error {
	entry := caseAudit(ctx, guildContext, attribution, action, resourceType, resourceID, result, failureReason)
	if entry == nil {
		return nil
	}
	return recordAudit(ctx, s.store, entry)
}
