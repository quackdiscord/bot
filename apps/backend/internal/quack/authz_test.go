package quack_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/quackdiscord/bot/internal/quack"
	"github.com/quackdiscord/bot/internal/store"
)

func TestLiveAuthorizationPermissionMatrix(t *testing.T) {
	tests := []struct {
		name       string
		actorID    string
		ownerID    string
		bits       uint64
		capability quack.PermissionAction
		want       bool
	}{
		{name: "owner full access", actorID: "owner", ownerID: "owner", capability: quack.PermissionActionGuildSettingsWrite, want: true},
		{name: "administrator full access", actorID: "admin", ownerID: "owner", bits: uint64(discordgo.PermissionAdministrator), capability: quack.PermissionActionCaseCreate, want: true},
		{name: "manage guild configures", actorID: "manager", ownerID: "owner", bits: uint64(discordgo.PermissionManageGuild), capability: quack.PermissionActionCaseTemplateWrite, want: true},
		{name: "manage guild reads templates", actorID: "manager", ownerID: "owner", bits: uint64(discordgo.PermissionManageGuild), capability: quack.PermissionActionCaseTemplateRead, want: true},
		{name: "manage guild cannot moderate", actorID: "manager", ownerID: "owner", bits: uint64(discordgo.PermissionManageGuild), capability: quack.PermissionActionCaseCreate, want: false},
		{name: "moderate members creates case", actorID: "mod", ownerID: "owner", bits: uint64(discordgo.PermissionModerateMembers), capability: quack.PermissionActionCaseCreate, want: true},
		{name: "moderate members reads audit", actorID: "mod", ownerID: "owner", bits: uint64(discordgo.PermissionModerateMembers), capability: quack.PermissionActionAuditRead, want: true},
		{name: "moderate members cannot configure", actorID: "mod", ownerID: "owner", bits: uint64(discordgo.PermissionModerateMembers), capability: quack.PermissionActionGuildSettingsWrite, want: false},
		{name: "kick alone cannot create", actorID: "kick", ownerID: "owner", bits: uint64(discordgo.PermissionKickMembers), capability: quack.PermissionActionCaseCreate, want: false},
		{name: "ban alone cannot create", actorID: "ban", ownerID: "owner", bits: uint64(discordgo.PermissionBanMembers), capability: quack.PermissionActionCaseCreate, want: false},
		{name: "ordinary member denied", actorID: "member", ownerID: "owner", bits: uint64(discordgo.PermissionSendMessages), capability: quack.PermissionActionAuditRead, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repositories := newMigratedStore(t)
			snapshot := authorizationSnapshot(tt.ownerID, tt.actorID, tt.bits)
			service := quack.NewGuildService(repositories, fakeDiscordClient{botGuild: &snapshot.Guild, authorization: snapshot})
			guildContext, err := service.ResolveDiscordStaffContext(context.Background(), quack.DiscordStaffContextInput{
				DiscordGuildID: "guild-1", DiscordUserID: tt.actorID,
				PermissionBits: ^uint64(0),
			})
			if err != nil {
				t.Fatalf("resolve live context: %v", err)
			}
			err = guildContext.Authorize(tt.capability)
			if tt.want && err != nil {
				t.Fatalf("expected authorization, got %v", err)
			}
			if !tt.want && !errors.Is(err, quack.ErrAuthorizationDenied) {
				t.Fatalf("expected typed denial, got %v", err)
			}
			if guildContext.PermissionBits != tt.bits {
				t.Fatalf("interaction snapshot granted authority: got %d want live %d", guildContext.PermissionBits, tt.bits)
			}
		})
	}
}

func TestFormerStaffLosesAccessWithoutLosingAttribution(t *testing.T) {
	repositories := newMigratedStore(t)
	present := authorizationSnapshot("owner", "mod", uint64(discordgo.PermissionModerateMembers))
	service := quack.NewGuildService(repositories, fakeDiscordClient{botGuild: &present.Guild, authorization: present})
	first, err := service.ResolveStaffContext(context.Background(), testSession("mod"), "guild-1")
	if err != nil {
		t.Fatalf("resolve present moderator: %v", err)
	}
	staffID := first.Staff.ID

	departed := authorizationSnapshot("owner", "mod", 0)
	departed.Actor.Present = false
	service = quack.NewGuildService(repositories, fakeDiscordClient{botGuild: &departed.Guild, authorization: departed})
	current, err := service.ResolveStaffContext(context.Background(), testSession("mod"), "guild-1")
	if err != nil {
		t.Fatalf("resolve departed moderator: %v", err)
	}
	if current.Staff == nil || current.Staff.ID != staffID || current.Staff.LastSeenPermissionBits != uint64(discordgo.PermissionModerateMembers) {
		t.Fatalf("expected preserved attribution cache, got %+v", current.Staff)
	}
	ctx := quack.ContextWithTrace(context.Background(), "req-former", "corr-former")
	if err := current.Authorize(quack.PermissionActionCaseCreate); !errors.Is(err, quack.ErrAuthorizationDenied) {
		t.Fatalf("expected former staff denial, got %v", err)
	}
	if audits, err := repositories.ListAuditLogEntries(ctx, current.Guild.ID); err != nil || len(audits) != 0 {
		t.Fatalf("a refusal about the actor was audited: audits=%+v err=%v", audits, err)
	}
}

