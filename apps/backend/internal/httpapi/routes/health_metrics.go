package routes

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/quack"
)

// migrationReadinessProvider exposes the reviewed migration ledger without
// coupling HTTP health checks to the SQL adapter.
type migrationReadinessProvider interface {
	MigrationReadiness(context.Context) (uint64, error)
}

// operationalMetricProvider exposes aggregate low-cardinality counters.
type operationalMetricProvider interface {
	OperationalMetricSnapshot(context.Context) (map[string]int64, error)
}

// readinessCheck is one dependency or runtime capability in the readiness contract.
type readinessCheck struct {
	Ready   bool   `json:"ready"`
	Detail  string `json:"detail,omitempty"`
	Latency int64  `json:"latency_ms,omitempty"`
}

// readinessResponse is the fail-closed aggregate returned by /readyz.
type readinessResponse struct {
	Ready  bool                      `json:"ready"`
	Checks map[string]readinessCheck `json:"checks"`
}

// liveness reports only whether the process can serve requests. Dependency
// outages must not cause an orchestrator restart loop.
// @Summary Report process liveness
// @Tags Health
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Router /livez [get]
func liveness(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"live": true})
}

// readiness reports every dependency required to accept moderation work.
// @Summary Report dependency readiness
// @Tags Health
// @Produce json
// @Success 200 {object} readinessResponse
// @Failure 503 {object} readinessResponse
// @Router /readyz [get]
func readiness(c *gin.Context, services *Deps, discord DiscordStatusProvider) {
	result := readinessResponse{Ready: true, Checks: map[string]readinessCheck{}}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	if services == nil || services.Store == nil {
		result.Checks["database"] = readinessCheck{Ready: false, Detail: "dependency unavailable"}
		result.Checks["redis"] = readinessCheck{Ready: false, Detail: "dependency unavailable"}
	} else {
		result.Checks["database"] = timedReadiness(func() error { return services.Store.PingDatabase(ctx) })
		result.Checks["redis"] = timedReadiness(func() error { return services.Store.PingRedis(ctx) })
	}
	connected, _, latency := false, "", int64(0)
	if discord != nil {
		connected, _, latency = discord.Status()
	}
	result.Checks["discord"] = readinessCheck{Ready: connected, Latency: latency, Detail: readinessDetail(connected, "gateway disconnected")}

	queueReady := false
	var opsStatus *quack.OpsStatusResponse
	if services != nil && services.Ops != nil {
		if status, err := services.Ops.GlobalStatus(ctx); err == nil {
			opsStatus = status
			queueReady = status.Queue.Active
		}
	}
	result.Checks["queue"] = readinessCheck{Ready: queueReady, Detail: readinessDetail(queueReady, "action queue inactive")}

	migration := readinessCheck{Ready: false, Detail: "migration status unavailable"}
	if services != nil {
		if provider, ok := services.Store.(migrationReadinessProvider); ok {
			if version, err := provider.MigrationReadiness(ctx); err == nil {
				migration = readinessCheck{Ready: true, Detail: fmt.Sprintf("version %d", version)}
			} else {
				migration.Detail = "migration ledger is not current"
			}
		}
	}
	result.Checks["migration"] = migration

	actionsReady := opsStatus != nil
	if opsStatus != nil {
		for _, capability := range opsStatus.Actions.Capabilities {
			if !capability.Executable {
				actionsReady = false
				break
			}
		}
	}

	result.Checks["action_capabilities"] = readinessCheck{Ready: actionsReady, Detail: readinessDetail(actionsReady, "required action unavailable")}

	for _, check := range result.Checks {
		if !check.Ready {
			result.Ready = false
			break
		}
	}
	status := http.StatusOK
	if !result.Ready {
		status = http.StatusServiceUnavailable
	}
	c.JSON(status, result)
}

// timedReadiness runs one already-bounded dependency check and reports only a
// safe classification rather than raw adapter errors.
func timedReadiness(check func() error) readinessCheck {
	started := time.Now()
	err := check()
	ready := err == nil
	return readinessCheck{Ready: ready, Latency: time.Since(started).Milliseconds(), Detail: readinessDetail(ready, "dependency unavailable")}
}

// readinessDetail avoids repeating success text while preserving an actionable class.
func readinessDetail(ready bool, unavailable string) string {
	if ready {
		return ""
	}
	return unavailable
}

// metrics emits a token-protected Prometheus text snapshot with no actor,
// guild, member, content, URL, or payload labels.
// @Summary Export operational metrics
// @Tags Operations
// @Produce text/plain
// @Security MetricsKey
// @Success 200 {string} string
// @Failure 403 {string} string
// @Failure 404 {string} string
// @Failure 503 {string} string
// @Router /metrics [get]
func metrics(c *gin.Context, services *Deps) {
	configured := strings.TrimSpace(services.Config.API.MetricsToken)
	provided := strings.TrimSpace(c.GetHeader("X-Quack-Metrics-Key"))
	if configured == "" {
		c.Status(http.StatusNotFound)
		return
	}
	if len(configured) != len(provided) || subtle.ConstantTimeCompare([]byte(configured), []byte(provided)) != 1 {
		c.Status(http.StatusForbidden)
		return
	}
	provider, ok := services.Store.(operationalMetricProvider)
	if !ok {
		c.Status(http.StatusServiceUnavailable)
		return
	}
	snapshot, err := provider.OperationalMetricSnapshot(c.Request.Context())
	if err != nil {
		c.Status(http.StatusServiceUnavailable)
		return
	}
	if status, statusErr := services.Ops.GlobalStatus(c.Request.Context()); statusErr == nil {
		snapshot["quack_action_queue_depth"] = int64(status.Queue.QueueSize)
		snapshot["quack_action_queue_failures_total"] = int64(status.Queue.FailedTotal)
		snapshot["quack_action_retrying_current"] = status.Actions.StatusCounts["retrying"]
	}
	keys := make([]string, 0, len(snapshot))
	for key := range snapshot {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	c.Header("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	var output strings.Builder
	for _, key := range keys {
		_, _ = fmt.Fprintf(&output, "%s %d\n", key, snapshot[key])
	}
	c.String(http.StatusOK, output.String())
}
