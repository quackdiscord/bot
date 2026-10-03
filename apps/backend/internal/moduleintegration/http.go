package moduleintegration

import (
	"errors"
	"net/http"
	"strings"

	"github.com/quackdiscord/bot/internal/httpapi/apierror"

	"github.com/gin-gonic/gin"
	"github.com/quackdiscord/bot/internal/config"
	"github.com/quackdiscord/bot/internal/httpapi/middleware"
	httpplatform "github.com/quackdiscord/bot/internal/httpapi/platform"
	"github.com/quackdiscord/bot/internal/modules/generallogging"
	"github.com/quackdiscord/bot/internal/modules/honeypot"
	"github.com/quackdiscord/bot/internal/modules/tickets"
	"github.com/quackdiscord/bot/internal/quack"
)

// RegisterHTTP mounts both optional-module route registrars beneath one live,
// authenticated guild context and applies QP-B's shared safety primitives.
func (r *Runtime) RegisterHTTP(group *gin.RouterGroup, services *quack.Services, cfg config.Config, primitives httpplatform.Primitives) error {
	if r == nil || r.Tickets == nil || r.Logging == nil || r.Honeypot == nil {
		return errors.New("optional module HTTP runtime is not configured")
	}
	if group == nil || services == nil {
		return errors.New("optional module HTTP dependencies are not configured")
	}

	modulesGroup := group.Group("/:discordGuildID/modules")
	modulesGroup.Use(middleware.RequireGuildContext(services, ""))
	modulesGroup.Use(moduleRateLimit(primitives, cfg))
	modulesGroup.Use(moduleIdempotency(primitives, cfg, r.Tickets))
	// Normalize feature errors before the idempotency layer persists a response;
	// the global envelope remains the final process-wide safety boundary.
	modulesGroup.Use(middleware.ErrorEnvelope)
	tickets.RegisterRoutes(modulesGroup, r.Tickets, resolveTicketActor)
	generallogging.RegisterRoutes(modulesGroup, r.Logging, resolveLoggingActor)
	honeypot.RegisterRoutes(modulesGroup, r.Honeypot, resolveHoneypotActor)
	return nil
}

// resolveHoneypotActor translates one live guild context into Manage Guild authority.
func resolveHoneypotActor(c *gin.Context) (honeypot.Actor, error) {
	guildContext := middleware.GetGuildContext(c)
	if guildContext == nil || guildContext.Guild == nil {
		return honeypot.Actor{}, errors.New("live guild context is unavailable")
	}
	return honeypot.Actor{
		GuildID: guildContext.Guild.ID, DiscordUserID: guildContext.ActorDiscordUserID,
		CanManage: guildContext.Can(quack.PermissionActionGuildSettingsWrite),
	}, nil
}

// moduleRateLimit applies the shared authenticated-member policy to all module
// reads and writes, keyed by the current actor and internal guild.
func moduleRateLimit(primitives httpplatform.Primitives, cfg config.Config) gin.HandlerFunc {
	limit := httpplatform.RateLimit{
		Maximum: cfg.Limits.MemberRead.Max,
		Window:  cfg.Limits.MemberRead.Window,
	}
	return primitives.RateLimits.Limit("optional-modules", limit, moduleSubject)
}

// moduleIdempotency requires a fenced key for mutation methods while leaving
// safe reads unaffected.
func moduleIdempotency(primitives httpplatform.Primitives, cfg config.Config, ticketServices ...*tickets.Service) gin.HandlerFunc {
	ttl := cfg.API.IdempotencyTTL
	protect := primitives.Idempotency.Protect("optional-module-write", ttl, moduleWriteSubject)
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			guild := middleware.GetGuildContext(c)
			action := quack.PermissionActionGuildSettingsWrite
			path := c.FullPath()
			if strings.Contains(path, "/tickets/") && (strings.HasSuffix(path, "/resolve") || strings.HasSuffix(path, "/reopen")) {
				action = quack.PermissionActionTicketResolve
			}
			allowed := guild != nil && guild.Can(action)
			if strings.HasSuffix(path, "/tickets/:ticketID/cancel") && len(ticketServices) > 0 && ticketServices[0] != nil {
				actor, err := resolveTicketActor(c)
				if err == nil {
					ticket, _, err := ticketServices[0].Detail(c.Request.Context(), actor, c.Param("ticketID"))
					allowed = err == nil && ticket != nil && (actor.CanModerate || ticket.OwnerDiscordUserID == actor.DiscordUserID)
				}
			}
			if !allowed {
				apierror.Write(c, http.StatusForbidden, apierror.CodeAuthorization, "access denied")
				return
			}
			protect(c)
		default:
			c.Next()
		}
	}
}

// moduleWriteSubject prevents one idempotency key from replaying a response
// across distinct module operations while retaining the actor/guild boundary.
func moduleWriteSubject(c *gin.Context) string {
	return moduleSubject(c) + ":" + c.Request.Method + ":" + c.Request.URL.EscapedPath()
}

// moduleSubject keeps identity material inside the shared hashed key boundary.
func moduleSubject(c *gin.Context) string {
	guildContext := middleware.GetGuildContext(c)
	if guildContext == nil || guildContext.Guild == nil {
		return "unknown"
	}
	return guildContext.Guild.ID + ":" + guildContext.ActorDiscordUserID
}

// resolveTicketActor translates one live guild context into ticket authority.
func resolveTicketActor(c *gin.Context) (tickets.Actor, error) {
	guildContext := middleware.GetGuildContext(c)
	if guildContext == nil || guildContext.Guild == nil {
		return tickets.Actor{}, errors.New("live guild context is unavailable")
	}
	return tickets.Actor{
		GuildID: guildContext.Guild.ID, DiscordUserID: guildContext.ActorDiscordUserID,
		CanManage:   guildContext.Can(quack.PermissionActionGuildSettingsWrite),
		CanModerate: guildContext.Can(quack.PermissionActionTicketResolve),
	}, nil
}

// resolveLoggingActor translates one live guild context into Manage Guild authority.
func resolveLoggingActor(c *gin.Context) (generallogging.Actor, error) {
	guildContext := middleware.GetGuildContext(c)
	if guildContext == nil || guildContext.Guild == nil {
		return generallogging.Actor{}, errors.New("live guild context is unavailable")
	}
	return generallogging.Actor{
		GuildID: guildContext.Guild.ID, DiscordUserID: guildContext.ActorDiscordUserID,
		CanManage: guildContext.Can(quack.PermissionActionGuildSettingsWrite),
	}, nil
}
