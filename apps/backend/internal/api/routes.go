package api

import (
	"net/http"
	"strings"

	"github.com/quackdiscord/bot/internal/quack"
)

// modulePrefix is where ModuleMux mounts every optional-module route.
const modulePrefix = "/guilds/{discordGuildID}/modules"

// ModuleMux mounts optional-module routes under
// /guilds/{discordGuildID}/modules. Each route gets the same protection as
// core guild routes: the endpoint rate limit, a session, live guild
// membership, and a per-actor module rate limit. Handlers find the caller
// with quack.StaffFromContext.
type ModuleMux struct {
	s *Server
}

// routes is the API's table of contents. Each helper below names the
// protection a family of routes gets; optional modules add theirs through
// ModuleMux.
func (s *Server) routes() {
	// Health and operations. /metrics and /ops/status take shared keys.
	s.handle("GET /status", s.status)
	s.handle("GET /livez", s.liveness)
	s.handle("GET /readyz", s.readiness)
	s.handle("GET /metrics", s.metrics)
	s.handle("GET /ops/status", s.globalOpsStatus)

	// Discord sign-in, limited per client IP.
	oauth := s.cfg.Limits.OAuth
	s.handle("GET /auth/discord/login", s.discordLogin,
		s.limit("oauth-login", oauth, s.clientIPSubject), s.requireOAuth)
	s.handle("GET /auth/discord/callback", s.discordCallback,
		s.limit("oauth-callback", oauth, s.clientIPSubject), s.requireOAuth)
	s.handle("GET /auth/me", s.authMe, s.requireAuth)
	s.handle("POST /auth/logout", s.logout, s.requireAuth)
	s.handle("POST /auth/logout-all", s.logoutAll, s.requireAuth)

	// Guilds the caller can open in the dashboard.
	s.handle("GET /guilds", s.listGuilds, s.policy(classRead), s.requireAuth)
	s.handle("GET /guilds/{discordGuildID}/ops/status", s.guildOpsStatus(), s.policy(classRead))
	s.staff("GET /guilds/{discordGuildID}/me", "", s.guildMe)

	// Settings.
	s.staff("GET /guilds/{discordGuildID}/settings", quack.PermissionActionGuildSettingsRead, s.getSettings)
	s.staff("PATCH /guilds/{discordGuildID}/settings", quack.PermissionActionGuildSettingsWrite, s.updateSettings)
	s.staff("POST /guilds/{discordGuildID}/settings/starter-policy-notice/acknowledge",
		quack.PermissionActionGuildSettingsWrite, s.acknowledgeStarterPolicyNotice)

	// Templates.
	s.staff("GET /guilds/{discordGuildID}/templates", quack.PermissionActionCaseTemplateRead, s.listTemplates)
	s.staff("POST /guilds/{discordGuildID}/templates", quack.PermissionActionCaseTemplateWrite, s.createTemplate)
	s.staff("POST /guilds/{discordGuildID}/templates/import", quack.PermissionActionCaseTemplateWrite, s.importTemplate)
	s.staff("GET /guilds/{discordGuildID}/templates/{templateID}", quack.PermissionActionCaseTemplateRead, s.getTemplate)
	s.staff("PATCH /guilds/{discordGuildID}/templates/{templateID}",
		quack.PermissionActionCaseTemplateWrite, s.updateTemplate)
	s.staff("DELETE /guilds/{discordGuildID}/templates/{templateID}",
		quack.PermissionActionCaseTemplateDelete, s.archiveTemplate)
	s.staff("POST /guilds/{discordGuildID}/templates/{templateID}/restore",
		quack.PermissionActionCaseTemplateWrite, s.restoreTemplate)
	s.staff("GET /guilds/{discordGuildID}/templates/{templateID}/export",
		quack.PermissionActionCaseTemplateWrite, s.exportTemplate)

	// Cases.
	s.staff("GET /guilds/{discordGuildID}/cases", quack.PermissionActionCaseRead, s.listCases)
	s.staffClass("POST /guilds/{discordGuildID}/cases", classCaseCreate, quack.PermissionActionCaseCreate, s.createCase)
	s.staff("GET /guilds/{discordGuildID}/cases/{caseRef}", quack.PermissionActionCaseRead, s.getCase)
	s.staff("POST /guilds/{discordGuildID}/cases/{caseRef}/void", quack.PermissionActionCaseVoid, s.voidCase)
	s.staff("GET /guilds/{discordGuildID}/users/{targetDiscordUserID}/cases",
		quack.PermissionActionCaseRead, s.listUserCases)

	// Action recovery.
	s.staff("GET /guilds/{discordGuildID}/action-failures", quack.PermissionActionCaseRead, s.listFailedActions)
	s.staffClass("POST /guilds/{discordGuildID}/action-failures/{executionID}/retry",
		classRecovery, quack.PermissionActionCaseCreate, s.retryFailedAction)
	s.staff("POST /guilds/{discordGuildID}/action-failures/{executionID}/dismiss",
		quack.PermissionActionFailureDismiss, s.dismissFailedAction)
	s.staffClass("POST /guilds/{discordGuildID}/cases/{caseRef}/reversals",
		classRecovery, quack.PermissionActionCaseCreate, s.reverseCaseAction)

	// Audit log and statistics.
	s.staff("GET /guilds/{discordGuildID}/audit-log", quack.PermissionActionAuditRead, s.listAuditLog)
	s.staff("GET /guilds/{discordGuildID}/statistics", quack.PermissionActionAuditRead, s.getStatistics)

	// Appeal review. Each write names its idempotency class and capability.
	appeals := s.services.Appeals
	review := quack.PermissionActionAppealReview
	s.appealRead("GET /guilds/{discordGuildID}/appeals", s.listStaffAppeals)
	s.appealRead("GET /guilds/{discordGuildID}/appeals/{appealID}", s.getStaffAppeal)
	s.appealWrite("POST /guilds/{discordGuildID}/appeals/{appealID}/request-information",
		"appeal-request-information", review, decideAppeal(appeals.RequestInformation))
	s.appealWrite("POST /guilds/{discordGuildID}/appeals/{appealID}/reopen",
		"appeal-reopen", review, decideAppeal(appeals.Reopen))
	s.appealWrite("POST /guilds/{discordGuildID}/appeals/{appealID}/accept",
		"appeal-accept", review, decideAppeal(appeals.Accept))
	s.appealWrite("POST /guilds/{discordGuildID}/appeals/{appealID}/reject",
		"appeal-reject", review, decideAppeal(appeals.Reject))
	s.appealWrite("POST /guilds/{discordGuildID}/appeals/{appealID}/close",
		"appeal-close", review, decideAppeal(appeals.Close))
	s.appealWriteClass("POST /guilds/{discordGuildID}/appeals/{appealID}/reversals",
		classRecovery, "appeal-reversal", review, s.reverseAcceptedAppeal)

	// The signed-in member's own cases and appeals. These do not require
	// membership in the guild, so a banned member can still appeal.
	s.member("GET /members/me/guilds/{guildID}/cases", s.listMemberCases)
	s.member("GET /members/me/cases/{caseID}", s.getMemberCase)
	s.memberWrite("POST /members/me/cases/{caseID}/appeal", "appeal-submit", s.submitAppeal)
	s.member("GET /members/me/appeals/{appealID}", s.getMemberAppeal)
	s.memberWrite("POST /members/me/appeals/{appealID}/information", "appeal-information", s.submitAppealInformation)
}

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

