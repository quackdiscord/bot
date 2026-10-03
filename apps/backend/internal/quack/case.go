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

// maxCreateAttempts bounds retries after errPreflightStale.
const maxCreateAttempts = 4

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

var staffAttribution = caseAttribution{actorType: "staff"}

// casePreflight is what was decided before taking the guild lock. The locked
// transaction re-selects the level and compares.
type casePreflight struct {
	TemplateID        string
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
	return s.create(ctx, systemContext, input, caseAttribution{actorType: "system", system: true})
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
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if len(input.IdempotencyKey) > 191 {
		return nil, caseValidationError("idempotency key is too long")
	}

	var created *CreatedCase
	var err error
	for range maxCreateAttempts {
		var preflight *casePreflight
		preflight, err = s.preflight(ctx, guildContext, input, attribution)
		if err != nil {
			break
		}
		err = s.store.WithGuildCaseLock(ctx, guildContext.Guild.ID, func(tx CaseStore) error {
			locked := *s
			locked.store = tx
			var lockedErr error
			created, lockedErr = locked.createLocked(ctx, guildContext, input, preflight, attribution)
			return lockedErr
		})
		if !errors.Is(err, errPreflightStale) {
			break
		}
	}
	if err != nil {
		var denial *AuthorizationError
		if errors.As(err, &denial) && s.guilds != nil {
			_ = s.guilds.auditDenial(ctx, guildContext, denial.Capability, AuditSourceFromContext(ctx), denial.Reason, denial.MetadataJSON)
		}
		if errors.Is(err, ErrCaseValidation) || errors.Is(err, ErrCasePermissionDenied) ||
			errors.Is(err, ErrCaseTemplateNotAvailable) || errors.Is(err, errPreflightStale) {
			_ = s.audit(ctx, guildContext, attribution, string(AuditActionCaseCreate), "case", "unknown", AuditResultFailure, err.Error())
		}
		if errors.Is(err, errPreflightStale) {
			err = caseValidationError("case state changed repeatedly; retry the request")
		}
		return nil, err
	}

	if s.scheduler != nil && !s.scheduler.Submit(ctx, created.Case.ID) {
		slog.WarnContext(ctx, "Immediate action scheduling deferred to durable polling", "case_id", created.Case.ID)
	}
	slog.InfoContext(ctx, "Case created", "guild_id", created.Case.GuildID,
		"case_id", created.Case.ID, "case_number", created.Case.CaseNumber,
		"template_id", input.TemplateID, "source", created.Case.Source)
	response := caseResponse(created.Case, created.ActionExecutions)
	return &response, nil
}

