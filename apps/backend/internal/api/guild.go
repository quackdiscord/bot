package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/quackdiscord/bot/internal/quack"
)

// guild resolves the caller's live Discord permissions in the
// {discordGuildID} guild and requires action. An empty action only requires
// that the caller is present in the guild; such routes check finer
// capabilities themselves. It must run after requireAuth.
func (s *Server) guild(action quack.PermissionAction) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			session := sessionFrom(ctx)
			discordGuildID := r.PathValue("discordGuildID")
			requestID, correlationID := quack.TraceIDsFromContext(ctx)
			staff, err := s.services.Guilds.ResolveStaffContext(ctx, session, discordGuildID)
			if err != nil {
				slog.Warn("live guild authorization denied", "request_id", requestID, "correlation_id", correlationID,
					"actor_discord_user_id", session.DiscordUserID, "discord_guild_id", discordGuildID)
				if errors.Is(err, quack.ErrBotNotInGuild) {
					writeError(w, r, http.StatusNotFound, codeNotFound, "guild not found")
				} else {
					writeError(w, r, http.StatusForbidden, codeAuthorization, "live guild authorization unavailable")
				}
				return
			}
			if err := s.services.Guilds.Authorize(ctx, staff, action, quack.AuditSourceAPI); err != nil {
				slog.Warn("guild permission denied", "request_id", requestID, "correlation_id", correlationID,
					"actor_discord_user_id", session.DiscordUserID, "discord_guild_id", discordGuildID,
					"permission_action", string(action))
				writeError(w, r, http.StatusForbidden, codeAuthorization, "access denied")
				return
			}
			next.ServeHTTP(w, r.WithContext(quack.ContextWithStaff(ctx, staff)))
		})
	}
}

// allow gates a write on a check that needs the guild context, answering
// with denied when it fails. It runs before idempotency so a replay cannot
// outlive the caller's permission.
func allow(check func(*http.Request) bool, denied func(http.ResponseWriter, *http.Request)) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !check(r) {
				denied(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// can reports whether the guild staff can perform action.
func can(action quack.PermissionAction) func(*http.Request) bool {
	return func(r *http.Request) bool {
		staff := quack.StaffFromContext(r.Context())
		return staff != nil && staff.Can(action)
	}
}

// ModuleMux mounts optional-module routes under
// /guilds/{discordGuildID}/modules. Each route gets the same protection as
// core guild routes: the endpoint rate limit, a session, live guild
// membership, and a per-actor module rate limit. Handlers find the caller
// with quack.StaffFromContext.
type ModuleMux struct {
	s *Server
}

const modulePrefix = "/guilds/{discordGuildID}/modules"

// Handle mounts a read. pattern is a method and a path relative to the
// module prefix, such as "GET /tickets/{ticketID}".
func (m *ModuleMux) Handle(pattern string, h http.Handler) {
	m.s.module(pattern, h)
}

// HandleWrite mounts a write. allowed decides whether the caller may make
// it; a caller who may not gets a 403 before any idempotent replay. The
// request must carry an Idempotency-Key.
func (m *ModuleMux) HandleWrite(pattern string, allowed func(*http.Request) bool, h http.Handler) {
	denied := func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, http.StatusForbidden, codeAuthorization, "access denied")
	}
	m.s.module(pattern, chain(h,
		allow(allowed, denied),
		m.s.idempotent("optional-module-write", moduleWriteSubject),
	))
}

func (s *Server) module(pattern string, h http.Handler) {
	method, path, _ := strings.Cut(pattern, " ")
	s.mux.Handle(method+" "+modulePrefix+path, chain(h,
		s.policy(endpointClass(method)),
		s.requireAuth,
		s.guild(""),
		s.limit("optional-modules", s.cfg.Limits.MemberRead, guildActorSubject),
	))
}
