package routes

import (
	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/httpapi/middleware"
	"github.com/quackdiscord/bot/internal/quack"
)

// RegisterCoreModerationStaffRoutes mounts QP-A handlers on an authenticated router without owning the central router.
func RegisterCoreModerationStaffRoutes(group *gin.RouterGroup, services *Deps) {
	group.POST("/:discordGuildID/templates/:templateID/restore", middleware.RequireGuildContext(services.Services, quack.PermissionActionCaseTemplateWrite), func(c *gin.Context) { restoreTemplate(c, services) })
	group.GET("/:discordGuildID/templates/:templateID/export", middleware.RequireGuildContext(services.Services, quack.PermissionActionCaseTemplateWrite), func(c *gin.Context) { exportTemplate(c, services) })
	group.POST("/:discordGuildID/templates/import", middleware.RequireGuildContext(services.Services, quack.PermissionActionCaseTemplateWrite), func(c *gin.Context) { importTemplate(c, services) })
	group.POST("/:discordGuildID/cases/:caseRef/void", middleware.RequireGuildContext(services.Services, quack.PermissionActionCaseVoid), func(c *gin.Context) { voidCase(c, services) })
	group.GET("/:discordGuildID/action-failures", middleware.RequireGuildContext(services.Services, quack.PermissionActionCaseRead), func(c *gin.Context) { listFailedActions(c, services) })
	group.POST("/:discordGuildID/action-failures/:executionID/retry", middleware.RequireGuildContext(services.Services, quack.PermissionActionCaseCreate), func(c *gin.Context) { retryFailedAction(c, services) })
	group.POST("/:discordGuildID/action-failures/:executionID/dismiss", middleware.RequireGuildContext(services.Services, quack.PermissionActionFailureDismiss), func(c *gin.Context) { dismissFailedAction(c, services) })
	group.POST("/:discordGuildID/cases/:caseRef/reversals", middleware.RequireGuildContext(services.Services, quack.PermissionActionCaseCreate), func(c *gin.Context) { reverseCaseAction(c, services) })
}

// RegisterCoreModerationMemberRoutes mounts target-owned reads on a group already protected by member authentication.
func RegisterCoreModerationMemberRoutes(group *gin.RouterGroup, services *Deps) {
	group.GET("/guilds/:guildID/cases", func(c *gin.Context) { listMemberOwnedCases(c, services) })
	group.GET("/cases/:caseID", func(c *gin.Context) { getMemberOwnedCase(c, services) })
}
