package quack_test

import (
	"context"
	"errors"
	"strings"
	"testing"

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
			err = service.Authorize(context.Background(), guildContext, tt.capability, quack.AuditSourceDiscord)
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
	if err := service.Authorize(ctx, current, quack.PermissionActionCaseCreate, quack.AuditSourceAPI); !errors.Is(err, quack.ErrAuthorizationDenied) {
		t.Fatalf("expected former staff denial, got %v", err)
	}
	audits, err := repositories.ListAuditLogEntries(ctx, current.Guild.ID)
	if err != nil || len(audits) != 1 {
		t.Fatalf("expected one denial audit, audits=%+v err=%v", audits, err)
	}
	if audits[0].ActorDiscordUserID != "mod" || audits[0].ResourceID != string(quack.PermissionActionCaseCreate) || audits[0].RequestID != "req-former" || audits[0].CorrelationID != "corr-former" || audits[0].Result != quack.AuditResultDenied {
		t.Fatalf("unexpected denial audit: %+v", audits[0])
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
		{name: "actor missing kick", action: quack.ActionKickUser, mutate: func(s *quack.DiscordGuildAuthorization) { s.Actor.PermissionBits = moderate }, wantReason: "permission_required"},
		{name: "actor missing ban", action: quack.ActionBanUser, mutate: func(s *quack.DiscordGuildAuthorization) { s.Actor.PermissionBits = moderate }, wantReason: "permission_required"},
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
			audits, auditErr := repositories.ListAuditLogEntries(ctx, guildContext.Guild.ID)
			if auditErr != nil || len(audits) != 1 {
				t.Fatalf("expected exactly one denial audit, audits=%+v err=%v", audits, auditErr)
			}
			if audits[0].Action != "authorization.denied" || audits[0].Result != quack.AuditResultDenied || audits[0].FailureReason != tt.wantReason || audits[0].RequestID != "req-case" || audits[0].CorrelationID != "corr-case" || audits[0].Source != quack.AuditSourceWeb {
				t.Fatalf("unexpected denial audit: %+v", audits[0])
			}
		})
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
