package quack

import (
	"context"
	"slices"
)

// DenyReasonMFARequired is the denial reason for a staff member blocked by
// the guild's two-factor requirement. Adapters match it to tell the member
// how to get access back.
const DenyReasonMFARequired = "mfa_required"

// MFAStatusReader reports what Quack last confirmed with Discord about a
// user's two-factor authentication. The dashboard sign-in records it, since
// only the user's own OAuth token can read it.
type MFAStatusReader interface {
	// DiscordUserMFAEnabled reports whether the user had two-factor
	// authentication on when Quack last checked. A user Quack never checked
	// reports false.
	DiscordUserMFAEnabled(ctx context.Context, discordUserID string) (bool, error)
}

// StaffRoles are the Discord roles a guild configured as Quack staff tiers.
// They live in guild settings and are edited by Manage Guild.
type StaffRoles struct {
	// ModeratorRoleIDs make their holders moderators. Empty means members
	// with Moderate Members are moderators instead; once set, only these
	// roles count.
	ModeratorRoleIDs []string
	// RulesManagerRoleIDs let their holders create, edit, import, export,
	// and archive templates, and nothing else.
	RulesManagerRoleIDs []string
}

// IsModeratorRole reports whether roleID is a configured moderator role.
func (r StaffRoles) IsModeratorRole(roleID string) bool {
	return slices.Contains(r.ModeratorRoleIDs, roleID)
}

// IsDiscordStaff reports whether a member with these guild permissions and
// roles belongs in Quack's staff-only Discord spaces (staff channels and
// ticket threads) and is exempt from the honeypot: Administrator, Moderate
// Members, or a configured moderator role. Owners carry every permission
// bit. It is deliberately broader than the moderator capability, so that
// configuring moderator roles never locks people with Moderate Members out
// of channels Quack already made for them; they could moderate in Discord
// directly anyway.
func (r StaffRoles) IsDiscordStaff(bits uint64, roleIDs []string) bool {
	if bits&(permissionAdministrator|permissionModerateMembers) != 0 {
		return true
	}
	return hasAnyRole(roleIDs, r.ModeratorRoleIDs)
}

// staffAccessInput is everything that decides a member's Quack
// capabilities in a guild.
type staffAccessInput struct {
	permissionBits uint64
	isOwner        bool
	roleIDs        []string
	roles          StaffRoles
	// guildRoleIDs are the guild's current roles, or nil when unknown.
	// Configured roles missing from it were deleted in Discord.
	guildRoleIDs []string
	// guildRequiresMFA is Discord's elevated MFA level for the guild.
	guildRequiresMFA bool
	// actorMFAEnabled is what Quack last confirmed about the member's 2FA.
	actorMFAEnabled bool
}

// staffAccess is what a member may do in Quack, derived by
// deriveStaffAccess.
type staffAccess struct {
	isAdmin        bool
	isModerator    bool
	canManageGuild bool
	canModerate    bool
	canManageRules bool
	// mfaRequired means the member would hold staff capabilities but the
	// guild requires 2FA they have not confirmed, so every capability above
	// and in permissions is withheld.
	mfaRequired bool
	permissions map[PermissionAction]bool
}

// isStaff reports whether the access grants any capability.
func (a staffAccess) isStaff() bool {
	return a.canManageGuild || a.canModerate || a.canManageRules
}

// deriveStaffAccess is the single place Quack turns Discord standing into
// capabilities:
//
//   - The owner and Administrator can do everything.
//   - Manage Guild configures templates, settings, and modules.
//   - A configured moderator role applies templates, reads cases and the
//     audit log, reviews appeals, and works failures and tickets. With no
//     moderator roles configured, or only ones since deleted in Discord,
//     Moderate Members does instead, so nobody is locked out.
//   - A configured rules manager role manages templates only.
//   - Only the owner and Administrators change the moderator roles.
//
// When the guild requires 2FA and the member has not confirmed it with
// Quack, every capability is withheld, owners included.
func deriveStaffAccess(in staffAccessInput) staffAccess {
	isAdmin := in.isOwner || hasAllBits(in.permissionBits, permissionAdministrator)
	canManage := isAdmin || hasAllBits(in.permissionBits, permissionManageGuild)
	moderatorRole := hasAllBits(in.permissionBits, permissionModerateMembers)
	if moderatorRoles := existingRoles(in.roles.ModeratorRoleIDs, in.guildRoleIDs); len(moderatorRoles) > 0 {
		moderatorRole = hasAnyRole(in.roleIDs, moderatorRoles)
	}
	canModerate := isAdmin || moderatorRole
	canManageRules := canManage || hasAnyRole(in.roleIDs, in.roles.RulesManagerRoleIDs)
	access := staffAccess{
		isAdmin:        isAdmin,
		isModerator:    !isAdmin && moderatorRole,
		canManageGuild: canManage,
		canModerate:    canModerate,
		canManageRules: canManageRules,
		permissions: map[PermissionAction]bool{
			PermissionActionCaseCreate:         canModerate,
			PermissionActionCaseRead:           canModerate,
			PermissionActionCaseTemplateRead:   canModerate || canManageRules,
			PermissionActionCaseTemplateWrite:  canManageRules,
			PermissionActionCaseTemplateDelete: canManageRules,
			PermissionActionAppealReview:       canModerate,
			PermissionActionTicketResolve:      canModerate,
			PermissionActionAuditRead:          canModerate,
			PermissionActionGuildSettingsRead:  canManage,
			PermissionActionGuildSettingsWrite: canManage,
			PermissionActionCaseVoid:           canModerate,
			PermissionActionFailureDismiss:     canModerate,
			PermissionActionStaffRolesWrite:    isAdmin,
		},
	}
	if !in.guildRequiresMFA || in.actorMFAEnabled || !access.isStaff() {
		return access
	}
	for action := range access.permissions {
		access.permissions[action] = false
	}
	return staffAccess{mfaRequired: true, permissions: access.permissions}
}

// existingRoles returns the configured roles still in the guild, or all of
// them when guildRoleIDs is nil because the guild's roles are unknown.
func existingRoles(configured, guildRoleIDs []string) []string {
	if guildRoleIDs == nil {
		return configured
	}
	var out []string
	for _, roleID := range configured {
		if slices.Contains(guildRoleIDs, roleID) {
			out = append(out, roleID)
		}
	}
	return out
}

// hasAnyRole reports whether held includes any of configured.
func hasAnyRole(held, configured []string) bool {
	for _, roleID := range configured {
		if slices.Contains(held, roleID) {
			return true
		}
	}
	return false
}