func TestCasePreflightMatrixAndNoPartialCommit(t *testing.T) {
	moderate := uint64(discordgo.PermissionModerateMembers)
	allActions := moderate | uint64(discordgo.PermissionKickMembers) | uint64(discordgo.PermissionBanMembers)
	tests := []struct {
		name               string
		action             quack.ActionType
		targetID           string
		mutate             func(*quack.DiscordGuildAuthorization)
		mutateAfterResolve bool
		wantReason         string
		wantOK             bool
	}{
		{name: "valid warning", wantOK: true},
		{name: "valid timeout", action: quack.ActionTimeoutUser, wantOK: true},
		{name: "valid kick", action: quack.ActionKickUser, wantOK: true},
		{name: "valid ban", action: quack.ActionBanUser, wantOK: true},
		{name: "administrator can ban", action: quack.ActionBanUser, mutate: func(s *quack.DiscordGuildAuthorization) {
			s.Actor.PermissionBits = uint64(discordgo.PermissionAdministrator)
		}, wantOK: true},
		{name: "administrator bot can ban", action: quack.ActionBanUser, mutate: func(s *quack.DiscordGuildAuthorization) {
			s.Bot.PermissionBits = uint64(discordgo.PermissionAdministrator)
		}, wantOK: true},
		{name: "self", targetID: "mod", wantReason: "self_target"},
		{name: "bot account", mutate: func(s *quack.DiscordGuildAuthorization) { s.Target.Bot = true }, wantReason: "bot_target"},
		{name: "quack bot", targetID: "quack", wantReason: "bot_target"},
		{name: "guild owner", targetID: "owner", wantReason: "guild_owner_target"},
		{name: "departed target", mutate: func(s *quack.DiscordGuildAuthorization) { s.Target.Present = false }, wantReason: "target_not_in_guild"},
		{name: "actor peer role", mutate: func(s *quack.DiscordGuildAuthorization) { s.Target.TopRolePosition = s.Actor.TopRolePosition }, wantReason: "actor_hierarchy"},
		{name: "actor higher role", mutate: func(s *quack.DiscordGuildAuthorization) { s.Target.TopRolePosition = s.Actor.TopRolePosition + 1 }, wantReason: "actor_hierarchy"},
		{name: "bot peer role", mutate: func(s *quack.DiscordGuildAuthorization) {
			s.Actor.TopRolePosition = 30
			s.Target.TopRolePosition = s.Bot.TopRolePosition
		}, wantReason: "bot_hierarchy"},
		// Moderators apply templates; Quack acts with its own permissions.
		{name: "moderator without kick can kick", action: quack.ActionKickUser, mutate: func(s *quack.DiscordGuildAuthorization) { s.Actor.PermissionBits = moderate }, wantOK: true},
		{name: "moderator without ban can ban", action: quack.ActionBanUser, mutate: func(s *quack.DiscordGuildAuthorization) { s.Actor.PermissionBits = moderate }, wantOK: true},
		{name: "not a moderator", action: quack.ActionTimeoutUser, mutate: func(s *quack.DiscordGuildAuthorization) {
			s.Actor.PermissionBits = uint64(discordgo.PermissionBanMembers)
		}, mutateAfterResolve: true, wantReason: "permission_required"},
		{name: "bot missing timeout", action: quack.ActionTimeoutUser, mutate: func(s *quack.DiscordGuildAuthorization) { s.Bot.PermissionBits = allActions &^ moderate }, wantReason: "bot_permission_required"},
		{name: "bot missing kick", action: quack.ActionKickUser, mutate: func(s *quack.DiscordGuildAuthorization) {
			s.Bot.PermissionBits = allActions &^ uint64(discordgo.PermissionKickMembers)
		}, wantReason: "bot_permission_required"},
		{name: "bot missing ban", action: quack.ActionBanUser, mutate: func(s *quack.DiscordGuildAuthorization) {
			s.Bot.PermissionBits = allActions &^ uint64(discordgo.PermissionBanMembers)
		}, wantReason: "bot_permission_required"},
		{name: "bot departed", mutate: func(s *quack.DiscordGuildAuthorization) { s.Bot.Present = false }, wantReason: "bot_not_in_guild"},
		{name: "cross guild", mutate: func(s *quack.DiscordGuildAuthorization) { s.Guild.ID = "guild-2" }, mutateAfterResolve: true, wantReason: "guild_mismatch"},
		{name: "cross user target", mutate: func(s *quack.DiscordGuildAuthorization) { s.Target.DiscordUserID = "other-target" }, mutateAfterResolve: true, wantReason: "identity_mismatch"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repositories := newMigratedStore(t)
			targetID := tt.targetID
			if targetID == "" {
				targetID = "target"
			}
			snapshot := authorizationSnapshot("owner", "mod", allActions)
			snapshot.Target = &quack.DiscordMemberAuthorization{DiscordUserID: targetID, Present: true, TopRolePosition: 1}
			if tt.mutate != nil && !tt.mutateAfterResolve {
				tt.mutate(snapshot)
			}
			services := quack.New(quack.Deps{Store: repositories, Guilds: fakeDiscordClient{botGuild: &snapshot.Guild, authorization: snapshot}})
			guildContext, err := services.Guilds.ResolveStaffContext(context.Background(), testSession("mod"), "guild-1")
			if err != nil {
				t.Fatalf("resolve actor: %v", err)
			}
			if tt.mutate != nil && tt.mutateAfterResolve {
				tt.mutate(snapshot)
			}
			templateID := createAuthorizationTemplate(t, repositories, guildContext.Guild.ID, tt.action)
			ctx := quack.ContextWithTrace(context.Background(), "req-case", "corr-case")
			_, err = services.Cases.Create(ctx, guildContext, quack.CaseInput{TemplateID: templateID, TargetDiscordUserID: targetID, Source: quack.CaseSourceDashboard})
			if tt.wantOK {
				if err != nil {
					t.Fatalf("expected case success, got %v", err)
				}
				cases, listErr := repositories.ListCases(ctx, guildContext.Guild.ID)
				if listErr != nil || len(cases) != 1 {
					t.Fatalf("expected one committed case, cases=%+v err=%v", cases, listErr)
				}
				return
			}
			if !errors.Is(err, quack.ErrAuthorizationDenied) || !strings.Contains(err.Error(), tt.wantReason) {
				t.Fatalf("expected %q typed denial, got %v", tt.wantReason, err)
			}
			cases, listErr := repositories.ListCases(ctx, guildContext.Guild.ID)
			if listErr != nil || len(cases) != 0 {
				t.Fatalf("denial committed a case: cases=%+v err=%v", cases, listErr)
			}
			// Only denials caused by Quack's own Discord access are audited.
			audits, auditErr := repositories.ListAuditLogEntries(ctx, guildContext.Guild.ID)
			if botCaused := tt.wantReason == "bot_permission_required" || tt.wantReason == "bot_hierarchy" ||
				tt.wantReason == "bot_not_in_guild"; !botCaused {
				if auditErr != nil || len(audits) != 0 {
					t.Fatalf("a refusal about the actor or target was audited: audits=%+v err=%v", audits, auditErr)
				}
				return
			}
			if auditErr != nil || len(audits) != 1 {
				t.Fatalf("expected exactly one denial audit, audits=%+v err=%v", audits, auditErr)
			}
			if audits[0].Action != "authorization.denied" || audits[0].Result != quack.AuditResultDenied || audits[0].FailureReason != tt.wantReason || audits[0].RequestID != "req-case" || audits[0].CorrelationID != "corr-case" || audits[0].Source != quack.AuditSourceWeb {
				t.Fatalf("unexpected denial audit: %+v", audits[0])
			}
		})
	}
}

