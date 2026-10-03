package quack

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// AuditService serves the guild audit log to staff.
type AuditService struct {
	store AuditStore
}

// NewAuditService returns an AuditService backed by store.
func NewAuditService(store AuditStore) *AuditService {
	return &AuditService{store: store}
}

// AuditListInput holds the raw audit log filters from a request.
type AuditListInput struct {
	Limit               string
	Offset              string
	ActorDiscordUserID  string
	Action              string
	ResourceType        string
	ResourceID          string
	Result              string
	Source              string
	CaseID              string
	MemberDiscordUserID string
	CreatedAfter        string
	CreatedBefore       string
	// ReadSource is the adapter performing the read, recorded on the
	// audit.read entry.
	ReadSource AuditSource
	BeforeID   string
}

// AuditListResponse is a page of audit entries. NextCursor is set when the
// page is full and can be passed back as BeforeID.
type AuditListResponse struct {
	Entries    []AuditEntryResponse `json:"entries"`
	Total      int64                `json:"total"`
	Limit      int                  `json:"limit"`
	Offset     int                  `json:"offset"`
	NextCursor string               `json:"next_cursor,omitempty"`
}

// AuditEntryResponse is one audit entry as staff see it.
type AuditEntryResponse struct {
	ID                  string      `json:"id"`
	CreatedAt           time.Time   `json:"created_at"`
	UpdatedAt           time.Time   `json:"updated_at"`
	GuildID             string      `json:"guild_id"`
	ActorDiscordUserID  string      `json:"actor_discord_user_id,omitempty"`
	ActorPermissionBits string      `json:"actor_permission_bits"`
	Source              AuditSource `json:"source"`
	Action              string      `json:"action"`
	ResourceType        string      `json:"resource_type"`
	ResourceID          string      `json:"resource_id"`
	Result              AuditResult `json:"result"`
	FailureReason       string      `json:"failure_reason,omitempty"`
	CorrelationID       string      `json:"correlation_id,omitempty"`
	RequestID           string      `json:"request_id,omitempty"`
	Metadata            any         `json:"metadata"`
}

// List returns a filtered page of the guild's audit log. Every read,
// including denied and failed ones, is itself audited.
func (s *AuditService) List(ctx context.Context, guildContext *GuildStaffContext, input AuditListInput) (*AuditListResponse, error) {
	if guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil {
		return nil, auditValidationError("missing guild context")
	}
	if !guildContext.Can(PermissionActionAuditRead) {
		_ = s.recordRead(ctx, guildContext, input, AuditResultDenied, "permission_denied")
		return nil, ErrAuditPermissionDenied
	}

	limit, offset, err := parsePage(input.Limit, input.Offset, auditValidationError)
	if err != nil {
		return nil, err
	}
	beforeID := strings.TrimSpace(input.BeforeID)
	if beforeID != "" && len(beforeID) != 26 {
		return nil, auditValidationError("before_id must be an audit entry ID")
	}
	if beforeID != "" && offset != 0 {
		return nil, auditValidationError("offset and before_id cannot be combined")
	}
	result := AuditResult(strings.TrimSpace(input.Result))
	if result != "" && !validAuditResult(result) {
		return nil, auditValidationError("result is invalid")
	}
	source := AuditSource(strings.TrimSpace(input.Source))
	if source != "" && !validAuditSource(source) {
		return nil, auditValidationError("source is invalid")
	}
	createdAfter, err := parseFilterTime(input.CreatedAfter, auditValidationError)
	if err != nil {
		return nil, err
	}
	createdBefore, err := parseFilterTime(input.CreatedBefore, auditValidationError)
	if err != nil {
		return nil, err
	}
	// The store matches these inside metadata with LIKE, so wildcard
	// characters would widen the search beyond one case or member.
	if strings.ContainsAny(input.CaseID, `%_\\`) || strings.ContainsAny(input.MemberDiscordUserID, `%_\\`) {
		return nil, auditValidationError("case and member filters must be exact identifiers")
	}

	page, err := s.store.ListAuditLogEntriesFiltered(ctx, ListAuditLogEntriesParams{
		GuildID:             guildContext.Guild.ID,
		ActorDiscordUserID:  strings.TrimSpace(input.ActorDiscordUserID),
		Source:              string(source),
		Action:              strings.TrimSpace(input.Action),
		ResourceType:        strings.TrimSpace(input.ResourceType),
		ResourceID:          strings.TrimSpace(input.ResourceID),
		Result:              result,
		CaseID:              strings.TrimSpace(input.CaseID),
		MemberDiscordUserID: strings.TrimSpace(input.MemberDiscordUserID),
		CreatedAfter:        createdAfter,
		CreatedBefore:       createdBefore,
		BeforeID:            beforeID,
		Limit:               limit,
		Offset:              offset,
	})
	if err != nil {
		_ = s.recordRead(ctx, guildContext, input, AuditResultFailure, "query_failed")
		return nil, err
	}

	entries := make([]AuditEntryResponse, 0, len(page.Entries))
	for _, entry := range page.Entries {
		entries = append(entries, AuditEntryResponse{
			ID:                  entry.ID,
			CreatedAt:           entry.CreatedAt,
			UpdatedAt:           entry.UpdatedAt,
			GuildID:             entry.GuildID,
			ActorDiscordUserID:  entry.ActorDiscordUserID,
			ActorPermissionBits: PermissionBitsString(entry.ActorPermissionBits),
			Source:              entry.Source,
			Action:              entry.Action,
			ResourceType:        entry.ResourceType,
			ResourceID:          entry.ResourceID,
			Result:              entry.Result,
			FailureReason:       entry.FailureReason,
			CorrelationID:       entry.CorrelationID,
			RequestID:           entry.RequestID,
			Metadata:            parseJSON(entry.MetadataJSON),
		})
	}

	nextCursor := ""
	if len(entries) == limit {
		nextCursor = entries[len(entries)-1].ID
	}
	if err := s.recordRead(ctx, guildContext, input, AuditResultSuccess, ""); err != nil {
		return nil, err
	}
	return &AuditListResponse{
		Entries:    entries,
		Total:      page.Total,
		Limit:      limit,
		Offset:     offset,
		NextCursor: nextCursor,
	}, nil
}