// handle registers h behind mws, outermost first.
func (s *Server) handle(pattern string, h http.HandlerFunc, mws ...middleware) {
	s.mux.Handle(pattern, chain(h, mws...))
}

// staff registers a guild staff route that requires action, under the
// default endpoint class for its method.
func (s *Server) staff(pattern string, action quack.PermissionAction, h http.HandlerFunc) {
	s.staffClass(pattern, endpointClass(patternMethod(pattern)), action, h)
}

// staffClass registers a guild staff route: endpoint policy, session, live
// authorization for action, and for writes an Idempotency-Key.
func (s *Server) staffClass(pattern, class string, action quack.PermissionAction, h http.HandlerFunc) {
	mws := []middleware{s.policy(class), s.requireAuth, s.guild(action)}
	if isWrite(patternMethod(pattern)) {
		mws = append(mws, s.idempotent("dashboard-write:"+class, s.endpointWriteSubject))
	}
	s.handle(pattern, h, mws...)
}

// appealRead registers an appeal review read. Appeal routes only require
// guild membership up front; the appeal service checks capabilities, and
// failures use the appeal error messages.
func (s *Server) appealRead(pattern string, h http.HandlerFunc) {
	s.appealRoute(pattern, classRead, h)
}

// appealWrite registers an appeal review write under the default write
// class.
func (s *Server) appealWrite(pattern, idempotencyClass string, action quack.PermissionAction, h http.HandlerFunc) {
	s.appealWriteClass(pattern, classWrite, idempotencyClass, action, h)
}

// appealWriteClass registers an appeal review write. The capability is
// checked before idempotency so a demoted reviewer cannot replay.
func (s *Server) appealWriteClass(pattern, class, idempotencyClass string, action quack.PermissionAction, h http.HandlerFunc) {
	denied := func(w http.ResponseWriter, r *http.Request) {
		appealErrors.write(w, r, quack.ErrAppealPermissionDenied)
	}
	s.appealRoute(pattern, class, h,
		allow(can(action), denied),
		s.idempotent(idempotencyClass, staffAppealWriteSubject),
	)
}

// appealRoute registers h behind the protection every appeal review route
// shares, then mws.
func (s *Server) appealRoute(pattern, class string, h http.HandlerFunc, mws ...middleware) {
	s.handle(pattern, h, append([]middleware{
		s.policy(class),
		s.requireAuth,
		s.guild(""),
		s.limit("appeal-staff", s.cfg.Limits.MemberRead, guildActorSubject),
	}, mws...)...)
}

// member registers a route for the signed-in member's own resources, then
// mws.
func (s *Server) member(pattern string, h http.HandlerFunc, mws ...middleware) {
	s.handle(pattern, h, append([]middleware{
		s.policy(endpointClass(patternMethod(pattern))),
		s.requireAuth,
		s.limit("member-reads-and-appeals", s.cfg.Limits.MemberRead, memberSubject),
	}, mws...)...)
}

// memberWrite registers a member write, which needs an Idempotency-Key.
func (s *Server) memberWrite(pattern, idempotencyClass string, h http.HandlerFunc) {
	s.member(pattern, h, s.idempotent(idempotencyClass, memberWriteSubject))
}

// module registers a module route under modulePrefix.
func (s *Server) module(pattern string, h http.Handler) {
	method, path, _ := strings.Cut(pattern, " ")
	s.mux.Handle(method+" "+modulePrefix+path, chain(h,
		s.policy(endpointClass(method)),
		s.requireAuth,
		s.guild(""),
		s.limit("optional-modules", s.cfg.Limits.MemberRead, guildActorSubject),
	))
}

// patternMethod returns the method of a ServeMux pattern such as "GET /livez".
func patternMethod(pattern string) string {
	method, _, _ := strings.Cut(pattern, " ")
	return method
}

// isWrite reports whether method changes state, and so needs CSRF
// protection and an Idempotency-Key.
func isWrite(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}
