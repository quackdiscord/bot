package quack

import (
	"errors"
	"fmt"
	"strings"
)

// Errors the adapters map to responses. Validation errors wrap their
// sentinel with a message that is safe to show the caller.
var (
	// ErrAuthorizationDenied means live Discord state does not allow the
	// operation. It is usually wrapped in an *AuthorizationError.
	ErrAuthorizationDenied = errors.New("authorization denied")
	// ErrAuthorizationUnavailable means live Discord state could not be
	// fetched, so Quack refuses rather than trusting stale data.
	ErrAuthorizationUnavailable = errors.New("live discord authorization unavailable")
	// ErrBotNotInGuild means Quack is not installed in the requested guild.
	ErrBotNotInGuild = errors.New("bot is not in guild")

	ErrCaseValidation           = errors.New("case validation failed")
	ErrCaseTemplateNotAvailable = errors.New("case template not available")
	ErrCasePermissionDenied     = errors.New("case permission denied")
	ErrCaseNotFound             = errors.New("case not found")

	ErrTemplateValidation       = errors.New("template validation failed")
	ErrTemplateNotFound         = errors.New("case template not found")
	ErrTemplatePermissionDenied = errors.New("template permission denied")

	ErrGuildSettingsValidation       = errors.New("guild settings validation failed")
	ErrGuildSettingsPermissionDenied = errors.New("guild settings permission denied")
	// ErrGuildSettingsNotFound means the guild has not been bootstrapped.
	ErrGuildSettingsNotFound = errors.New("guild settings not found")

	// ErrEvidenceValidation means a message link is malformed or points
	// outside the case's guild or target.
	ErrEvidenceValidation = errors.New("evidence validation failed")

	// ErrAppealNotFound is also returned when the appeal belongs to someone
	// else, so members cannot probe for other members' appeals.
	ErrAppealNotFound         = errors.New("appeal not found")
	ErrAppealValidation       = errors.New("appeal validation failed")
	ErrAppealPermissionDenied = errors.New("appeal permission denied")
	// ErrAppealConflict means the case already has an appeal or the appeal
	// is not in a state that allows the requested transition.
	ErrAppealConflict = errors.New("appeal state conflict")

	// ErrAppealAlreadyExists is returned by stores when a case already has an
	// appeal.
	ErrAppealAlreadyExists = errors.New("appeal already exists for case")
	// ErrAppealStateConflict is returned by stores when an appeal changed
	// since it was read.
	ErrAppealStateConflict = errors.New("appeal state conflict")
	// ErrAppealCaseIneligible is returned by stores when the case was voided
	// or no longer belongs to the appellant.
	ErrAppealCaseIneligible = errors.New("case is not eligible for appeal")

	ErrAuditValidation       = errors.New("audit validation failed")
	ErrAuditPermissionDenied = errors.New("audit permission denied")

	ErrStatisticsValidation       = errors.New("statistics validation failed")
	ErrStatisticsPermissionDenied = errors.New("statistics permission denied")

	// ErrAuditMirrorChannelUnavailable means the configured audit channel was
	// deleted or the bot lost access to it.
	ErrAuditMirrorChannelUnavailable = errors.New("audit mirror channel unavailable")
)

// AuthorizationError explains an authorization denial with a short reason
// code that is safe to log, audit, and show.
type AuthorizationError struct {
	Capability PermissionAction
	Reason     string
	// MetadataJSON is extra audit metadata, such as the action that was
	// about to run.
	MetadataJSON string
}

func (e *AuthorizationError) Error() string {
	if e == nil || strings.TrimSpace(e.Reason) == "" {
		return ErrAuthorizationDenied.Error()
	}
	return fmt.Sprintf("%s: %s", ErrAuthorizationDenied, e.Reason)
}

func (e *AuthorizationError) Unwrap() error { return ErrAuthorizationDenied }

// EvidenceUnavailableError means a valid message link could not be captured,
// for example because the message was deleted. Outcome is recorded on the
// evidence snapshot.
type EvidenceUnavailableError struct{ Outcome, Message string }

func (e *EvidenceUnavailableError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Outcome
}

func caseValidationError(message string) error {
	return fmt.Errorf("%w: %s", ErrCaseValidation, message)
}

func templateValidationError(message string) error {
	return fmt.Errorf("%w: %s", ErrTemplateValidation, message)
}

func settingsValidationError(message string) error {
	return fmt.Errorf("%w: %s", ErrGuildSettingsValidation, message)
}

func appealValidationError(message string) error {
	return fmt.Errorf("%w: %s", ErrAppealValidation, message)
}

func auditValidationError(message string) error {
	return fmt.Errorf("%w: %s", ErrAuditValidation, message)
}

func statisticsValidationError(message string) error {
	return fmt.Errorf("%w: %s", ErrStatisticsValidation, message)
}