// TestStaffRolesAndMFAGateModeration checks moderator roles end to end:
// a role moderator without any moderation permission can time out, Moderate
// Members alone stops counting once roles are set, and a guild requiring
// 2FA refuses staff until Quack has confirmed theirs, auditing why.
func TestStaffRolesAndMFAGateModeration(t *testing.T) {
	ctx := context.Background()
	repositories := newMigratedStore(t)
	guildID := configureStaffRoles(t, repositories, "guild-1", quack.StaffRoles{ModeratorRoleIDs: []string{"mods"}})
	templateID := createAuthorizationTemplate(t, repositories, guildID, quack.ActionTimeoutUser)
	snapshot := authorizationSnapshot("owner", "mod", 0)
	snapshot.Actor.RoleIDs = []string{"mods"}
	snapshot.Target = &quack.DiscordMemberAuthorization{DiscordUserID: "target", Present: true, TopRolePosition: 1}
	services := quack.New(quack.Deps{Store: repositories, Guilds: fakeDiscordClient{botGuild: &snapshot.Guild, authorization: snapshot}})
	create := func() error {
		staff, err := services.Guilds.ResolveStaffContext(ctx, testSession("mod"), "guild-1")
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if err := staff.Authorize(quack.PermissionActionCaseCreate); err != nil {
			return err
		}
		_, err = services.Cases.Create(ctx, staff, quack.CaseInput{
			TemplateID: templateID, TargetDiscordUserID: "target", Source: quack.CaseSourceDashboard, IdempotencyKey: quack.NewID(),
		})
		return err
	}
	if err := create(); err != nil {
		t.Fatalf("role moderator timeout: %v", err)
	}

	snapshot.Actor.RoleIDs = nil
	snapshot.Actor.PermissionBits = uint64(discordgo.PermissionModerateMembers)
	if err := create(); !errors.Is(err, quack.ErrAuthorizationDenied) || !strings.Contains(err.Error(), "permission_required") {
		t.Fatalf("Moderate Members without the role = %v, want permission_required", err)
	}

	snapshot.Actor.RoleIDs = []string{"mods"}
	snapshot.Guild.MFARequired = true
	err := create()
	if !errors.Is(err, quack.ErrAuthorizationDenied) || !strings.Contains(err.Error(), quack.DenyReasonMFARequired) {
		t.Fatalf("unconfirmed 2FA = %v, want mfa_required", err)
	}
	audits, auditErr := repositories.ListAuditLogEntries(ctx, guildID)
	if auditErr != nil || len(audits) == 0 || audits[len(audits)-1].Result == quack.AuditResultDenied {
		t.Fatalf("2FA refusal was audited: %+v, %v", audits, auditErr)
	}
	if err := repositories.RecordDiscordUserMFA(ctx, "mod", true, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := create(); err != nil {
		t.Fatalf("confirmed 2FA timeout: %v", err)
	}

	// A rules manager gets templates and nothing else.
	configureStaffRoles(t, repositories, "guild-1", quack.StaffRoles{ModeratorRoleIDs: []string{"mods"}, RulesManagerRoleIDs: []string{"rules"}})
	snapshot.Actor.RoleIDs = []string{"rules"}
	snapshot.Actor.PermissionBits = 0
	staff, err := services.Guilds.ResolveStaffContext(ctx, testSession("mod"), "guild-1")
	if err != nil {
		t.Fatal(err)
	}
	if !staff.Can(quack.PermissionActionCaseTemplateWrite) || !staff.Can(quack.PermissionActionCaseTemplateDelete) ||
		staff.Can(quack.PermissionActionCaseCreate) || staff.Can(quack.PermissionActionGuildSettingsRead) {
		t.Fatalf("rules manager permissions = %v", staff.Permissions)
	}
}

func authorizationSnapshot(ownerID, actorID string, actorPermissions uint64) *quack.DiscordGuildAuthorization {
	return &quack.DiscordGuildAuthorization{
		Guild: quack.DiscordBotGuild{ID: "guild-1", Name: "Guild", OwnerID: ownerID},
		Actor: quack.DiscordMemberAuthorization{DiscordUserID: actorID, DisplayName: "Live Actor", PermissionBits: actorPermissions, TopRolePosition: 10, Present: true},
		Bot:   quack.DiscordMemberAuthorization{DiscordUserID: "quack", PermissionBits: ^uint64(0), TopRolePosition: 20, Present: true, Bot: true},
	}
}

func createAuthorizationTemplate(t *testing.T, repositories *store.Store, guildID string, actionType quack.ActionType) string {
	t.Helper()
	actions := []quack.CaseTemplateLevelAction{}
	if actionType != "" {
		actions = append(actions, quack.CaseTemplateLevelAction{ActionType: actionType, ConfigJSON: `{}`})
	}
	created, err := repositories.CreateCaseTemplate(context.Background(), quack.CreateCaseTemplateParams{
		Template: quack.CaseTemplate{GuildID: guildID, Slug: "authorization", Name: "Authorization", ReasonTemplate: "Policy", CreatedByDiscordUserID: "owner", UpdatedByDiscordUserID: "owner"},
		Levels:   []quack.ExpandedCaseTemplateLevel{{Level: quack.CaseTemplateLevel{Name: "Default", Position: 1, IsDefault: true}, Actions: actions}},
	})
	if err != nil {
		t.Fatalf("create authorization template: %v", err)
	}
	return created.Template.ID
}

// TestStaffGatesAndRecords checks the membership-only check, the staff
// gate, and the 2FA gate, and that only staff get a staff record.
func TestStaffGatesAndRecords(t *testing.T) {
	ctx := context.Background()
	repositories := newMigratedStore(t)
	snapshot := authorizationSnapshot("owner", "member", uint64(discordgo.PermissionSendMessages))
	service := quack.NewGuildService(repositories, fakeDiscordClient{botGuild: &snapshot.Guild, authorization: snapshot})
	resolve := func() *quack.GuildStaffContext {
		t.Helper()
		staff, err := service.ResolveStaffContext(ctx, testSession(snapshot.Actor.DiscordUserID), "guild-1")
		if err != nil {
			t.Fatal(err)
		}
		return staff
	}
	denials := func(guildID string) []string {
		t.Helper()
		audits, err := repositories.ListAuditLogEntries(ctx, guildID)
		if err != nil {
			t.Fatal(err)
		}
		var reasons []string
		for _, audit := range audits {
			reasons = append(reasons, audit.FailureReason)
		}
		return reasons
	}

	member := resolve()
	if record, err := repositories.GetStaffMember(ctx, member.Guild.ID, "member"); err != nil || record != nil {
		t.Fatalf("a member who is not staff got a staff record: %+v, %v", record, err)
	}
	if member.Staff == nil || member.Staff.DiscordUserID != "member" || member.Staff.ID != "" {
		t.Fatalf("member attribution = %+v, want an unsaved record", member.Staff)
	}
	if err := member.Authorize(""); err != nil {
		t.Fatalf("membership check refused a member: %v", err)
	}
	if err := member.RequireConfirmedMFA(); err != nil {
		t.Fatalf("2FA gate refused a member who is not staff: %v", err)
	}
	if err := member.AuthorizeStaff(); !errors.Is(err, quack.ErrAuthorizationDenied) {
		t.Fatalf("staff gate let a member through: %v", err)
	}
	if got := denials(member.Guild.ID); len(got) != 0 {
		t.Fatalf("denials = %v, want none: refusals about the actor are not audited", got)
	}

	// A moderator held back only by 2FA keeps member access, gets no new
	// record, and is refused by both staff gates with mfa_required.
	snapshot.Actor.DiscordUserID = "blocked"
	snapshot.Actor.PermissionBits = uint64(discordgo.PermissionModerateMembers)
	snapshot.Guild.MFARequired = true
	blocked := resolve()
	if !blocked.MFARequired || blocked.Can(quack.PermissionActionCaseRead) {
		t.Fatalf("blocked moderator = %+v", blocked)
	}
	if record, err := repositories.GetStaffMember(ctx, blocked.Guild.ID, "blocked"); err != nil || record != nil {
		t.Fatalf("2FA-blocked moderator got a staff record: %+v, %v", record, err)
	}
	if err := blocked.Authorize(""); err != nil {
		t.Fatalf("membership check refused a 2FA-blocked member: %v", err)
	}
	for name, gate := range map[string]func() error{
		"capability": func() error {
			return blocked.Authorize(quack.PermissionActionCaseRead)
		},
		"staff": func() error { return blocked.AuthorizeStaff() },
		"2FA":   func() error { return blocked.RequireConfirmedMFA() },
	} {
		if err := gate(); err == nil || !strings.Contains(err.Error(), quack.DenyReasonMFARequired) {
			t.Errorf("%s gate = %v, want mfa_required", name, err)
		}
	}

	snapshot.Guild.MFARequired = false
	moderator := resolve()
	if record, err := repositories.GetStaffMember(ctx, moderator.Guild.ID, "blocked"); err != nil || record == nil {
		t.Fatalf("moderator got no staff record: %v", err)
	}
	if err := moderator.AuthorizeStaff(); err != nil {
		t.Fatalf("staff gate refused a moderator: %v", err)
	}
}
