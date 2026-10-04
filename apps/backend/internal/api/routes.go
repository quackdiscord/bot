package api

import (
	"net/http"
	"slices"
	"strings"

	"github.com/quackdiscord/bot/internal/modules"
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

// Handler-specific error statuses, named by the service error table that
// produces them.
var (
	caseFailures     = []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound}
	templateFailures = []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound, http.StatusConflict}
	settingsFailures = []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound}
	appealFailures   = []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound, http.StatusConflict}
	auditFailures    = []int{http.StatusBadRequest, http.StatusForbidden}
	unavailable      = []int{http.StatusServiceUnavailable}
)

// routes is the API's table of contents. Each helper below names the
// protection a family of routes gets; optional modules add theirs through
// ModuleMux. Every route carries the Doc the HTTP contract is generated from.
func (s *Server) routes() {
	// Health and operations. /metrics and /ops/status take shared keys.
	s.handle("GET /status", Protection{}, Doc{
		ID: "getStatus", Summary: "Report Discord, Redis, and database connectivity",
		Description: "Always 200; /readyz is the probe that fails.",
		Response:    statusResponse{},
	}, s.status)
	s.handle("GET /livez", Protection{}, Doc{
		ID: "getLiveness", Summary: "Report that the process is serving",
		Response: livenessResponse{},
	}, s.liveness)
	s.handle("GET /readyz", Protection{}, Doc{
		ID: "getReadiness", Summary: "Report whether the bot can take moderation work",
		Response: readinessResponse{},
		Also: []Response{{
			Status:      http.StatusServiceUnavailable,
			Description: "A check failed; the report names which",
			Body:        readinessResponse{},
		}},
	}, s.readiness)
	s.handle("GET /metrics", Protection{Auth: AuthMetricsKey}, Doc{
		ID: "getMetrics", Summary: "Aggregate counters in the Prometheus text format",
		ContentType: "text/plain; version=0.0.4", Errors: unavailable,
	}, s.metrics)
	s.handle("GET /ops/status", Protection{Auth: AuthOpsKey}, Doc{
		ID: "getOpsStatus", Summary: "Queue and action health across all guilds",
		Response: &quack.OpsStatusResponse{},
	}, s.globalOpsStatus)

	// Discord sign-in, limited per client IP.
	oauth := s.cfg.Limits.OAuth
	s.handle("GET /auth/discord/login", Protection{RateLimited: true}, Doc{
		ID: "startDiscordLogin", Summary: "Start Discord sign-in",
		Description: "Sets the OAuth state cookie, then redirects to Discord, or with mode=json returns the authorization URL.",
		Query:       loginQuery{},
		Status:      http.StatusFound,
		Also:        []Response{{Status: http.StatusOK, Description: "mode=json", Body: loginResponse{}}},
		Errors:      unavailable,
	}, s.discordLogin,
		s.limit("oauth-login", oauth, s.clientIPSubject), s.requireOAuth)
	s.handle("GET /auth/discord/callback", Protection{RateLimited: true}, Doc{
		ID: "finishDiscordLogin", Summary: "Finish Discord sign-in",
		Description: "Sets the session and CSRF cookies, then redirects to the dashboard, or returns the session if sign-in started with mode=json.",
		Query:       callbackQuery{},
		Status:      http.StatusFound,
		Also:        []Response{{Status: http.StatusOK, Description: "Sign-in started with mode=json", Body: signInResponse{}}},
		Errors:      []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusServiceUnavailable},
	}, s.discordCallback,
		s.limit("oauth-callback", oauth, s.clientIPSubject), s.requireOAuth)
	signedIn := Protection{Auth: AuthSession}
	s.handle("GET /auth/me", signedIn, Doc{
		ID: "getAuthMe", Summary: "The signed-in user and the CSRF token to echo on writes",
		Response: authMeResponse{},
	}, s.authMe, s.requireAuth)
	s.handle("POST /auth/logout", signedIn, Doc{
		ID: "logout", Summary: "End the current session",
		Status: http.StatusNoContent, Errors: unavailable,
	}, s.logout, s.requireAuth)
	s.handle("POST /auth/logout-all", signedIn, Doc{
		ID: "logoutAll", Summary: "Revoke every session of the signed-in user",
		Status: http.StatusNoContent, Errors: unavailable,
	}, s.logoutAll, s.requireAuth)

	// Guilds the caller can open in the dashboard.
	s.handle("GET /guilds", Protection{Auth: AuthSession, RateLimited: true}, Doc{
		ID: "listGuilds", Summary: "The caller's Discord guilds where they can use Quack",
		Response: guildListResponse{}, Errors: []int{http.StatusBadGateway},
	}, s.listGuilds, s.policy(classRead), s.requireAuth)
	s.handle("GET /guilds/{discordGuildID}/ops/status",
		Protection{Auth: AuthOpsKeyOrSession, RateLimited: true, Guild: true}, Doc{
			ID: "getGuildOpsStatus", Summary: "One guild's operational health",
			Description: "Operators use the ops key; anyone else must be a guild administrator.",
			Response:    guildOpsResponse{},
			Errors:      []int{http.StatusForbidden, http.StatusNotFound},
		}, s.guildOpsStatus(), s.policy(classRead))
	s.staff("GET /guilds/{discordGuildID}/me", "", s.guildMe, Doc{
		ID: "getGuildMe", Summary: "The guild and the caller's live staff permissions in it",
		Response: guildMeResponse{},
	})

	// Settings.
	s.staff("GET /guilds/{discordGuildID}/settings", quack.PermissionActionGuildSettingsRead, s.getSettings, Doc{
		ID: "getSettings", Summary: "The guild's settings",
		Response: settingsEnvelope{}, Errors: settingsFailures,
	})
	s.staff("PATCH /guilds/{discordGuildID}/settings", quack.PermissionActionGuildSettingsWrite, s.updateSettings, Doc{
		ID: "updateSettings", Summary: "Change some of the guild's settings",
		Body: quack.GuildSettingsInput{}, Response: settingsEnvelope{}, Errors: settingsFailures,
	})
	s.staff("POST /guilds/{discordGuildID}/settings/starter-policy-notice/acknowledge",
		quack.PermissionActionGuildSettingsWrite, s.acknowledgeStarterPolicyNotice, Doc{
			ID: "acknowledgeStarterPolicyNotice", Summary: "Dismiss the one-time starter template notice",
			Response: settingsEnvelope{}, Errors: settingsFailures,
		})

	// Templates.
	s.staff("GET /guilds/{discordGuildID}/templates", quack.PermissionActionCaseTemplateRead, s.listTemplates, Doc{
		ID: "listTemplates", Summary: "The guild's case templates",
		Response: templateListResponse{}, Errors: templateFailures,
	})
	s.staff("POST /guilds/{discordGuildID}/templates", quack.PermissionActionCaseTemplateWrite, s.createTemplate, Doc{
		ID: "createTemplate", Summary: "Create a case template",
		Body: quack.TemplateInput{}, Status: http.StatusCreated, Response: templateEnvelope{}, Errors: templateFailures,
	})
	s.staff("POST /guilds/{discordGuildID}/templates/import", quack.PermissionActionCaseTemplateWrite, s.importTemplate, Doc{
		ID: "importTemplate", Summary: "Create a template from an exported policy",
		Body: quack.TemplateImportInput{}, Status: http.StatusCreated, Response: templateEnvelope{}, Errors: templateFailures,
	})
	s.staff("GET /guilds/{discordGuildID}/templates/{templateID}", quack.PermissionActionCaseTemplateRead, s.getTemplate, Doc{
		ID: "getTemplate", Summary: "One case template",
		Response: templateEnvelope{}, Errors: templateFailures,
	})
	s.staff("PATCH /guilds/{discordGuildID}/templates/{templateID}",
		quack.PermissionActionCaseTemplateWrite, s.updateTemplate, Doc{
			ID: "updateTemplate", Summary: "Replace a case template",
			Body: quack.TemplateInput{}, Response: templateEnvelope{}, Errors: templateFailures,
		})
	s.staff("DELETE /guilds/{discordGuildID}/templates/{templateID}",
		quack.PermissionActionCaseTemplateDelete, s.archiveTemplate, Doc{
			ID: "archiveTemplate", Summary: "Archive a case template", Description: "Restore undoes it.",
			Response: templateEnvelope{}, Errors: templateFailures,
		})
	s.staff("POST /guilds/{discordGuildID}/templates/{templateID}/restore",
		quack.PermissionActionCaseTemplateWrite, s.restoreTemplate, Doc{
			ID: "restoreTemplate", Summary: "Restore an archived case template",
			Response: templateEnvelope{}, Errors: templateFailures,
		})
	s.staff("GET /guilds/{discordGuildID}/templates/{templateID}/export",
		quack.PermissionActionCaseTemplateWrite, s.exportTemplate, Doc{
			ID: "exportTemplate", Summary: "A template's policy without guild-specific IDs, for import elsewhere",
			Response: templatePolicyEnvelope{}, Errors: templateFailures,
		})

	// Cases.
	s.staff("GET /guilds/{discordGuildID}/cases", quack.PermissionActionCaseRead, s.listCases, Doc{
		ID: "listCases", Summary: "Search the guild's cases",
		Query: caseListQuery{}, Response: &quack.CaseListResponse{}, Errors: caseFailures,
	})
	s.staffClass("POST /guilds/{discordGuildID}/cases", classCaseCreate, quack.PermissionActionCaseCreate, s.createCase, Doc{
		ID: "createCase", Summary: "Open a case from a template",
		Body: caseCreateRequest{}, Status: http.StatusCreated, Response: caseEnvelope{}, Errors: caseFailures,
	})
	s.staff("GET /guilds/{discordGuildID}/cases/{caseRef}", quack.PermissionActionCaseRead, s.getCase, Doc{
		ID: "getCase", Summary: "One case, by ID or case number",
		Response: caseDetailEnvelope{}, Errors: caseFailures,
	})
	s.staff("POST /guilds/{discordGuildID}/cases/{caseRef}/void", quack.PermissionActionCaseVoid, s.voidCase, Doc{
		ID: "voidCase", Summary: "Mark a case invalid",
		Description: "replacement_case_id is rejected; open the replacement afterwards with replaces_case_id.",
		Body:        voidCaseRequest{}, Response: caseEnvelope{}, Errors: caseFailures,
	})
	s.staff("GET /guilds/{discordGuildID}/users/{targetDiscordUserID}/cases",
		quack.PermissionActionCaseRead, s.listUserCases, Doc{
			ID: "listUserCases", Summary: "A member's case history in the guild",
			Query: caseListQuery{}, Response: &quack.CaseProfileResponse{}, Errors: caseFailures,
		})

	// Action recovery.
	s.staff("GET /guilds/{discordGuildID}/action-failures", quack.PermissionActionCaseRead, s.listFailedActions, Doc{
		ID: "listFailedActions", Summary: "The queue of failed case actions",
		Query: pageQuery{}, Response: failedActionListResponse{}, Errors: caseFailures,
	})
	s.staffClass("POST /guilds/{discordGuildID}/action-failures/{executionID}/retry",
		classRecovery, quack.PermissionActionCaseCreate, s.retryFailedAction, Doc{
			ID: "retryFailedAction", Summary: "Requeue a failed action",
			Status: http.StatusAccepted, Response: failedActionEnvelope{}, Errors: caseFailures,
		})
	s.staff("POST /guilds/{discordGuildID}/action-failures/{executionID}/dismiss",
		quack.PermissionActionFailureDismiss, s.dismissFailedAction, Doc{
			ID: "dismissFailedAction", Summary: "Remove a failure from the queue; its history stays",
			Response: failedActionEnvelope{}, Errors: caseFailures,
		})
	s.staffClass("POST /guilds/{discordGuildID}/cases/{caseRef}/reversals",
		classRecovery, quack.PermissionActionCaseCreate, s.reverseCaseAction, Doc{
			ID: "reverseCaseAction", Summary: "Queue a timeout removal or unban",
			Description: "The body must set confirm.",
			Body:        reverseActionRequest{}, Status: http.StatusAccepted, Response: actionEnvelope{}, Errors: caseFailures,
		})

	// Discord display data: member search, user names and avatars, and
	// channels. Discord answers these; the adapter caches them briefly.
	s.staff("GET /guilds/{discordGuildID}/directory/members", quack.PermissionActionCaseRead, s.searchMembers, Doc{
		ID: "searchMembers", Summary: "Search the guild's current members by name",
		Description: "Matches the start of a username or nickname. limit defaults to 10 and is capped at 25.",
		Query:       memberSearchQuery{}, Response: memberSearchResponse{}, Errors: directoryFailures,
	})
	s.staff("GET /guilds/{discordGuildID}/directory/users", quack.PermissionActionCaseRead, s.lookupUsers, Doc{
		ID: "lookupUsers", Summary: "Display names and avatars for Discord user IDs",
		Description: "Users come back in the order asked, as guild members when they are one. Unknown users are left out.",
		Query:       userLookupQuery{}, Response: userLookupResponse{}, Errors: directoryFailures,
	})
	s.staff("GET /guilds/{discordGuildID}/directory/channels", quack.PermissionActionGuildSettingsRead, s.listChannels, Doc{
		ID: "listChannels", Summary: "The guild's channels, for channel pickers",
		Description: "Already in Discord's sidebar order. Threads are left out.",
		Response:    channelListResponse{}, Errors: directoryFailures,
	})

	// Audit log and statistics.
	s.staff("GET /guilds/{discordGuildID}/audit-log", quack.PermissionActionAuditRead, s.listAuditLog, Doc{
		ID: "listAuditLog", Summary: "Search the guild's audit log",
		Description: "Newest first.",
		Query:       auditLogQuery{}, Response: &quack.AuditListResponse{}, Errors: auditFailures,
	})
	s.staff("GET /guilds/{discordGuildID}/statistics", quack.PermissionActionAuditRead, s.getStatistics, Doc{
		ID: "getStatistics", Summary: "Moderation counts over [from, to)",
		Query: statisticsQuery{}, Response: &quack.StaffStatistics{}, Errors: auditFailures,
	})

	// Appeal review. Each write names its idempotency class and capability.
	appeals := s.services.Appeals
	review := quack.PermissionActionAppealReview
	s.appealRead("GET /guilds/{discordGuildID}/appeals", s.listStaffAppeals, Doc{
		ID: "listAppeals", Summary: "The guild's appeals",
		Query: appealListQuery{}, Response: &quack.AppealListResponse{}, Errors: appealFailures,
	})
	s.appealRead("GET /guilds/{discordGuildID}/appeals/{appealID}", s.getStaffAppeal, Doc{
		ID: "getAppeal", Summary: "One appeal",
		Response: appealEnvelope{}, Errors: appealFailures,
	})
	decision := func(id, summary string) Doc {
		return Doc{ID: id, Summary: summary, Body: quack.AppealDecisionInput{}, Response: appealEnvelope{}, Errors: appealFailures}
	}
	s.appealWrite("POST /guilds/{discordGuildID}/appeals/{appealID}/request-information",
		"appeal-request-information", review, decideAppeal(appeals.RequestInformation),
		decision("requestAppealInformation", "Ask the member for more information"))
	s.appealWrite("POST /guilds/{discordGuildID}/appeals/{appealID}/reopen",
		"appeal-reopen", review, decideAppeal(appeals.Reopen), decision("reopenAppeal", "Reopen a decided appeal"))
	s.appealWrite("POST /guilds/{discordGuildID}/appeals/{appealID}/accept",
		"appeal-accept", review, decideAppeal(appeals.Accept),
		decision("acceptAppeal", "Accept an appeal; reversing its actions is a separate request"))
	s.appealWrite("POST /guilds/{discordGuildID}/appeals/{appealID}/reject",
		"appeal-reject", review, decideAppeal(appeals.Reject), decision("rejectAppeal", "Reject an appeal"))
	s.appealWrite("POST /guilds/{discordGuildID}/appeals/{appealID}/close",
		"appeal-close", review, decideAppeal(appeals.Close), decision("closeAppeal", "Close an appeal"))
	s.appealWriteClass("POST /guilds/{discordGuildID}/appeals/{appealID}/reversals",
		classRecovery, "appeal-reversal", review, s.reverseAcceptedAppeal, Doc{
			ID: "reverseAppealAction", Summary: "Queue a reversal of an action on an accepted appeal's case",
			Description: "The body must set confirm.",
			Body:        appealReversalRequest{}, Status: http.StatusAccepted, Response: actionEnvelope{},
			Errors: appealFailures,
		})

	// The signed-in member's own cases and appeals. These do not require
	// membership in the guild, so a banned member can still appeal.
	s.member("GET /members/me/guilds/{guildID}/cases", s.listMemberCases, Doc{
		ID: "listMemberCases", Summary: "The member's cases in a guild, by internal guild ID",
		Query: caseListQuery{}, Response: &quack.MemberCaseListResponse{}, Errors: caseFailures,
	})
	s.member("GET /members/me/cases/{caseID}", s.getMemberCase, Doc{
		ID: "getMemberCase", Summary: "The member-facing view of one of the member's cases",
		Response: memberCaseEnvelope{}, Errors: caseFailures,
	})
	s.memberWrite("POST /members/me/cases/{caseID}/appeal", "appeal-submit", s.submitAppeal, Doc{
		ID: "submitAppeal", Summary: "File the member's one appeal for a case",
		Body: quack.AppealSubmissionInput{}, Status: http.StatusCreated, Response: appealEnvelope{}, Errors: appealFailures,
	})
	s.member("GET /members/me/appeals/{appealID}", s.getMemberAppeal, Doc{
		ID: "getMemberAppeal", Summary: "One of the member's appeals",
		Response: appealEnvelope{}, Errors: appealFailures,
	})
	s.memberWrite("POST /members/me/appeals/{appealID}/information", "appeal-information", s.submitAppealInformation, Doc{
		ID: "submitAppealInformation", Summary: "Answer staff's request for more information",
		Body: quack.AppealInformationInput{}, Response: appealEnvelope{}, Errors: appealFailures,
	})
}

