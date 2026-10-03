package routes

import (
	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/httpapi/middleware"
	httpplatform "github.com/quackdiscord/bot/internal/httpapi/platform"
	"github.com/quackdiscord/bot/internal/moduleintegration"
	"github.com/quackdiscord/bot/internal/quack"
)

// SetupRoutes explicitly wires setup routes so runtime behavior does not depend on init-time registration.
func SetupRoutes(r *gin.Engine, services *Deps, providers ...DiscordStatusProvider) {
	if err := SetupRoutesWithModules(r, services, nil, providers...); err != nil {
		panic(err)
	}
}

// SetupRoutesWithModules installs core and optional-module registrars and
// returns composition errors to process startup.
func SetupRoutesWithModules(r *gin.Engine, services *Deps, moduleRuntime *moduleintegration.Runtime, providers ...DiscordStatusProvider) error {
	var discord DiscordStatusProvider
	if len(providers) > 0 {
		discord = providers[0]
	}
	r.GET("/status", func(c *gin.Context) { status(c, services, discord) })
	r.GET("/livez", liveness)
	r.GET("/readyz", func(c *gin.Context) { readiness(c, services, discord) })
	r.GET("/metrics", func(c *gin.Context) { metrics(c, services) })
	r.GET("/ops/status", func(c *gin.Context) { globalOpsStatus(c, services) })
	r.GET("/guilds/:discordGuildID/ops/status", func(c *gin.Context) { guildOpsStatus(c, services) })
	setupAuthRoutes(r, services)
	if err := setupGuildRoutes(r, services, moduleRuntime); err != nil {
		return err
	}
	return setupMemberRoutes(r, services, moduleRuntime)
}

// setupGuildRoutes explicitly wires setup guild routes so runtime behavior does not depend on init-time registration.
func setupGuildRoutes(r *gin.Engine, services *Deps, moduleRuntime *moduleintegration.Runtime) error {
	guilds := r.Group("/guilds")
	guilds.Use(middleware.RequireAuth(services.Store, services.Config.Auth))
	RegisterCoreModerationStaffRoutes(guilds, services)
	RegisterAuditStatisticsStaffRoutes(guilds, services)
	if moduleRuntime != nil {
		primitives := httpplatform.FromRepository(services.Store)
		if err := RegisterAppealStaffRoutes(guilds, services, moduleRuntime.Appeals, primitives); err != nil {
			return err
		}
		if err := moduleRuntime.RegisterHTTP(guilds, services.Services, services.Config, primitives); err != nil {
			return err
		}
	}

	guilds.GET("", func(c *gin.Context) { listUserGuilds(c, services) })
	guilds.GET("/:discordGuildID/me", middleware.RequireGuildContext(services.Services, ""), guildMe)
	guilds.GET("/:discordGuildID/settings", middleware.RequireGuildContext(services.Services, quack.PermissionActionGuildSettingsRead), func(c *gin.Context) {
		getGuildSettings(c, services)
	})
	guilds.PATCH("/:discordGuildID/settings", middleware.RequireGuildContext(services.Services, quack.PermissionActionGuildSettingsWrite), func(c *gin.Context) {
		updateGuildSettings(c, services)
	})
	guilds.POST("/:discordGuildID/settings/starter-policy-notice/acknowledge", middleware.RequireGuildContext(services.Services, quack.PermissionActionGuildSettingsWrite), func(c *gin.Context) {
		acknowledgeStarterPolicyNotice(c, services)
	})
	guilds.GET("/:discordGuildID/templates", middleware.RequireGuildContext(services.Services, quack.PermissionActionCaseTemplateRead), func(c *gin.Context) {
		listTemplates(c, services)
	})
	guilds.POST("/:discordGuildID/templates", middleware.RequireGuildContext(services.Services, quack.PermissionActionCaseTemplateWrite), func(c *gin.Context) {
		createTemplate(c, services)
	})
	guilds.GET("/:discordGuildID/templates/:templateID", middleware.RequireGuildContext(services.Services, quack.PermissionActionCaseTemplateRead), func(c *gin.Context) {
		getTemplate(c, services)
	})
	guilds.PATCH("/:discordGuildID/templates/:templateID", middleware.RequireGuildContext(services.Services, quack.PermissionActionCaseTemplateWrite), func(c *gin.Context) {
		updateTemplate(c, services, moduleRuntime)
	})
	guilds.DELETE("/:discordGuildID/templates/:templateID", middleware.RequireGuildContext(services.Services, quack.PermissionActionCaseTemplateDelete), func(c *gin.Context) {
		archiveTemplate(c, services, moduleRuntime)
	})
	guilds.GET("/:discordGuildID/cases", middleware.RequireGuildContext(services.Services, quack.PermissionActionCaseRead), func(c *gin.Context) {
		listCases(c, services)
	})
	guilds.POST("/:discordGuildID/cases", middleware.RequireGuildContext(services.Services, quack.PermissionActionCaseCreate), func(c *gin.Context) {
		createCase(c, services)
	})
	guilds.GET("/:discordGuildID/cases/:caseRef", middleware.RequireGuildContext(services.Services, quack.PermissionActionCaseRead), func(c *gin.Context) {
		getCase(c, services)
	})
	guilds.GET("/:discordGuildID/users/:targetDiscordUserID/cases", middleware.RequireGuildContext(services.Services, quack.PermissionActionCaseRead), func(c *gin.Context) {
		listUserCases(c, services)
	})
	guilds.GET("/:discordGuildID/audit-log", middleware.RequireGuildContext(services.Services, quack.PermissionActionAuditRead), func(c *gin.Context) {
		listAuditLog(c, services)
	})
	return nil
}

// setupMemberRoutes mounts target-owned reads behind caller authentication
// without requiring the member to remain in the Discord guild.
func setupMemberRoutes(r *gin.Engine, services *Deps, moduleRuntime *moduleintegration.Runtime) error {
	members := r.Group("/members/me")
	members.Use(middleware.RequireAuth(services.Store, services.Config.Auth))
	if moduleRuntime != nil {
		return RegisterAppealAndMemberRoutes(members, services, moduleRuntime.Appeals, httpplatform.FromRepository(services.Store))
	}
	RegisterCoreModerationMemberRoutes(members, services)
	return nil
}
