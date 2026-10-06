package quack

import (
	"context"
	"strings"
	"time"
)

// StaffStatisticsService serves guild activity counts to staff. The counts
// are computed from history on each request; nothing is aggregated or
// ranked per moderator.
type StaffStatisticsService struct {
	store StatisticsStore
}

// NewStaffStatisticsService returns a StaffStatisticsService backed by
// store.
func NewStaffStatisticsService(store StatisticsStore) *StaffStatisticsService {
	return &StaffStatisticsService{store: store}
}

// StatisticsInput is the requested range as RFC 3339 strings. From is
// inclusive and To exclusive; To defaults to now and From to a month before
// To.
type StatisticsInput struct {
	From string
	To   string
}

// StaffStatisticsParams is the guild and range to count.
type StaffStatisticsParams struct {
	GuildID string
	From    time.Time
	To      time.Time
}

// StatisticBucket is one label and its count.
type StatisticBucket struct {
	Key   string `json:"key"`
	Count int64  `json:"count"`
}

// StaffStatistics is a guild's activity over a range, broken down by day,
// type, and outcome.
type StaffStatistics struct {
	From            time.Time         `json:"from"`
	To              time.Time         `json:"to"`
	CaseTotal       int64             `json:"case_total"`
	ActionTotal     int64             `json:"action_total"`
	AppealTotal     int64             `json:"appeal_total"`
	AuditTotal      int64             `json:"audit_total"`
	CasesByDay      []StatisticBucket `json:"cases_by_day"`
	CasesByTemplate []StatisticBucket `json:"cases_by_template"`
	CasesByValidity []StatisticBucket `json:"cases_by_validity"`
	CasesBySource   []StatisticBucket `json:"cases_by_source"`
	ActionsByDay    []StatisticBucket `json:"actions_by_day"`
	ActionsByType   []StatisticBucket `json:"actions_by_type"`
	ActionsByResult []StatisticBucket `json:"actions_by_result"`
	AppealsByDay    []StatisticBucket `json:"appeals_by_day"`
	AppealsByStatus []StatisticBucket `json:"appeals_by_status"`
	AuditsByDay     []StatisticBucket `json:"audits_by_day"`
	AuditsByAction  []StatisticBucket `json:"audits_by_action"`
	AuditsByResult  []StatisticBucket `json:"audits_by_result"`
	AuditsBySource  []StatisticBucket `json:"audits_by_source"`
}

// Get returns the guild's statistics for the requested range, which may
// span at most 366 days. It needs audit read access; only denials are
// audited.
func (s *StaffStatisticsService) Get(ctx context.Context, guildContext *GuildStaffContext, input StatisticsInput) (*StaffStatistics, error) {
	if guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil || !guildContext.Can(PermissionActionAuditRead) {
		_ = s.auditDenied(ctx, guildContext)
		return nil, ErrStatisticsPermissionDenied
	}
	from, to, err := statisticsRange(input, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	return s.store.DeriveStaffStatistics(ctx, StaffStatisticsParams{GuildID: guildContext.Guild.ID, From: from, To: to})
}

func statisticsRange(input StatisticsInput, now time.Time) (from, to time.Time, err error) {
	to = now.UTC()
	if value := strings.TrimSpace(input.To); value != "" {
		if to, err = time.Parse(time.RFC3339, value); err != nil {
			return time.Time{}, time.Time{}, statisticsValidationError("to must use RFC3339")
		}
	}
	from = to.AddDate(0, -1, 0)
	if value := strings.TrimSpace(input.From); value != "" {
		if from, err = time.Parse(time.RFC3339, value); err != nil {
			return time.Time{}, time.Time{}, statisticsValidationError("from must use RFC3339")
		}
	}
	from, to = from.UTC(), to.UTC()
	if !from.Before(to) || to.Sub(from) > 366*24*time.Hour {
		return time.Time{}, time.Time{}, statisticsValidationError("range must be positive and at most 366 days")
	}
	return from, to, nil
}

// auditDenied records a denied statistics request.
func (s *StaffStatisticsService) auditDenied(ctx context.Context, guildContext *GuildStaffContext) error {
	if guildContext == nil || guildContext.Guild == nil {
		return nil
	}
	actorID := ""
	bits := uint64(0)
	if guildContext.Staff != nil {
		actorID = guildContext.Staff.DiscordUserID
		bits = guildContext.PermissionBits
	}
	requestID, correlationID := TraceIDsFromContext(ctx)
	return recordAudit(ctx, s.store, &AuditLogEntry{
		GuildID:             guildContext.Guild.ID,
		ActorDiscordUserID:  actorID,
		ActorPermissionBits: bits,
		Source:              AuditSourceAPI,
		Action:              string(AuditActionStatisticsRead),
		ResourceType:        "statistics",
		ResourceID:          "guild",
		Result:              AuditResultDenied,
		FailureReason:       "permission_denied",
		RequestID:           requestID,
		CorrelationID:       correlationID,
		MetadataJSON:        "{}",
	})
}
