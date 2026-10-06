package quack

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// Denial reasons carried on AuthorizationError. Only the ones about Quack's
// own Discord access (see isBotDenial) are written to the audit log.
const (
	denyActorNotInGuild    = "actor_not_in_guild"
	denyPermissionRequired = "permission_required"
	denySelfTarget         = "self_target"
	denyBotTarget          = "bot_target"
	denyOwnerTarget        = "guild_owner_target"
	denyTargetNotInGuild   = "target_not_in_guild"
	denyActorHierarchy     = "actor_hierarchy"
	denyBotHierarchy       = "bot_hierarchy"
	denyBotPermission      = "bot_permission_required"
	denyBotNotInGuild      = "bot_not_in_guild"
	denyGuildMismatch      = "guild_mismatch"
	denyIdentityMismatch   = "identity_mismatch"
	denyInvalidReversal    = "invalid_reversal"
)

// isBotDenial reports whether reason refuses a request because of Quack's
// own Discord access: a missing permission, its role position, or its
// absence from the guild. Those are worth an audit entry, since an admin
// has to fix them. Refusals about the person acting (their capabilities,
// 2FA, role position, chosen target, or a mismatched request) are only
// answered, never audited.
func isBotDenial(reason string) bool {
	switch reason {
	case denyBotPermission, denyBotHierarchy, denyBotNotInGuild:
		return true
	}
	return false
}

