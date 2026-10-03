package routes

import (
	"errors"
	"net/http"

	"github.com/quackdiscord/bot/internal/httpapi/apierror"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/httpapi/middleware"
	"github.com/quackdiscord/bot/internal/quack"
)

// listAuditLog returns audit log subject to authorization, ordering, and filtering constraints.
// @Summary List guild audit entries
// @Tags Audit
// @Produce json
// @Param discordGuildID path string true "Discord guild ID"
// @Param limit query int false "Page size"
// @Param offset query int false "Page offset"
// @Param action query string false "Audit action"
// @Param resource_type query string false "Resource type"
// @Param result query string false "Audit result"
// @Security CookieAuth
// @Success 200 {object} quack.AuditListResponse
// @Failure 400 {object} map[string]interface{}
// @Failure 403 {object} map[string]interface{}
// @Router /guilds/{discordGuildID}/audit-log [get]
func listAuditLog(c *gin.Context, services *Deps) {
	result, err := services.Audits.List(c.Request.Context(), middleware.GetGuildContext(c), quack.AuditListInput{
		Limit:               c.Query("limit"),
		Offset:              c.Query("offset"),
		ActorDiscordUserID:  c.Query("actor_discord_user_id"),
		Source:              c.Query("source"),
		Action:              c.Query("action"),
		ResourceType:        c.Query("resource_type"),
		ResourceID:          c.Query("resource_id"),
		Result:              c.Query("result"),
		CaseID:              c.Query("case_id"),
		MemberDiscordUserID: c.Query("member_discord_user_id"),
		CreatedAfter:        c.Query("created_after"),
		CreatedBefore:       c.Query("created_before"),
		ReadSource:          quack.AuditSourceAPI,
		BeforeID:            c.Query("before_id"),
	})
	if err != nil {
		writeAuditError(c, err)
		return
	}

	c.JSON(http.StatusOK, result)
}

// writeAuditError maps audit error into the preserved HTTP error response contract.
func writeAuditError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, quack.ErrAuditValidation):
		apierror.Write(c, http.StatusBadRequest, apierror.CodeValidation, err.Error())
	case errors.Is(err, quack.ErrAuditPermissionDenied):
		apierror.Write(c, http.StatusForbidden, apierror.CodeAuthorization, err.Error())
	default:
		apierror.Write(c, http.StatusInternalServerError, apierror.CodeInternal, "audit operation failed")
	}
}