// Handle mounts a read. pattern is a method and a path relative to the
// module prefix, such as "GET /tickets/{ticketID}".
func (m *ModuleMux) Handle(pattern string, d modules.Doc, h http.Handler) {
	m.s.module(pattern, false, d, h)
}

// HandleWrite mounts a write. allowed decides whether the caller may make
// it; a caller who may not gets a 403 before any idempotent replay. The
// request must carry an Idempotency-Key.
func (m *ModuleMux) HandleWrite(pattern string, d modules.Doc, allowed func(*http.Request) bool, h http.Handler) {
	denied := func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, http.StatusForbidden, codeAuthorization, "access denied")
	}
	d.Errors = slices.Concat(d.Errors, []int{http.StatusForbidden})
	m.s.module(pattern, true, d, chain(h,
		allow(allowed, denied),
		m.s.idempotent("optional-module-write", moduleWriteSubject),
	))
}

// handle registers h behind mws, outermost first. p must describe what mws
// enforce.
func (s *Server) handle(pattern string, p Protection, d Doc, h http.HandlerFunc, mws ...middleware) {
	s.mount(pattern, p, d, chain(h, mws...))
}

// staff registers a guild staff route that requires action, under the
// default endpoint class for its method.
func (s *Server) staff(pattern string, action quack.PermissionAction, h http.HandlerFunc, d Doc) {
	s.staffClass(pattern, endpointClass(patternMethod(pattern)), action, h, d)
}

