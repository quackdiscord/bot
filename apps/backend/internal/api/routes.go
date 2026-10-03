package api

import (
	"net/http"
	"strings"

	"github.com/quackdiscord/bot/internal/quack"
)

// routes is the whole route table. Optional modules add theirs through
// ModuleMux under /guilds/{discordGuildID}/modules.
func (s *Server) routes() {
	// Health and operations. /metrics and /ops/status take shared keys.
	s.handle("GET /status", s.status)
	s.handle("GET /livez", s.liveness)
	s.handle("GET /readyz", s.readiness)
	s.handle("GET /metrics", s.metrics)
	s.handle("GET /ops/status", s.globalOpsStatus)

	// Discord sign-in, limited per client IP.
	oauth := s.cfg.Limits.OAuth
	s.handle("GET /auth/discord/login", s.discordLogin, s.limit("oauth-login", oauth, s.clientIPSubject))
	s.handle("GET /auth/discord/callback", s.discordCallback, s.limit("oauth-callback", oauth, s.clientIPSubject))
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
	s.staff("PATCH /guilds/{discordGuildID}/templates/{templateID}", quack.PermissionActionCaseTemplateWrite, s.updateTemplate)
	s.staff("DELETE /guilds/{discordGuildID}/templates/{templateID}", quack.PermissionActionCaseTemplateDelete, s.archiveTemplate)
	s.staff("POST /guilds/{discordGuildID}/templates/{templateID}/restore", quack.PermissionActionCaseTemplateWrite, s.restoreTemplate)
	s.staff("GET /guilds/{discordGuildID}/templates/{templateID}/export", quack.PermissionActionCaseTemplateWrite, s.exportTemplate)

	// Cases.
	s.staff("GET /guilds/{discordGuildID}/cases", quack.PermissionActionCaseRead, s.listCases)
	s.staffClass("POST /guilds/{discordGuildID}/cases", classCaseCreate, quack.PermissionActionCaseCreate, s.createCase)
	s.staff("GET /guilds/{discordGuildID}/cases/{caseRef}", quack.PermissionActionCaseRead, s.getCase)
	s.staff("POST /guilds/{discordGuildID}/cases/{caseRef}/void", quack.PermissionActionCaseVoid, s.voidCase)
	s.staff("GET /guilds/{discordGuildID}/users/{targetDiscordUserID}/cases", quack.PermissionActionCaseRead, s.listUserCases)

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
	s.appealStaff("GET /guilds/{discordGuildID}/appeal-settings", s.getAppealSettings)
	s.appealStaffWrite("PUT /guilds/{discordGuildID}/appeal-settings",
		"appeal-settings", quack.PermissionActionGuildSettingsWrite, s.updateAppealSettings)
	s.appealStaff("GET /guilds/{discordGuildID}/appeals", s.listStaffAppeals)
	s.appealStaff("GET /guilds/{discordGuildID}/appeals/{appealID}", s.getStaffAppeal)
	for _, transition := range []string{"request-information", "reopen", "accept", "reject", "close"} {
		s.appealStaffWrite("POST /guilds/{discordGuildID}/appeals/{appealID}/"+transition,
			"appeal-"+transition, quack.PermissionActionAppealReview, s.transitionAppeal(transition))
	}
	s.appealStaffWriteClass("POST /guilds/{discordGuildID}/appeals/{appealID}/reversals", classRecovery,
		"appeal-reversal", quack.PermissionActionAppealReview, s.reverseAcceptedAppeal)

	// The signed-in member's own cases and appeals. These do not require
	// membership in the guild, so a banned member can still appeal.
	s.member("GET /members/me/guilds/{guildID}/cases", s.listMemberCases)
	s.member("GET /members/me/cases/{caseID}", s.getMemberCase)
	s.memberWrite("POST /members/me/cases/{caseID}/appeal", "appeal-submit", s.submitAppeal)
	s.member("GET /members/me/appeals/{appealID}", s.getMemberAppeal)
	s.memberWrite("POST /members/me/appeals/{appealID}/information", "appeal-information", s.submitAppealInformation)
}

// handle registers h behind mws, outermost first.
func (s *Server) handle(pattern string, h http.HandlerFunc, mws ...middleware) {
	s.mux.Handle(pattern, chain(h, mws...))
}

// staff registers a guild staff route that requires action, using the
// method's endpoint class.
func (s *Server) staff(pattern string, action quack.PermissionAction, h http.HandlerFunc) {
	method, _, _ := strings.Cut(pattern, " ")
	s.staffClass(pattern, endpointClass(method), action, h)
}

// staffClass registers a guild staff route: endpoint policy, session, live
// authorization for action, and for writes an Idempotency-Key.
func (s *Server) staffClass(pattern, class string, action quack.PermissionAction, h http.HandlerFunc) {
	method, _, _ := strings.Cut(pattern, " ")
	mws := []middleware{s.policy(class), s.requireAuth, s.guild(action)}
	if isWrite(method) {
		mws = append(mws, s.idempotent("dashboard-write:"+class, s.endpointWriteSubject))
	}
	s.handle(pattern, h, mws...)
}

// appealStaff registers an appeal review read. Appeal routes only require
// guild membership up front; the appeal service checks capabilities.
func (s *Server) appealStaff(pattern string, h http.HandlerFunc, mws ...middleware) {
	method, _, _ := strings.Cut(pattern, " ")
	s.appealStaffClass(pattern, endpointClass(method), h, mws...)
}

func (s *Server) appealStaffClass(pattern, class string, h http.HandlerFunc, mws ...middleware) {
	s.handle(pattern, h, append([]middleware{
		s.policy(class),
		s.requireAuth,
		s.guild(""),
		s.limit("appeal-staff", s.cfg.Limits.MemberRead, guildActorSubject),
	}, mws...)...)
}

func (s *Server) appealStaffWrite(pattern, idempotencyClass string, action quack.PermissionAction, h http.HandlerFunc) {
	s.appealStaffWriteClass(pattern, classWrite, idempotencyClass, action, h)
}

// appealStaffWriteClass registers an appeal review write. The capability is
// checked before idempotency so a demoted reviewer cannot replay.
func (s *Server) appealStaffWriteClass(pattern, class, idempotencyClass string, action quack.PermissionAction, h http.HandlerFunc) {
	denied := func(w http.ResponseWriter, r *http.Request) {
		writeAppealError(w, r, quack.ErrAppealPermissionDenied)
	}
	s.appealStaffClass(pattern, class, h,
		allow(can(action), denied),
		s.idempotent(idempotencyClass, staffAppealWriteSubject),
	)
}

// member registers a route for the signed-in member's own resources.
func (s *Server) member(pattern string, h http.HandlerFunc, mws ...middleware) {
	method, _, _ := strings.Cut(pattern, " ")
	s.handle(pattern, h, append([]middleware{
		s.policy(endpointClass(method)),
		s.requireAuth,
		s.limit("member-reads-and-appeals", s.cfg.Limits.MemberRead, memberSubject),
	}, mws...)...)
}

func (s *Server) memberWrite(pattern, idempotencyClass string, h http.HandlerFunc) {
	s.member(pattern, h, s.idempotent(idempotencyClass, memberWriteSubject))
}
