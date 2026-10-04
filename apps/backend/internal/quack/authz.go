package quack

import (
	"context"
	"encoding/json"
	"strings"
)

// Denial reasons recorded on AuthorizationError and in the audit log.
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

// Authorize checks that guildContext's actor is still in the guild and holds
// capability. An empty capability only checks membership. Denials are
// audited.
func (s *GuildService) Authorize(ctx context.Context, guildContext *GuildStaffContext, capability PermissionAction, source AuditSource) error {
	ctx = ensureTraceContext(ctx)
	if guildContext == nil || guildContext.Guild == nil {
		return &AuthorizationError{Capability: capability, Reason: denyActorNotInGuild}
	}
	var reason string
	switch {
	case !guildContext.Live.Actor.Present:
		reason = denyActorNotInGuild
	case capability != "" && !guildContext.Can(capability):
		reason = denyPermissionRequired
	default:
		return nil
	}
	_ = s.auditDenial(ctx, guildContext, capability, source, reason, "{}")
	return &AuthorizationError{Capability: capability, Reason: reason}
}

// PreflightCase re-checks Discord right before a case is stored: the actor
// can moderate, and actor and bot can both act on the target under Discord's
// role hierarchy and permissions for actionType. It refreshes guildContext
// with what it fetched.
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
	guildContext.applyLive(snapshot, actorID)

	actor, bot, target := snapshot.Actor, snapshot.Bot, *snapshot.Target
	actorIsOwner := actor.DiscordUserID == snapshot.Guild.OwnerID
	switch {
	case !actor.Present:
		return caseDenial(actionType, denyActorNotInGuild)
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

	required := actionPermission(actionType)
	if required != 0 && !actorIsOwner && !hasDiscordPermission(actor.PermissionBits, required) {
		return caseDenial(actionType, denyPermissionRequired)
	}
	if required != 0 && !hasDiscordPermission(bot.PermissionBits, required) {
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

// PreflightReversal re-checks Discord before a timeout removal or unban.
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
	guildContext.applyLive(snapshot, actorID)

	actorIsOwner := actorID == snapshot.Guild.OwnerID
	required := actionPermission(actionType)
	switch {
	case !snapshot.Actor.Present || !guildContext.Can(PermissionActionCaseCreate):
		return caseDenial(actionType, denyPermissionRequired)
	case !snapshot.Bot.Present:
		return caseDenial(actionType, denyBotNotInGuild)
	case !actorIsOwner && !hasDiscordPermission(snapshot.Actor.PermissionBits, required):
		return caseDenial(actionType, denyPermissionRequired)
	case !hasDiscordPermission(snapshot.Bot.PermissionBits, required):
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
// the case transaction may still be retried; CaseService audits it once the
// attempt is final.
func caseDenial(actionType ActionType, reason string) error {
	metadata, _ := json.Marshal(map[string]string{"selected_action": string(actionType)})
	return &AuthorizationError{Capability: PermissionActionCaseCreate, Reason: reason, MetadataJSON: string(metadata)}
}

// auditDenial records an authorization denial with its reason.
func (s *GuildService) auditDenial(ctx context.Context, guildContext *GuildStaffContext, capability PermissionAction, source AuditSource, reason, metadata string) error {
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

// actionPermission is the Discord permission an action needs, or 0 when it
// needs none.
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