// staffClass registers a guild staff route: endpoint policy, session, live
// authorization for action, and for writes an Idempotency-Key.
func (s *Server) staffClass(pattern, class string, action quack.PermissionAction, h http.HandlerFunc, d Doc) {
	p := Protection{Auth: AuthSession, RateLimited: true, Guild: true}
	mws := []middleware{s.policy(class), s.requireAuth, s.guild(action)}
	if isWrite(patternMethod(pattern)) {
		p.Idempotent = true
		mws = append(mws, s.idempotent("dashboard-write:"+class, s.endpointWriteSubject))
	}
	s.handle(pattern, p, d, h, mws...)
}

// appealRead registers an appeal review read. Appeal routes only require
// guild membership up front; the appeal service checks capabilities, and
// failures use the appeal error messages.
func (s *Server) appealRead(pattern string, h http.HandlerFunc, d Doc) {
	s.appealRoute(pattern, classRead, h, d)
}

// appealWrite registers an appeal review write under the default write
// class.
func (s *Server) appealWrite(pattern, idempotencyClass string, action quack.PermissionAction, h http.HandlerFunc, d Doc) {
	s.appealWriteClass(pattern, classWrite, idempotencyClass, action, h, d)
}

// appealWriteClass registers an appeal review write. The capability is
// checked before idempotency so a demoted reviewer cannot replay.
func (s *Server) appealWriteClass(pattern, class, idempotencyClass string, action quack.PermissionAction, h http.HandlerFunc, d Doc) {
	denied := func(w http.ResponseWriter, r *http.Request) {
		appealErrors.write(w, r, quack.ErrAppealPermissionDenied)
	}
	s.appealRoute(pattern, class, h, d,
		allow(can(action), denied),
		s.idempotent(idempotencyClass, staffAppealWriteSubject),
	)
}