// recordRead audits an audit log read. The metadata says which filters were
// used, never their values or the results.
func (s *AuditService) recordRead(ctx context.Context, guildContext *GuildStaffContext, input AuditListInput, result AuditResult, failure string) error {
	if guildContext == nil || guildContext.Guild == nil {
		return nil
	}
	actorID := ""
	permissionBits := uint64(0)
	if guildContext.Staff != nil {
		actorID = guildContext.Staff.DiscordUserID
		permissionBits = guildContext.PermissionBits
	}
	requestID, correlationID := TraceIDsFromContext(ctx)
	metadata, _ := json.Marshal(map[string]any{
		"actor_filter":    strings.TrimSpace(input.ActorDiscordUserID) != "",
		"source_filter":   strings.TrimSpace(input.Source) != "",
		"action_filter":   strings.TrimSpace(input.Action) != "",
		"resource_filter": strings.TrimSpace(input.ResourceType) != "" || strings.TrimSpace(input.ResourceID) != "",
		"case_filter":     strings.TrimSpace(input.CaseID) != "",
		"member_filter":   strings.TrimSpace(input.MemberDiscordUserID) != "",
		"date_filter":     strings.TrimSpace(input.CreatedAfter) != "" || strings.TrimSpace(input.CreatedBefore) != "",
	})
	readSource := input.ReadSource
	if !validAuditSource(readSource) {
		readSource = AuditSourceAPI
	}
	return recordAudit(ctx, s.store, &AuditLogEntry{
		GuildID:             guildContext.Guild.ID,
		ActorDiscordUserID:  actorID,
		ActorPermissionBits: permissionBits,
		Source:              readSource,
		Action:              string(AuditActionAuditRead),
		ResourceType:        "audit_log",
		ResourceID:          "list",
		Result:              result,
		FailureReason:       failure,
		RequestID:           requestID,
		CorrelationID:       correlationID,
		MetadataJSON:        string(metadata),
	})
}

// parsePage parses limit and offset query values. Limit defaults to 50 and
// is capped at 100. invalid wraps validation messages in the caller's
// sentinel.
func parsePage(limitValue, offsetValue string, invalid func(string) error) (limit, offset int, err error) {
	limit = 50
	if value := strings.TrimSpace(limitValue); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed <= 0 {
			return 0, 0, invalid("limit must be a positive integer")
		}
		limit = min(parsed, 100)
	}
	if value := strings.TrimSpace(offsetValue); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 {
			return 0, 0, invalid("offset must be a non-negative integer")
		}
		offset = parsed
	}
	return limit, offset, nil
}

// parseFilterTime normalizes an optional RFC 3339 filter bound to UTC.
func parseFilterTime(value string, invalid func(string) error) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return "", invalid("date filter must use RFC3339")
	}
	return parsed.UTC().Format(time.RFC3339Nano), nil
}
