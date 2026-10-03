package quack

import (
	"context"
	"errors"
	"time"
)

// opsFailureLimit is how many recent failures a status report lists.
const opsFailureLimit = 10

// OpsService reports the health of the action pipeline to operators.
type OpsService struct {
	store     OpsStore
	scheduler Scheduler
}

// NewOpsService returns an OpsService. scheduler may be nil, in which case
// queue stats are reported as zero.
func NewOpsService(store OpsStore, scheduler Scheduler) *OpsService {
	return &OpsService{store: store, scheduler: scheduler}
}

// ActionQueueSnapshot summarizes stored executions, as counted by the store.
type ActionQueueSnapshot struct {
	StatusCounts         []ActionStatusCount
	OldestPendingOrRetry *OldestActionExecution
	RecentFailures       []RecentActionFailure
}

// ActionStatusCount is how many executions are in one status.
type ActionStatusCount struct {
	Status ActionExecutionStatus
	Count  int64
}

// OldestActionExecution is the execution that has waited longest to run.
type OldestActionExecution struct {
	ID          string                `json:"id"`
	CaseID      string                `json:"case_id"`
	CaseNumber  uint64                `json:"case_number"`
	ActionType  ActionType            `json:"action_type"`
	Status      ActionExecutionStatus `json:"status"`
	CreatedAt   time.Time             `json:"created_at"`
	NextRetryAt *time.Time            `json:"next_retry_at,omitempty"`
}

// RecentActionFailure is a recently failed execution.
type RecentActionFailure struct {
	ID            string                `json:"id"`
	CaseID        string                `json:"case_id"`
	CaseNumber    uint64                `json:"case_number"`
	ActionType    ActionType            `json:"action_type"`
	Status        ActionExecutionStatus `json:"status"`
	LastErrorCode string                `json:"last_error_code,omitempty"`
	LastError     string                `json:"last_error,omitempty"`
	UpdatedAt     time.Time             `json:"updated_at"`
}

// OpsStatusResponse is an operator status report. Queue describes the
// in-process workers; Actions describes the stored backlog, so operators can
// tell a slow queue from a stuck one.
type OpsStatusResponse struct {
	GeneratedAt time.Time       `json:"generated_at"`
	Scope       string          `json:"scope"`
	GuildID     string          `json:"guild_id,omitempty"`
	Queue       QueueStats      `json:"queue"`
	Actions     OpsActionStatus `json:"actions"`
}

// OpsActionStatus is the stored execution backlog.
type OpsActionStatus struct {
	Capabilities         []OpsActionCapability  `json:"capabilities"`
	StatusCounts         map[string]int64       `json:"status_counts"`
	OldestPendingOrRetry *OldestActionExecution `json:"oldest_pending_or_retry,omitempty"`
	RecentFailures       []RecentActionFailure  `json:"recent_failures"`
}

// OpsActionCapability says whether Quack can perform an action type.
type OpsActionCapability struct {
	ActionType ActionType `json:"action_type"`
	Executable bool       `json:"executable"`
	Status     string     `json:"status"`
}

// GlobalStatus reports on every guild.
func (s *OpsService) GlobalStatus(ctx context.Context) (*OpsStatusResponse, error) {
	return s.status(ctx, "", "global")
}

// GuildStatus reports on one guild's executions. Queue stats are still
// process-wide.
func (s *OpsService) GuildStatus(ctx context.Context, guildID string) (*OpsStatusResponse, error) {
	if guildID == "" {
		return nil, errors.New("guild id is required")
	}
	return s.status(ctx, guildID, "guild")
}

func (s *OpsService) status(ctx context.Context, guildID, scope string) (*OpsStatusResponse, error) {
	snapshot, err := s.store.ActionQueueSnapshot(ctx, guildID, opsFailureLimit)
	if err != nil {
		return nil, err
	}
	var queue QueueStats
	if s.scheduler != nil {
		queue = s.scheduler.Stats()
	}
	return &OpsStatusResponse{
		GeneratedAt: time.Now().UTC(),
		Scope:       scope,
		GuildID:     guildID,
		Queue:       queue,
		Actions:     opsActionStatus(snapshot),
	}, nil
}

func opsActionStatus(snapshot *ActionQueueSnapshot) OpsActionStatus {
	status := OpsActionStatus{
		Capabilities: []OpsActionCapability{
			{ActionType: ActionTimeoutUser, Executable: true, Status: "implemented"},
			{ActionType: ActionKickUser, Executable: true, Status: "implemented"},
			{ActionType: ActionBanUser, Executable: true, Status: "implemented"},
			{ActionType: ActionRemoveTimeout, Executable: true, Status: "staff_confirmed_reversal"},
			{ActionType: ActionUnbanUser, Executable: true, Status: "staff_confirmed_reversal"},
		},
		StatusCounts: map[string]int64{},
	}
	if snapshot == nil {
		return status
	}
	for _, row := range snapshot.StatusCounts {
		status.StatusCounts[string(row.Status)] = row.Count
	}
	status.OldestPendingOrRetry = snapshot.OldestPendingOrRetry
	status.RecentFailures = snapshot.RecentFailures
	if status.RecentFailures == nil {
		status.RecentFailures = []RecentActionFailure{}
	}
	return status
}