// appealRoute registers h behind the protection every appeal review route
// shares, then mws.
func (s *Server) appealRoute(pattern, class string, h http.HandlerFunc, d Doc, mws ...middleware) {
	p := Protection{Auth: AuthSession, RateLimited: true, Guild: true, Idempotent: isWrite(patternMethod(pattern))}
	s.handle(pattern, p, d, h, append([]middleware{
		s.policy(class),
		s.requireAuth,
		s.guild(""),
		s.limit("appeal-staff", s.cfg.Limits.MemberRead, guildActorSubject),
	}, mws...)...)
}

// member registers a route for the signed-in member's own resources.
func (s *Server) member(pattern string, h http.HandlerFunc, d Doc) {
	s.memberRoute(pattern, Protection{}, h, d)
}

// memberWrite registers a member write, which needs an Idempotency-Key.
func (s *Server) memberWrite(pattern, idempotencyClass string, h http.HandlerFunc, d Doc) {
	s.memberRoute(pattern, Protection{Idempotent: true}, h, d, s.idempotent(idempotencyClass, memberWriteSubject))
}

// memberRoute registers h behind the protection every member route shares,
// then mws. p says whether mws make it idempotent.
func (s *Server) memberRoute(pattern string, p Protection, h http.HandlerFunc, d Doc, mws ...middleware) {
	p.Auth, p.RateLimited = AuthSession, true
	s.handle(pattern, p, d, h, append([]middleware{
		s.policy(endpointClass(patternMethod(pattern))),
		s.requireAuth,
		s.limit("member-reads-and-appeals", s.cfg.Limits.MemberRead, memberSubject),
	}, mws...)...)
}

// module registers a module route under modulePrefix. idempotent says
// whether h already requires an Idempotency-Key.
func (s *Server) module(pattern string, idempotent bool, d modules.Doc, h http.Handler) {
	method, path, _ := strings.Cut(pattern, " ")
	p := Protection{Auth: AuthSession, RateLimited: true, Guild: true, Idempotent: idempotent}
	doc := Doc{
		ID: d.ID, Summary: d.Summary, Description: d.Description,
		Query: d.Query, Body: d.Body, Status: d.Status, Response: d.Response, Errors: d.Errors,
	}
	s.handle(method+" "+modulePrefix+path, p, doc, h.ServeHTTP,
		s.policy(endpointClass(method)),
		s.requireAuth,
		s.guild(""),
		s.limit("optional-modules", s.cfg.Limits.MemberRead, guildActorSubject),
	)
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