// preflight does the slow work that must not happen inside the guild lock:
// it re-checks Discord for the actor, target, and bot, and captures linked
// messages.
func (s *CaseService) preflight(ctx context.Context, guildContext *GuildStaffContext, input CaseInput, attribution caseAttribution) (*casePreflight, error) {
	templateID, targetID := strings.TrimSpace(input.TemplateID), strings.TrimSpace(input.TargetDiscordUserID)
	if templateID == "" || targetID == "" {
		return nil, caseValidationError("template_id and target_discord_user_id are required")
	}
	template, err := s.store.GetCaseTemplateExpanded(ctx, guildContext.Guild.ID, templateID)
	if err != nil {
		return nil, err
	}
	if template == nil || template.Template.ArchivedAt != nil {
		return nil, ErrCaseTemplateNotAvailable
	}
	level, err := s.selectLevel(ctx, guildContext.Guild.ID, targetID, template)
	if err != nil {
		return nil, err
	}
	actionType := level.actionType()
	if s.guilds != nil {
		if attribution.system {
			err = s.guilds.PreflightSystemCase(ctx, guildContext, targetID, actionType)
		} else {
			err = s.guilds.PreflightCase(ctx, guildContext, targetID, actionType)
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
	if strings.TrimSpace(input.ContextURL) != "" {
		links = append(links, input.ContextURL)
	}
	result := &casePreflight{
		TemplateID:        template.Template.ID,
		TemplateVersion:   template.Template.Version,
		SelectedLevelID:   level.Level.ID,
		ActionType:        actionType,
		ContextValuesJSON: valuesJSON,
	}
	if len(links) == 0 {
		return result, nil
	}
	if s.evidence == nil {
		return nil, caseValidationError("evidence capture is not configured")
	}
	settings, err := s.store.GetGuildSettings(ctx, guildContext.Guild.ID)
	if err != nil {
		return nil, err
	}
	channelID := ""
	if settings != nil {
		channelID = settings.ManagedEvidenceChannelDiscordID
	}
	actorID := guildContext.ActorDiscordUserID
	if attribution.system {
		actorID = ""
	} else if actorID == "" {
		return nil, caseValidationError("evidence actor is required")
	}
	// A message that can't be captured is only acceptable when the moderator
	// gave other context the member can see.
	captured, err := s.evidence.capture(ctx, guildContext.Guild.DiscordGuildID, actorID, targetID, channelID, links, hasOtherContext)
	if err != nil {
		_ = s.audit(ctx, guildContext, attribution, string(AuditActionEvidenceCapture), "case_evidence", "unknown", AuditResultFailure, err.Error())
		return nil, caseValidationError(err.Error())
	}
	if captured != nil {
		result.Captured = *captured
	}
	return result, nil
}

// createLocked writes the case. It runs inside the guild lock, so it is the
// one place idempotency is checked and the level is selected for real.
func (s *CaseService) createLocked(ctx context.Context, guildContext *GuildStaffContext, input CaseInput, preflight *casePreflight, attribution caseAttribution) (*CreatedCase, error) {
	templateID := strings.TrimSpace(input.TemplateID)
	targetID := strings.TrimSpace(input.TargetDiscordUserID)
	if input.IdempotencyKey != "" {
		existing, err := s.store.GetCaseByIdempotencyKey(ctx, guildContext.Guild.ID, input.IdempotencyKey)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			if existing.TargetDiscordUserID != targetID || existing.TemplateID == nil || *existing.TemplateID != templateID {
				return nil, caseValidationError("idempotency key was already used for another case request")
			}
			actions, err := s.store.ListCaseActionExecutions(ctx, existing.ID)
			if err != nil {
				return nil, err
			}
			return &CreatedCase{Case: *existing, ActionExecutions: actions}, nil
		}
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
	template, err := s.store.GetCaseTemplateExpanded(ctx, guildContext.Guild.ID, templateID)
	if err != nil {
		return nil, err
	}
	if template == nil || template.Template.ArchivedAt != nil {
		return nil, ErrCaseTemplateNotAvailable
	}
	reason := strings.TrimSpace(template.Template.ReasonTemplate)
	if reason == "" {
		return nil, caseValidationError("reason is required")
	}
	level, err := s.selectLevel(ctx, guildContext.Guild.ID, targetID, template)
	if err != nil {
		return nil, err
	}
	if preflight.TemplateID != template.Template.ID || preflight.TemplateVersion != template.Template.Version ||
		preflight.SelectedLevelID != level.Level.ID || preflight.ActionType != level.actionType() {
		return nil, errPreflightStale
	}
	snapshotJSON, err := buildTemplateSnapshot(template.Template, template.ContextFields, preflight.ContextValuesJSON, *level)
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
		TargetDiscordUserID:     targetID,
		ModeratorDiscordUserID:  actorID,
		Reason:                  reason,
		Validity:                CaseValidityValid,
		Source:                  source,
		CorrelationID:           correlationID,
		ContextChannelDiscordID: strings.TrimSpace(input.ContextChannelDiscordID),
		ContextMessageDiscordID: strings.TrimSpace(input.ContextMessageDiscordID),
		ContextURL:              strings.TrimSpace(input.ContextURL),
		MetadataJSON:            metadataJSON,
		ContextValuesJSON:       preflight.ContextValuesJSON,
	}
	if input.IdempotencyKey != "" {
		key := input.IdempotencyKey
		item.IdempotencyKey = &key
	}
	if ref := strings.TrimSpace(input.ReplacesCaseID); ref != "" {
		prior, err := s.store.GetCaseByIDOrNumber(ctx, guildContext.Guild.ID, ref)
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
		templateActionID := action.ID
		executions = append(executions, CaseActionExecution{
			TemplateActionID:   &templateActionID,
			ActionType:         action.ActionType,
			Status:             ActionExecutionPending,
			ConfigSnapshotJSON: action.ConfigJSON,
			MaxRetries:         action.MaxRetries,
			RetryBackoffMS:     1000,
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
		if entry := caseAudit(ctx, guildContext, attribution, string(AuditActionEvidenceCapture), "case_evidence", "", result, failure); entry != nil {
			entry.MetadataJSON = marshalJSONObject(map[string]any{
				"snapshot_count":   len(captured.Snapshots),
				"attachment_count": len(captured.Attachments),
				"partial":          len(captured.Warnings) > 0,
			})
			params.AdditionalAudits = append(params.AdditionalAudits, *entry)
		}
	}
	return s.store.CreateCase(ctx, params)
}

// Void marks a case invalid so it stops counting toward escalation. The
// case and the reason it was voided stay on record. To correct a case, void
// it and create the replacement with ReplacesCaseID.
func (s *CaseService) Void(ctx context.Context, guildContext *GuildStaffContext, caseRef, reason string, replacementCaseID *string) (response *CaseResponse, err error) {
	if guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil {
		return nil, caseValidationError("missing guild context")
	}
	defer func() {
		if err == nil {
			return
		}
		result := AuditResultFailure
		if errors.Is(err, ErrCasePermissionDenied) || errors.Is(err, ErrAuthorizationDenied) {
			result = AuditResultDenied
		}
		_ = s.audit(ctx, guildContext, staffAttribution, string(AuditActionCaseVoid), "case", strings.TrimSpace(caseRef), result, err.Error())
	}()
	if !guildContext.Can(PermissionActionCaseVoid) {
		return nil, ErrCasePermissionDenied
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, caseValidationError("void reason is required")
	}
	if replacementCaseID != nil {
		return nil, caseValidationError("create the replacement after voiding this case")
	}
	item, err := s.store.GetCaseByIDOrNumber(ctx, guildContext.Guild.ID, strings.TrimSpace(caseRef))
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
		ReplacementCaseID:  replacementCaseID,
		Audit:              caseAudit(ctx, guildContext, staffAttribution, string(AuditActionCaseVoid), "case", item.ID, AuditResultSuccess, ""),
	})
	if err != nil {
		return nil, err
	}
	if voided == nil {
		return nil, ErrCaseNotFound
	}
	slog.InfoContext(ctx, "Case voided", "guild_id", voided.GuildID, "case_id", voided.ID, "case_number", voided.CaseNumber)
	actions, err := s.store.ListCaseActionExecutions(ctx, voided.ID)
	if err != nil {
		return nil, err
	}
	result := caseResponse(*voided, actions)
	return &result, nil
}

func validCaseSource(source CaseSource) bool {
	switch source {
	case CaseSourceDashboard, CaseSourceDiscord, CaseSourceHoneypot, CaseSourceV4Import:
		return true
	default:
		return false
	}
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