// isActorDenial reports whether err refuses the person acting rather than
// Quack, so that callers auditing failures can skip it: a service's
// permission sentinel, or an AuthorizationError whose reason is not a bot
// reason.
func isActorDenial(err error) bool {
	if denial, ok := errors.AsType[*AuthorizationError](err); ok {
		return !isBotDenial(denial.Reason)
	}
	for _, target := range []error{
		ErrAuthorizationDenied, ErrCasePermissionDenied, ErrTemplatePermissionDenied, ErrGuildSettingsPermissionDenied,
		ErrAppealPermissionDenied, ErrAuditPermissionDenied, ErrStatisticsPermissionDenied,
	} {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}

// Authorize checks that the actor is still in the guild and holds
// capability, refusing a member the guild's 2FA requirement blocks with
// DenyReasonMFARequired. An empty capability only checks membership, so
// members can use what is theirs, such as their own tickets, whatever
// their staff standing; staff-only routes without a single capability use
// AuthorizeStaff or RequireConfirmedMFA as well. Refusals are not audited.
func (c *GuildStaffContext) Authorize(capability PermissionAction) error {
	var reason string
	switch {
	case c == nil || c.Guild == nil || !c.Live.Actor.Present:
		reason = denyActorNotInGuild
	case capability == "":
		return nil
	case c.MFARequired:
		reason = DenyReasonMFARequired
	case !c.Can(capability):
		reason = denyPermissionRequired
	default:
		return nil
	}
	return &AuthorizationError{Capability: capability, Reason: reason}
}

// AuthorizeStaff checks that the actor is in the guild and holds at least
// one staff capability, for staff-only surfaces that check finer
// capabilities later, such as Discord's staff commands. A member the
// guild's 2FA requirement blocks is refused with DenyReasonMFARequired.
// Refusals are not audited.
func (c *GuildStaffContext) AuthorizeStaff() error {
	switch {
	case c == nil || c.Guild == nil || !c.Live.Actor.Present:
		return &AuthorizationError{Reason: denyActorNotInGuild}
	case c.MFARequired:
		return &AuthorizationError{Reason: DenyReasonMFARequired}
	case !c.isStaff():
		return &AuthorizationError{Reason: denyPermissionRequired}
	}
	return nil
}

// RequireConfirmedMFA refuses, with DenyReasonMFARequired, a member who
// would be staff but for the guild's 2FA requirement. Everyone else passes,
// members who are not staff included, so it suits staff-only routes that
// still answer members, like the dashboard's guild overview.
func (c *GuildStaffContext) RequireConfirmedMFA() error {
	if c == nil || !c.MFARequired {
		return nil
	}
	return &AuthorizationError{Reason: DenyReasonMFARequired}
}

// PreflightCase re-checks Discord right before a case is stored: the actor
// can moderate, both actor and bot are above the target in Discord's role
// hierarchy, and the bot holds the permission actionType needs. The actor
// does not need that permission: being a moderator is what lets them apply
// a template, and Quack acts with its own permissions. It refreshes
// guildContext with what it fetched.
func (s *GuildService) PreflightCase(ctx context.Context, guildContext *GuildStaffContext, targetDiscordUserID string, actionType ActionType) error {
	ctx = ensureTraceContext(ctx)
	if s.discord == nil || guildContext == nil || guildContext.Guild == nil {
		return ErrAuthorizationUnavailable
	}
	actorID := guildContext.actorID()
	targetDiscordUserID = strings.TrimSpace(targetDiscordUserID)
	snapshot, err := s.discord.GuildAuthorization(ctx, guildContext.Guild.DiscordGuildID, actorID, targetDiscordUserID)
	if err != nil || snapshot == nil {
		return ErrAuthorizationUnavailable
	}
	if snapshot.Guild.ID != guildContext.Guild.DiscordGuildID {
		return caseDenial(actionType, denyGuildMismatch)
	}
	if snapshot.Actor.DiscordUserID != actorID || snapshot.Target == nil || snapshot.Target.DiscordUserID != targetDiscordUserID {
		return caseDenial(actionType, denyIdentityMismatch)
	}
	if err := s.loadStaffAccess(ctx, guildContext, snapshot); err != nil {
		return ErrAuthorizationUnavailable
	}
	guildContext.applyLive(snapshot, actorID)

	actor, bot, target := snapshot.Actor, snapshot.Bot, *snapshot.Target
	actorIsOwner := actor.DiscordUserID == snapshot.Guild.OwnerID
	switch {
	case !actor.Present:
		return caseDenial(actionType, denyActorNotInGuild)
	case guildContext.MFARequired:
		return caseDenial(actionType, DenyReasonMFARequired)
	case !guildContext.Can(PermissionActionCaseCreate):
		return caseDenial(actionType, denyPermissionRequired)
	case !bot.Present:
		return caseDenial(actionType, denyBotNotInGuild)
	case !target.Present:
		return caseDenial(actionType, denyTargetNotInGuild)
	case target.DiscordUserID == actor.DiscordUserID:
		return caseDenial(actionType, denySelfTarget)
	case target.Bot || target.DiscordUserID == bot.DiscordUserID:
		return caseDenial(actionType, denyBotTarget)
	case target.DiscordUserID == snapshot.Guild.OwnerID:
		return caseDenial(actionType, denyOwnerTarget)
	case !actorIsOwner && target.TopRolePosition >= actor.TopRolePosition:
		return caseDenial(actionType, denyActorHierarchy)
	case target.TopRolePosition >= bot.TopRolePosition:
		return caseDenial(actionType, denyBotHierarchy)
	}

	if required := actionPermission(actionType); required != 0 && !hasDiscordPermission(bot.PermissionBits, required) {
		return caseDenial(actionType, denyBotPermission)
	}
	return nil
}

// PreflightSystemCase is PreflightCase for cases Quack opens itself
// (honeypot). There is no staff actor to check, but every target-safety and
// bot check still applies.
func (s *GuildService) PreflightSystemCase(ctx context.Context, guildContext *GuildStaffContext, targetDiscordUserID string, actionType ActionType) error {
	ctx = ensureTraceContext(ctx)
	if s.discord == nil || guildContext == nil || guildContext.Guild == nil {
		return ErrAuthorizationUnavailable
	}
	targetDiscordUserID = strings.TrimSpace(targetDiscordUserID)
	snapshot, err := s.discord.GuildAuthorization(ctx, guildContext.Guild.DiscordGuildID, "", targetDiscordUserID)
	if err != nil || snapshot == nil {
		return ErrAuthorizationUnavailable
	}
	if snapshot.Guild.ID != guildContext.Guild.DiscordGuildID {
		return caseDenial(actionType, denyGuildMismatch)
	}
	if snapshot.Target == nil || snapshot.Target.DiscordUserID != targetDiscordUserID {
		return caseDenial(actionType, denyIdentityMismatch)
	}
	guildContext.Live = *snapshot
	guildContext.PermissionBits = 0

	bot, target := snapshot.Bot, *snapshot.Target
	switch {
	case !bot.Present:
		return caseDenial(actionType, denyBotNotInGuild)
	case !target.Present:
		return caseDenial(actionType, denyTargetNotInGuild)
	case target.Bot || target.DiscordUserID == bot.DiscordUserID:
		return caseDenial(actionType, denyBotTarget)
	case target.DiscordUserID == snapshot.Guild.OwnerID:
		return caseDenial(actionType, denyOwnerTarget)
	case target.TopRolePosition >= bot.TopRolePosition:
		return caseDenial(actionType, denyBotHierarchy)
	}
	if required := actionPermission(actionType); required != 0 && !hasDiscordPermission(bot.PermissionBits, required) {
		return caseDenial(actionType, denyBotPermission)
	}
	return nil
}

// PreflightReversal re-checks Discord before a timeout removal or unban: the
// actor can moderate and, for a timeout, actor and bot are above the
// target. As with PreflightCase, only the bot needs the Discord permission.
// Unlike PreflightCase, an unban target may have left the guild.
func (s *GuildService) PreflightReversal(ctx context.Context, guildContext *GuildStaffContext, targetDiscordUserID string, actionType ActionType) error {
	if actionType != ActionRemoveTimeout && actionType != ActionUnbanUser {
		return caseDenial(actionType, denyInvalidReversal)
	}
	if s.discord == nil || guildContext == nil || guildContext.Guild == nil {
		return ErrAuthorizationUnavailable
	}
	actorID := guildContext.actorID()
	snapshot, err := s.discord.GuildAuthorization(ctx, guildContext.Guild.DiscordGuildID, actorID, targetDiscordUserID)
	if err != nil || snapshot == nil {
		return ErrAuthorizationUnavailable
	}
	if err := s.loadStaffAccess(ctx, guildContext, snapshot); err != nil {
		return ErrAuthorizationUnavailable
	}
	guildContext.applyLive(snapshot, actorID)

	actorIsOwner := actorID == snapshot.Guild.OwnerID
	switch {
	case snapshot.Actor.Present && guildContext.MFARequired:
		return caseDenial(actionType, DenyReasonMFARequired)
	case !snapshot.Actor.Present || !guildContext.Can(PermissionActionCaseCreate):
		return caseDenial(actionType, denyPermissionRequired)
	case !snapshot.Bot.Present:
		return caseDenial(actionType, denyBotNotInGuild)
	case !hasDiscordPermission(snapshot.Bot.PermissionBits, actionPermission(actionType)):
		return caseDenial(actionType, denyBotPermission)
	}
	if actionType == ActionRemoveTimeout {
		target := snapshot.Target
		switch {
		case target == nil || !target.Present:
			return caseDenial(actionType, denyTargetNotInGuild)
		case !actorIsOwner && target.TopRolePosition >= snapshot.Actor.TopRolePosition:
			return caseDenial(actionType, denyActorHierarchy)
		case target.TopRolePosition >= snapshot.Bot.TopRolePosition:
			return caseDenial(actionType, denyBotHierarchy)
		}
	}
	return nil
}

// caseDenial builds a case-creation denial. It is not audited here because
// the case transaction may still be retried; CaseService audits a bot
// denial once the attempt is final.
func caseDenial(actionType ActionType, reason string) error {
	metadata, _ := json.Marshal(map[string]string{"selected_action": string(actionType)})
	return &AuthorizationError{Capability: PermissionActionCaseCreate, Reason: reason, MetadataJSON: string(metadata)}
}

// auditBotDenial records a denial caused by Quack's own Discord access
// (see isBotDenial). Other denials are not recorded.
func (s *GuildService) auditBotDenial(ctx context.Context, guildContext *GuildStaffContext, capability PermissionAction, source AuditSource, reason, metadata string) error {
	if !isBotDenial(reason) {
		return nil
	}
	requestID, correlationID := TraceIDsFromContext(ctx)
	return recordAudit(ctx, s.store, &AuditLogEntry{
		GuildID:             guildContext.Guild.ID,
		ActorDiscordUserID:  guildContext.actorID(),
		ActorPermissionBits: guildContext.PermissionBits,
		Source:              source,
		Action:              string(AuditActionAuthorizationDenied),
		ResourceType:        "permission",
		ResourceID:          string(capability),
		Result:              AuditResultDenied,
		FailureReason:       reason,
		CorrelationID:       correlationID,
		RequestID:           requestID,
		MetadataJSON:        metadata,
	})
}

// actionPermission is the Discord permission the bot needs for an action,
// or 0 when it needs none.
func actionPermission(actionType ActionType) uint64 {
	switch actionType {
	case ActionTimeoutUser, ActionRemoveTimeout:
		return permissionModerateMembers
	case ActionKickUser:
		return permissionKickMembers
	case ActionBanUser, ActionUnbanUser:
		return permissionBanMembers
	default:
		return 0
	}
}
