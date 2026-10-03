// Package quack is the moderation domain shared by the Discord bot and the
// HTTP API.
//
// Admins define case templates with escalation levels. Moderators apply a
// template to a member, and Quack picks the level from the member's history,
// records the case, and enforces it in Discord. Members are notified once per
// case and can appeal. Everything is audited.
//
// Case creation runs under a per-guild lock so case numbers and escalation
// counts stay consistent. Enforcement is durable: each action is a stored
// execution that a worker leases, attempts, and records, so a crash or a
// Discord outage never loses or silently repeats work. Every authorization
// decision uses live Discord state, never cached permissions.
//
// The package has no infrastructure dependencies. Storage and Discord are
// reached through small interfaces declared next to the services that use
// them (see ports.go and discord.go).
package quack

import (
	"context"
	"strings"
)

// systemActorID attributes audit entries written by Quack itself.
const systemActorID = "quack-system"

// Scheduler hands a case to the in-process action workers. It only reduces
// latency: executions are durable, and a poller picks up anything Submit
// drops.
type Scheduler interface {
	// Submit queues caseID and reports whether it was accepted.
	Submit(ctx context.Context, caseID string) bool
	Stats() QueueStats
}

// QueueStats is a snapshot of the in-process action queue.
type QueueStats struct {
	BufferSize        int    `json:"buffer_size"`
	Workers           int    `json:"workers"`
	Active            bool   `json:"active"`
	QueueSize         int    `json:"queue_size"`
	EnqueuedTotal     uint64 `json:"enqueued_total"`
	DroppedTotal      uint64 `json:"dropped_total"`
	ProcessedTotal    uint64 `json:"processed_total"`
	FailedTotal       uint64 `json:"failed_total"`
	PanickedTotal     uint64 `json:"panicked_total"`
	LastProcessedID   string `json:"last_processed_id,omitempty"`
	LastProcessedType string `json:"last_processed_type,omitempty"`
}

// Deps is everything New needs. Store is required. A nil Discord dependency
// disables what needs it: without Guilds there is no live authorization or
// case preflight, without Evidence message links are rejected, and without
// Channels the audit channel cannot be configured.
type Deps struct {
	Store     Store
	Guilds    GuildDirectory
	Enforcer  Enforcer
	Messenger Messenger
	Evidence  EvidenceClient
	Channels  StaffChannelValidator
	Scheduler Scheduler
	// DashboardBaseURL is the dashboard origin linked from appealable case
	// notifications. Without it, notifications carry no appeal button.
	DashboardBaseURL string
}

// Services are the domain services, built once at startup and shared by
// every adapter.
type Services struct {
	Guilds     *GuildService
	Settings   *GuildSettingsService
	Templates  *TemplateService
	Cases      *CaseService
	Actions    *ActionService
	Evidence   *EvidenceService
	Appeals    *AppealService
	Audits     *AuditService
	Statistics *StaffStatisticsService
	Ops        *OpsService
}

// New builds the services from deps.
func New(deps Deps) *Services {
	guilds := NewGuildService(deps.Store, deps.Guilds)
	evidence := NewEvidenceService(deps.Store, deps.Evidence)

	// Without Discord there is nothing to preflight against or capture from.
	var caseAuthorizer *GuildService
	if deps.Guilds != nil {
		caseAuthorizer = guilds
	}
	var caseEvidence *EvidenceService
	if deps.Evidence != nil {
		caseEvidence = evidence
	}

	return &Services{
		Guilds:     guilds,
		Settings:   NewGuildSettingsService(deps.Store, deps.Channels),
		Templates:  NewTemplateService(deps.Store),
		Cases:      NewCaseService(deps.Store, caseAuthorizer, caseEvidence, deps.Scheduler),
		Actions:    NewActionService(deps.Store, deps.Enforcer, deps.Messenger, guilds, deps.Scheduler, deps.DashboardBaseURL),
		Evidence:   evidence,
		Appeals:    NewAppealService(deps.Store),
		Audits:     NewAuditService(deps.Store),
		Statistics: NewStaffStatisticsService(deps.Store),
		Ops:        NewOpsService(deps.Store, deps.Scheduler),
	}
}

// DashboardBaseURL returns the first https origin in origins, without a
// trailing slash, for use as Deps.DashboardBaseURL.
func DashboardBaseURL(origins []string) string {
	for _, origin := range origins {
		if value := strings.TrimSpace(origin); strings.HasPrefix(value, "https://") {
			return strings.TrimRight(value, "/")
		}
	}
	return ""
}
