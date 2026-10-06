package api

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/quackdiscord/bot/internal/quack"
)

const (
	opsKeyHeader     = "X-Quack-Ops-Key"
	metricsKeyHeader = "X-Quack-Metrics-Key"
	healthTimeout    = 3 * time.Second
)

// connectionStatus is one dependency in the /status report.
type connectionStatus struct {
	Connected bool   `json:"connected"`
	Username  string `json:"username,omitempty"`
	Latency   int64  `json:"latency,omitempty"`
}

// readinessCheck is one dependency in the /readyz report. Detail never
// carries adapter error text, which can contain hostnames and credentials.
type readinessCheck struct {
	Ready   bool   `json:"ready"`
	Detail  string `json:"detail,omitempty"`
	Latency int64  `json:"latency_ms,omitempty"`
}

// readinessResponse is the /readyz report. Checks is keyed by dependency:
// database, redis, discord, queue, migration, and action_capabilities.
type readinessResponse struct {
	Ready  bool                      `json:"ready"`
	Checks map[string]readinessCheck `json:"checks" nullable:"false"`
}

// statusResponse is the /status report.
type statusResponse struct {
	Database connectionStatus `json:"database"`
	Discord  connectionStatus `json:"discord"`
	Redis    connectionStatus `json:"redis"`
}

type livenessResponse struct {
	Live bool `json:"live"`
}

// guildOpsResponse is one guild's operational health.
type guildOpsResponse struct {
	GuildHealth quack.GuildOperationalHealth `json:"guild_health"`
	Operations  *quack.OpsStatusResponse     `json:"operations" nullable:"false"`
}

// status reports Discord, Redis, and database connectivity. It always
// answers 200; /readyz is the probe that fails.
func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), healthTimeout)
	defer cancel()
	connected, username, latency := s.discord.Status()
	writeJSON(w, http.StatusOK, statusResponse{
		Discord:  connectionStatus{Connected: connected, Username: username, Latency: latency},
		Redis:    ping(ctx, s.store.PingRedis),
		Database: ping(ctx, s.store.PingDatabase),
	})
}

// liveness only says the process is serving. It ignores dependencies, so an
// outage does not make the orchestrator restart a healthy process.
func (s *Server) liveness(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, livenessResponse{Live: true})
}

// readiness checks everything needed to take moderation work: database,
// Redis, the Discord gateway, the action queue, the migration ledger, and
// that every action type can execute. Any failure makes it a 503.
func (s *Server) readiness(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), healthTimeout)
	defer cancel()
	checks := map[string]readinessCheck{
		"database": timedCheck(ctx, s.store.PingDatabase),
		"redis":    timedCheck(ctx, s.store.PingRedis),
	}
	connected, _, latency := s.discord.Status()
	checks["discord"] = readinessCheck{
		Ready:   connected,
		Latency: latency,
		Detail:  unreadyDetail(connected, "gateway disconnected"),
	}

	ops, err := s.services.Ops.GlobalStatus(ctx)
	opsKnown := err == nil && ops != nil
	queueReady := opsKnown && ops.Queue.Active
	checks["queue"] = readinessCheck{Ready: queueReady, Detail: unreadyDetail(queueReady, "action queue inactive")}

	migration := readinessCheck{Detail: "migration ledger is not current"}
	if version, err := s.store.MigrationReadiness(ctx); err == nil {
		migration = readinessCheck{Ready: true, Detail: fmt.Sprintf("version %d", version)}
	}
	checks["migration"] = migration

	actionsReady := opsKnown && !slices.ContainsFunc(ops.Actions.Capabilities, func(c quack.OpsActionCapability) bool {
		return !c.Executable
	})
	checks["action_capabilities"] = readinessCheck{
		Ready:  actionsReady,
		Detail: unreadyDetail(actionsReady, "required action unavailable"),
	}

	result := readinessResponse{Ready: true, Checks: checks}
	for _, check := range checks {
		result.Ready = result.Ready && check.Ready
	}
	status := http.StatusOK
	if !result.Ready {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, result)
}

