package routes

import (
	"errors"
	"net/http"

	"github.com/quackdiscord/bot/internal/httpapi/apierror"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/httpapi/middleware"
	"github.com/quackdiscord/bot/internal/quack"
)

// RegisterAuditStatisticsStaffRoutes mounts QP-E feature routes without editing the integration-owned router.
func RegisterAuditStatisticsStaffRoutes(group *gin.RouterGroup, services *Deps) {
	statistics := services.Statistics
	group.GET("/:discordGuildID/statistics", middleware.RequireGuildContext(services.Services, quack.PermissionActionAuditRead), func(c *gin.Context) {
		getStatistics(c, statistics)
	})
}

// getStatistics returns a bounded guild-scoped moderation snapshot.
// @Summary Get guild moderation statistics
// @Tags Audit
// @Produce json
// @Param discordGuildID path string true "Discord guild ID"
// @Param from query string false "Inclusive RFC3339 start"
// @Param to query string false "Exclusive RFC3339 end"
// @Security CookieAuth
// @Success 200 {object} quack.StaffStatistics
// @Failure 400 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Router /guilds/{discordGuildID}/statistics [get]
func getStatistics(c *gin.Context, statistics *quack.StaffStatisticsService) {
	result, err := statistics.Get(c.Request.Context(), middleware.GetGuildContext(c), quack.StatisticsInput{From: c.Query("from"), To: c.Query("to")})
	if err != nil {
		writeStatisticsError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// writeStatisticsError maps statistics service failures to stable HTTP responses.
func writeStatisticsError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, quack.ErrStatisticsValidation):
		apierror.Write(c, http.StatusBadRequest, apierror.CodeValidation, err.Error())
	case errors.Is(err, quack.ErrStatisticsPermissionDenied):
		apierror.Write(c, http.StatusForbidden, apierror.CodeAuthorization, "statistics access denied")
	default:
		apierror.Write(c, http.StatusInternalServerError, apierror.CodeInternal, "statistics operation failed")
	}
}
