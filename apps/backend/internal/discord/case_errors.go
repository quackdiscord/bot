package discord

import (
	"errors"
	"log/slog"

	"github.com/quackdiscord/bot/internal/quack"
)

// caseErrorMessage maps failures of case reads and of operations on an
// existing case to private replies, without claiming anything was created
// or missing. Anything unexpected is logged.
func caseErrorMessage(err error) string {
	switch {
	case errors.Is(err, quack.ErrCasePermissionDenied), errors.Is(err, quack.ErrAuthorizationDenied):
		return "You don’t have permission to do that. Ask a moderator with the required permission."
	case errors.Is(err, quack.ErrCaseTemplateNotAvailable):
		return "That rule is no longer available. Choose another from the suggestions."
	case errors.Is(err, quack.ErrCaseValidation):
		return "Something is missing or doesn’t look right. Check the case number and command options."
	case errors.Is(err, quack.ErrBotNotInGuild):
		return "I’m not set up in this server yet."
	default:
		slog.Error("case command failed", "error", err)
		return "I couldn’t finish that. Try again in a moment."
	}
}

// caseCreateErrorMessage is caseErrorMessage for case creation, where the
// reply also says that no case was created and how to recover.
func caseCreateErrorMessage(err error) string {
	if denial, ok := errors.AsType[*quack.AuthorizationError](err); ok {
		return caseAuthorizationErrorMessage(denial)
	}
	switch {
	case errors.Is(err, quack.ErrCasePermissionDenied):
		return "No case was created. You need Moderate Members permission to create cases. Ask a staff member with that permission to handle this case."
	case errors.Is(err, quack.ErrAuthorizationDenied):
		return "No case was created. Your permissions or role position don’t allow this action."
	case errors.Is(err, quack.ErrAuthorizationUnavailable):
		return "I couldn’t check Discord permissions, so no case was created. Try again in a moment."
	default:
		return caseErrorMessage(err)
	}
}

// caseAuthorizationErrorMessage turns a known denial reason into recovery
// guidance. Unknown reasons, and the audit metadata, are never shown.
func caseAuthorizationErrorMessage(denial *quack.AuthorizationError) string {
	const prefix = "No case was created. "
	switch denial.Reason {
	case "self_target":
		return prefix + "You cannot create a case against yourself. Select another member, or ask another authorized staff member to review your case."
	case "actor_hierarchy":
		return prefix + "The target's highest role is equal to or above yours. Ask a staff member with a higher role and the required permissions to handle this case."
	case "bot_hierarchy":
		return prefix + "The target's highest role is equal to or above Quack's. Ask a server administrator to review Quack's role position before trying again."
	case "bot_target":
		return prefix + "Cases cannot target bot accounts. Select a member who is not a bot."
	case "guild_owner_target":
		return prefix + "Cases cannot target the server owner. Select another member."
	case "target_not_in_guild":
		return prefix + "The target is no longer in this server. Select a current member."
	case "actor_not_in_guild":
		return prefix + "You are no longer a member of this server. Ask a current authorized staff member to handle this case."
	case "bot_not_in_guild":
		return prefix + "I’m not set up in this server yet. Ask a server administrator to restore Quack before trying again."
	}
	return prefix + "Quack could not confirm authority for this case. Ask a server administrator to review your permissions and the target, then try again."
}