// metrics serves aggregate counters in the Prometheus text format. It needs
// the metrics key, and is a 404 when none is configured. Labels never carry
// guild, user, or content identifiers.
func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	configured := strings.TrimSpace(s.cfg.API.MetricsToken)
	if configured == "" {
		writeError(w, r, http.StatusNotFound, codeNotFound, "resource not found")
		return
	}
	if !secretsEqual(configured, strings.TrimSpace(r.Header.Get(metricsKeyHeader))) {
		writeError(w, r, http.StatusForbidden, codeAuthorization, "access denied")
		return
	}
	snapshot, err := s.store.OperationalMetricSnapshot(r.Context())
	if err != nil {
		writeError(w, r, http.StatusServiceUnavailable, codeDependency, "dependency unavailable")
		return
	}
	if ops, err := s.services.Ops.GlobalStatus(r.Context()); err == nil {
		snapshot["quack_action_queue_depth"] = int64(ops.Queue.QueueSize)
		snapshot["quack_action_queue_failures_total"] = int64(ops.Queue.FailedTotal)
		snapshot["quack_action_retrying_current"] = ops.Actions.StatusCounts["retrying"]
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	for _, key := range slices.Sorted(maps.Keys(snapshot)) {
		fmt.Fprintf(w, "%s %d\n", key, snapshot[key])
	}
}

// globalOpsStatus reports queue and action health across all guilds, for
// operators holding the ops key.
func (s *Server) globalOpsStatus(w http.ResponseWriter, r *http.Request) {
	if !s.validOpsKey(r) {
		if strings.TrimSpace(s.cfg.API.OpsToken) == "" {
			writeError(w, r, http.StatusNotFound, codeNotFound, "ops status is disabled")
		} else {
			writeError(w, r, http.StatusForbidden, codeAuthorization, "invalid ops key")
		}
		return
	}
	status, err := s.services.Ops.GlobalStatus(r.Context())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, codeInternal, "ops status failed")
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// guildOpsStatus reports one guild's operational health. Operators use the
// ops key; anyone else goes through the normal session and guild
// middleware and must be a guild administrator.
func (s *Server) guildOpsStatus() http.HandlerFunc {
	isAdmin := func(r *http.Request) bool { return quack.StaffFromContext(r.Context()).IsAdmin }
	notAdmin := func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, http.StatusForbidden, codeAuthorization, "guild administrator access required")
	}
	forStaff := chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.writeGuildOps(w, r, quack.StaffFromContext(r.Context()).Guild.ID)
	}), s.requireAuth, s.guild(""), s.confirmedMFA, allow(isAdmin, notAdmin))

	return func(w http.ResponseWriter, r *http.Request) {
		if !s.validOpsKey(r) {
			forStaff.ServeHTTP(w, r)
			return
		}
		guild, err := s.store.GetGuildByDiscordID(r.Context(), r.PathValue("discordGuildID"))
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, codeInternal, "guild lookup failed")
			return
		}
		if guild == nil {
			writeError(w, r, http.StatusNotFound, codeNotFound, "guild not found")
			return
		}
		s.writeGuildOps(w, r, guild.ID)
	}
}

// writeGuildOps writes the ops status of the guild with internal ID guildID.
// Guild health that cannot be read is reported as degraded rather than
// failing the whole report.
func (s *Server) writeGuildOps(w http.ResponseWriter, r *http.Request, guildID string) {
	status, err := s.services.Ops.GuildStatus(r.Context(), guildID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, codeInternal, "ops status failed")
		return
	}
	health, err := s.services.Guilds.OperationalGuildHealth(r.Context(), r.PathValue("discordGuildID"))
	if err != nil {
		health = quack.GuildOperationalHealth{Degraded: true, Reasons: []string{"guild_health_unavailable"}}
	}
	writeJSON(w, http.StatusOK, guildOpsResponse{GuildHealth: health, Operations: status})
}

// validOpsKey reports whether the request carries the configured ops key.
// With no key configured, nothing is valid. Both sides are trimmed, as for
// the metrics key.
func (s *Server) validOpsKey(r *http.Request) bool {
	configured := strings.TrimSpace(s.cfg.API.OpsToken)
	return configured != "" && secretsEqual(configured, strings.TrimSpace(r.Header.Get(opsKeyHeader)))
}

// ping reports whether check succeeds and how long it took.
func ping(ctx context.Context, check func(context.Context) error) connectionStatus {
	started := time.Now()
	if err := check(ctx); err != nil {
		return connectionStatus{}
	}
	return connectionStatus{Connected: true, Latency: time.Since(started).Milliseconds()}
}

// timedCheck runs a dependency check for /readyz and reports its latency.
func timedCheck(ctx context.Context, check func(context.Context) error) readinessCheck {
	started := time.Now()
	ready := check(ctx) == nil
	return readinessCheck{
		Ready:   ready,
		Latency: time.Since(started).Milliseconds(),
		Detail:  unreadyDetail(ready, "dependency unavailable"),
	}
}

// unreadyDetail returns detail for a failed check and nothing otherwise.
func unreadyDetail(ready bool, detail string) string {
	if ready {
		return ""
	}
	return detail
}
