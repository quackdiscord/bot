package honeypot

import (
	"context"
	"errors"
)

// maxRecoveryBatch caps the incidents one RecoverPending call takes over.
const maxRecoveryBatch = 8

// RecoverPending finishes up to limit incidents whose worker died before
// recording an outcome, and returns the guilds that gained a case. It never
// punishes twice: a case already saved under the incident's idempotency key
// is adopted before anything else is consulted, even if the member has since
// left or the template was archived. Only when no case exists does it check
// the current settings, template, channel, message, and author live, and
// then open the case with the original key. A transient failure leaves the
// incident pending for a later call. It does nothing unless the applier is
// an IncidentRecoverer.
func (s *Service) RecoverPending(ctx context.Context, limit int) ([]string, error) {
	recoverer, ok := s.applier.(IncidentRecoverer)
	if !ok {
		return nil, nil
	}
	var guilds []string
	var failures []error
	for range min(max(limit, 1), maxRecoveryBatch) {
		trigger, err := s.store.claimPendingIncident(ctx)
		if err != nil {
			return guilds, errors.Join(append(failures, err)...)
		}
		if trigger == nil {
			break
		}
		attemptCtx, cancel := context.WithTimeout(ctx, attemptTimeout)
		recovered, err := s.recoverIncident(attemptCtx, recoverer, trigger)
		cancel()
		if err != nil {
			failures = append(failures, err)
		}
		if recovered {
			guilds = append(guilds, trigger.GuildID)
		}
	}
	return guilds, errors.Join(failures...)
}

// recoverIncident adopts or opens the case for one leased incident and
// reports whether it now has one.
func (s *Service) recoverIncident(ctx context.Context, recoverer IncidentRecoverer, trigger *Trigger) (bool, error) {
	request := ApplyRequest{
		GuildID:                 trigger.GuildID,
		TemplateID:              trigger.TemplateID,
		TargetDiscordUserID:     trigger.TargetDiscordUserID,
		ContextChannelDiscordID: trigger.ChannelDiscordID,
		ContextMessageDiscordID: trigger.MessageDiscordID,
		IdempotencyKey:          idempotencyKey(trigger.GuildID, trigger.MessageDiscordID),
		Source:                  SourceHoneypot,
		ActorType:               ActorTypeSystem,
	}
	result, err := recoverer.FindHoneypotCase(ctx, request)
	if err != nil {
		return false, err
	}
	if result.CaseID == "" {
		result, err = s.reopen(ctx, recoverer, trigger, request)
		if err != nil || result.CaseID == "" {
			return false, err
		}
	}
	if err := s.store.completeIncident(ctx, trigger, OutcomeCreated, result.CaseID, ""); err != nil {
		return false, err
	}
	s.auditTrigger(ctx, trigger.GuildID, "honeypot.case.created", "case", result.CaseID, nil)
	return true, nil
}

// reopen opens the case an interrupted incident never got, after checking
// that the trap still applies. When it no longer does, the incident is
// closed without a case and an empty result is returned.
func (s *Service) reopen(ctx context.Context, recoverer IncidentRecoverer, trigger *Trigger, request ApplyRequest) (ApplyResult, error) {
	settings, enabled, err := s.loadSettings(ctx, trigger.GuildID)
	if err != nil {
		return ApplyResult{}, err
	}
	if !enabled || settings.ChannelDiscordID != trigger.ChannelDiscordID || settings.TemplateID != trigger.TemplateID {
		return ApplyResult{}, s.store.completeIncident(ctx, trigger, OutcomeFailed, "", "recovery_policy_changed")
	}
	if err := s.templates.ValidateHoneypotTemplate(ctx, trigger.GuildID, trigger.TemplateID); err != nil {
		if errors.Is(err, ErrTemplateUnavailable) {
			return ApplyResult{}, errors.Join(err, s.store.completeIncident(ctx, trigger, OutcomeFailed, "", "template_unavailable"))
		}
		return ApplyResult{}, err
	}
	if err := s.channels.ValidateHoneypotChannel(ctx, trigger.GuildID, trigger.ChannelDiscordID); err != nil {
		return ApplyResult{}, err
	}
	request, err = recoverer.PrepareHoneypotRecovery(ctx, request)
	switch {
	case errors.Is(err, ErrExempt):
		return ApplyResult{}, s.store.completeIncident(ctx, trigger, OutcomeExempt, "", "recovery_author_exempt")
	case errors.Is(err, ErrNotTrigger):
		return ApplyResult{}, s.store.completeIncident(ctx, trigger, OutcomeFailed, "", "recovery_source_unavailable")
	case err != nil:
		return ApplyResult{}, err
	}
	result, err := s.applier.ApplyHoneypotCase(ctx, request)
	if err != nil {
		return ApplyResult{}, err
	}
	if result.CaseID == "" {
		return ApplyResult{}, errors.New("recovered honeypot application returned no saved case")
	}
	return result, nil
}
