package quack

import "testing"

func TestDeriveStaffAccess(t *testing.T) {
	const (
		moderate = permissionModerateMembers
		manage   = permissionManageGuild
		admin    = permissionAdministrator
	)
	moderators := StaffRoles{ModeratorRoleIDs: []string{"mods"}}
	rules := StaffRoles{RulesManagerRoleIDs: []string{"rules"}}
	both := StaffRoles{ModeratorRoleIDs: []string{"mods"}, RulesManagerRoleIDs: []string{"rules"}}

	type want struct{ admin, moderator, manage, moderate, manageRules, mfa bool }
	guild := []string{"guild", "mods", "rules", "members"}
	tests := []struct {
		name string
		in   staffAccessInput
		want want
	}{
		{"owner", staffAccessInput{isOwner: true, roles: both},
			want{admin: true, manage: true, moderate: true, manageRules: true}},
		{"administrator", staffAccessInput{permissionBits: admin, roles: both},
			want{admin: true, manage: true, moderate: true, manageRules: true}},
		{"manage guild", staffAccessInput{permissionBits: manage},
			want{manage: true, manageRules: true}},
		{"moderate members without roles configured", staffAccessInput{permissionBits: moderate},
			want{moderator: true, moderate: true}},
		{"moderate members once roles are configured", staffAccessInput{permissionBits: moderate, roles: moderators},
			want{}},
		{"moderator role", staffAccessInput{roleIDs: []string{"other", "mods"}, roles: moderators},
			want{moderator: true, moderate: true}},
		{"moderator role grants nothing unconfigured", staffAccessInput{roleIDs: []string{"mods"}},
			want{}},
		{"rules manager role", staffAccessInput{roleIDs: []string{"rules"}, roles: rules},
			want{manageRules: true}},
		{"rules manager role with moderate members", staffAccessInput{permissionBits: moderate, roleIDs: []string{"rules"}, roles: rules},
			want{moderator: true, moderate: true, manageRules: true}},
		{"manage guild and moderator role", staffAccessInput{permissionBits: manage, roleIDs: []string{"mods"}, roles: both},
			want{moderator: true, manage: true, moderate: true, manageRules: true}},
		{"ordinary member", staffAccessInput{roleIDs: []string{"members"}, roles: both},
			want{}},
		{"2FA confirmed", staffAccessInput{roleIDs: []string{"mods"}, roles: moderators, guildRequiresMFA: true, actorMFAEnabled: true},
			want{moderator: true, moderate: true}},
		{"2FA missing for moderator", staffAccessInput{roleIDs: []string{"mods"}, roles: moderators, guildRequiresMFA: true},
			want{mfa: true}},
		{"2FA missing for owner", staffAccessInput{isOwner: true, guildRequiresMFA: true},
			want{mfa: true}},
		{"2FA missing for rules manager", staffAccessInput{roleIDs: []string{"rules"}, roles: rules, guildRequiresMFA: true},
			want{mfa: true}},
		{"2FA missing for ordinary member", staffAccessInput{guildRequiresMFA: true},
			want{}},
		{"moderator role that still exists", staffAccessInput{roleIDs: []string{"mods"}, roles: moderators, guildRoleIDs: guild},
			want{moderator: true, moderate: true}},
		{"moderate members beside a live moderator role", staffAccessInput{permissionBits: moderate, roles: moderators, guildRoleIDs: guild},
			want{}},
		{"moderate members once every moderator role is deleted", staffAccessInput{permissionBits: moderate,
			roles: StaffRoles{ModeratorRoleIDs: []string{"gone"}}, guildRoleIDs: guild},
			want{moderator: true, moderate: true}},
		{"deleted moderator roles beside a live one", staffAccessInput{permissionBits: moderate,
			roles: StaffRoles{ModeratorRoleIDs: []string{"gone", "mods"}}, guildRoleIDs: guild},
			want{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deriveStaffAccess(tt.in)
			gotWant := want{got.isAdmin, got.isModerator, got.canManageGuild, got.canModerate, got.canManageRules, got.mfaRequired}
			if gotWant != tt.want {
				t.Fatalf("access = %+v, want %+v", gotWant, tt.want)
			}
			moderation := []PermissionAction{
				PermissionActionCaseCreate, PermissionActionCaseRead, PermissionActionAppealReview, PermissionActionTicketResolve,
				PermissionActionAuditRead, PermissionActionCaseVoid, PermissionActionFailureDismiss,
			}
			for _, action := range moderation {
				if got.permissions[action] != tt.want.moderate {
					t.Errorf("%s = %v, want %v", action, got.permissions[action], tt.want.moderate)
				}
			}
			for _, action := range []PermissionAction{PermissionActionCaseTemplateWrite, PermissionActionCaseTemplateDelete} {
				if got.permissions[action] != tt.want.manageRules {
					t.Errorf("%s = %v, want %v", action, got.permissions[action], tt.want.manageRules)
				}
			}
			if read := got.permissions[PermissionActionCaseTemplateRead]; read != (tt.want.moderate || tt.want.manageRules) {
				t.Errorf("%s = %v", PermissionActionCaseTemplateRead, read)
			}
			for _, action := range []PermissionAction{PermissionActionGuildSettingsRead, PermissionActionGuildSettingsWrite} {
				if got.permissions[action] != tt.want.manage {
					t.Errorf("%s = %v, want %v", action, got.permissions[action], tt.want.manage)
				}
			}
			if got.permissions[PermissionActionStaffRolesWrite] != tt.want.admin {
				t.Errorf("%s = %v, want %v", PermissionActionStaffRolesWrite, got.permissions[PermissionActionStaffRolesWrite], tt.want.admin)
			}
			if len(got.permissions) != 13 {
				t.Errorf("%d capabilities, want all 13 listed", len(got.permissions))
			}
		})
	}
}

func TestIsDiscordStaff(t *testing.T) {
	roles := StaffRoles{ModeratorRoleIDs: []string{"mods"}}
	for _, tt := range []struct {
		name    string
		bits    uint64
		roleIDs []string
		want    bool
	}{
		{"administrator", permissionAdministrator, nil, true},
		{"moderate members beside configured roles", permissionModerateMembers, nil, true},
		{"moderator role", 0, []string{"mods"}, true},
		{"manage guild", permissionManageGuild, nil, false},
		{"other role", 0, []string{"members"}, false},
	} {
		if got := roles.IsDiscordStaff(tt.bits, tt.roleIDs); got != tt.want {
			t.Errorf("%s: IsDiscordStaff = %v, want %v", tt.name, got, tt.want)
		}
	}
}
