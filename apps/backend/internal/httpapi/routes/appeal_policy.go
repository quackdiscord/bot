package routes

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/httpapi/apierror"
	"github.com/quackdiscord/bot/internal/httpapi/middleware"
	httpplatform "github.com/quackdiscord/bot/internal/httpapi/platform"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/quack/model"
)

func memberReadLimit(services *quack.Services) httpplatform.RateLimit {
	return httpplatform.RateLimit{Maximum: services.Config.Limits.MemberRead.Max, Window: services.Config.Limits.MemberRead.Window}
}

func memberWriteIdempotency(primitives httpplatform.Primitives, services *quack.Services, class string) gin.HandlerFunc {
	return primitives.Idempotency.Protect(class, services.Config.API.IdempotencyTTL, memberWriteSubject)
}

func staffWriteIdempotency(primitives httpplatform.Primitives, services *quack.Services, class string) gin.HandlerFunc {
	protect := primitives.Idempotency.Protect(class, services.Config.API.IdempotencyTTL, staffWriteSubject)
	return func(c *gin.Context) {
		action := model.PermissionActionAppealReview
		if class == "appeal-settings" {
			action = model.PermissionActionGuildSettingsWrite
		}
		guild := middleware.GetGuildContext(c)
		if guild == nil || !guild.Can(action) {
			writeAppealError(c, quack.ErrAppealPermissionDenied)
			return
		}
		protect(c)
	}
}

func memberSubject(c *gin.Context) string {
	session := middleware.GetAuthSession(c)
	if session == nil {
		return "unknown"
	}
	return session.DiscordUserID
}

func memberWriteSubject(c *gin.Context) string {
	return memberSubject(c) + ":" + c.Request.Method + ":" + c.FullPath() + ":" + c.Param("caseID") + ":" + c.Param("appealID")
}

func staffAppealSubject(c *gin.Context) string {
	guildContext := middleware.GetGuildContext(c)
	if guildContext == nil || guildContext.Guild == nil {
		return "unknown"
	}
	return guildContext.Guild.ID + ":" + guildContext.ActorDiscordUserID
}

func staffWriteSubject(c *gin.Context) string {
	return staffAppealSubject(c) + ":" + c.Request.Method + ":" + c.FullPath() + ":" + c.Param("appealID")
}

func writeAppealError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, quack.ErrAppealValidation):
		apierror.Write(c, http.StatusBadRequest, apierror.CodeValidation, "invalid appeal request")
	case errors.Is(err, quack.ErrAppealPermissionDenied):
		apierror.Write(c, http.StatusForbidden, apierror.CodeAuthorization, "appeal access denied")
	case errors.Is(err, quack.ErrAppealNotFound), errors.Is(err, model.ErrAppealCaseIneligible):
		apierror.Write(c, http.StatusNotFound, apierror.CodeNotFound, "appeal not found")
	case errors.Is(err, quack.ErrAppealConflict):
		apierror.Write(c, http.StatusConflict, apierror.CodeConflict, "appeal state conflict")
	default:
		apierror.Write(c, http.StatusInternalServerError, apierror.CodeInternal, "appeal operation failed")
	}
}
