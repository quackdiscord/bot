package routes

import (
	"net/http"

	"github.com/quackdiscord/bot/internal/httpapi/apierror"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/httpapi/middleware"
)

// listMemberOwnedCases uses the authenticated Discord identity rather than current guild membership.
// @Summary List the current member's cases
// @Tags Member cases
// @Produce json
// @Param guildID path string true "Quack guild ID"
// @Param limit query int false "Page size"
// @Param offset query int false "Page offset"
// @Security CookieAuth
// @Success 200 {object} quack.MemberCaseListResponse
// @Failure 401 {object} map[string]interface{}
// @Router /members/me/guilds/{guildID}/cases [get]
func listMemberOwnedCases(c *gin.Context, services *Deps) {
	session := middleware.GetAuthSession(c)
	if session == nil {
		apierror.Write(c, http.StatusUnauthorized, apierror.CodeAuthentication, "authentication required")
		return
	}
	result, err := services.Cases.ListMemberCases(c.Request.Context(), c.Param("guildID"), session.DiscordUserID, caseListInput(c))
	if err != nil {
		writeCaseError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// getMemberOwnedCase returns the privacy-safe projection only to the target identity.
// @Summary Get the current member's case
// @Tags Member cases
// @Produce json
// @Param caseID path string true "Case ID"
// @Security CookieAuth
// @Success 200 {object} map[string]interface{}
// @Failure 401 {object} map[string]interface{}
// @Failure 404 {object} map[string]interface{}
// @Router /members/me/cases/{caseID} [get]
func getMemberOwnedCase(c *gin.Context, services *Deps) {
	session := middleware.GetAuthSession(c)
	if session == nil {
		apierror.Write(c, http.StatusUnauthorized, apierror.CodeAuthentication, "authentication required")
		return
	}
	result, err := services.Cases.GetMemberCase(c.Request.Context(), c.Param("caseID"), session.DiscordUserID)
	if err != nil {
		writeCaseError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"case": result})
}
