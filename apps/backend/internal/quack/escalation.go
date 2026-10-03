package quack

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// CaseSelectedLevel is the level a case was created at and the case count
// that selected it.
type CaseSelectedLevel struct {
	TemplateLevelDetails
	MatchedCaseCount int64 `json:"matched_case_count"`
}

// CaseTemplateSnapshotResponse is the template as it was when the case was
// created. It is stored on the case as TemplateSnapshotJSON and returned to
// staff as is.
type CaseTemplateSnapshotResponse struct {
	Template      templateSnapshotTemplate       `json:"template"`
	SelectedLevel CaseSelectedLevel              `json:"selected_level"`
	Actions       []TemplateActionResponse       `json:"actions"`
	ContextFields []TemplateContextFieldResponse `json:"context_fields"`
	ContextValues []CaseContextValueResponse     `json:"context_values"`
}

// templateSnapshotTemplate is the part of the template a case keeps.
type templateSnapshotTemplate struct {
	ID             string `json:"id"`
	Slug           string `json:"slug"`
	Name           string `json:"name"`
	Version        uint   `json:"version"`
	ReasonTemplate string `json:"reason_template"`
	CaseDecayDays  int    `json:"case_decay_days"`
	Appealable     bool   `json:"appealable"`
}

// selectLevel picks the level with the highest threshold the member has
// reached, or the default level if none, and returns it with the count that
// chose it. The count includes the case being created, so a threshold of 3
// fires on the member's third case. Only valid, non-imported cases under the
// same template count, and only those inside the template's decay window
// when it has one.
func (s *CaseService) selectLevel(ctx context.Context, guildID, targetDiscordUserID string, template *ExpandedCaseTemplate) (ExpandedCaseTemplateLevel, int64, error) {
	prior, err := s.store.CountTemplateCasesForTarget(ctx, CountTemplateCasesForTargetParams{
		GuildID:             guildID,
		TemplateID:          template.Template.ID,
		TargetDiscordUserID: targetDiscordUserID,
		CreatedAtOrAfter:    decayCutoff(template.Template.CaseDecayDays, time.Now()),
	})
	if err != nil {
		return ExpandedCaseTemplateLevel{}, 0, fmt.Errorf("count prior cases: %w", err)
	}
	count := prior + 1

	// Templates are validated when saved, but a level that slipped through
	// must never enforce something unintended.
	var fallback, best *ExpandedCaseTemplateLevel
	for i := range template.Levels {
		candidate := &template.Levels[i]
		if len(candidate.Actions) > 1 {
			return ExpandedCaseTemplateLevel{}, 0, caseValidationError("template level has more than one enforcement action")
		}
		level := candidate.Level
		if level.IsDefault {
			fallback = candidate
			continue
		}
		if level.TriggerCaseCount <= 0 {
			return ExpandedCaseTemplateLevel{}, 0, caseValidationError("escalation level trigger_case_count must be positive")
		}
		if count >= int64(level.TriggerCaseCount) &&
			(best == nil || level.TriggerCaseCount > best.Level.TriggerCaseCount) {
			best = candidate
		}
	}
	if fallback == nil {
		return ExpandedCaseTemplateLevel{}, 0, caseValidationError("template has no default level")
	}
	if best != nil {
		return *best, count, nil
	}
	return *fallback, count, nil
}

// decayCutoff is the start of a decay window of days ending at now, or nil
// when days is zero and every case counts.
func decayCutoff(days int, now time.Time) *time.Time {
	if days <= 0 {
		return nil
	}
	cutoff := now.UTC().AddDate(0, 0, -days)
	return &cutoff
}

// buildTemplateSnapshot freezes everything that decided a case's outcome
// into Case.TemplateSnapshotJSON, so later template edits never rewrite
// history.
func buildTemplateSnapshot(template *ExpandedCaseTemplate, level ExpandedCaseTemplateLevel, caseCount int64, valuesJSON string) (string, error) {
	snapshot := CaseTemplateSnapshotResponse{
		Template: templateSnapshotTemplate{
			ID:             template.Template.ID,
			Slug:           template.Template.Slug,
			Name:           template.Template.Name,
			Version:        template.Template.Version,
			ReasonTemplate: template.Template.ReasonTemplate,
			CaseDecayDays:  template.Template.CaseDecayDays,
			Appealable:     template.Template.Appealable,
		},
		SelectedLevel: CaseSelectedLevel{
			TemplateLevelDetails: templateLevelDetails(level.Level),
			MatchedCaseCount:     caseCount,
		},
		Actions:       templateActionResponses(level.Actions),
		ContextFields: contextFieldResponses(template.ContextFields),
		ContextValues: parseContextValues(valuesJSON),
	}
	body, err := json.Marshal(snapshot)
	if err != nil {
		return "", fmt.Errorf("marshal case template snapshot: %w", err)
	}
	return string(body), nil
}

// parseTemplateSnapshot decodes a case's snapshot, or returns nil if it has
// none (imported v4 cases).
func parseTemplateSnapshot(snapshotJSON string) *CaseTemplateSnapshotResponse {
	var snapshot CaseTemplateSnapshotResponse
	if err := json.Unmarshal([]byte(snapshotJSON), &snapshot); err != nil || snapshot.Template.ID == "" {
		return nil
	}
	return &snapshot
}

// snapshotSelectedLevel reads the selected level from a case snapshot.
func snapshotSelectedLevel(snapshotJSON string) *CaseSelectedLevel {
	var snapshot struct {
		SelectedLevel CaseSelectedLevel `json:"selected_level"`
	}
	if err := json.Unmarshal([]byte(snapshotJSON), &snapshot); err != nil || snapshot.SelectedLevel.ID == "" {
		return nil
	}
	return &snapshot.SelectedLevel
}

// snapshotRuleName reads the template name from a case snapshot, or "" for
// cases without one.
func snapshotRuleName(snapshotJSON string) string {
	if snapshot := parseTemplateSnapshot(snapshotJSON); snapshot != nil {
		return snapshot.Template.Name
	}
	return ""
}

// snapshotAppealable reports whether the template allowed appeals when the
// case was created. Later template edits don't change it.
func snapshotAppealable(snapshotJSON string) bool {
	var snapshot struct {
		Template struct {
			Appealable bool `json:"appealable"`
		} `json:"template"`
	}
	return json.Unmarshal([]byte(snapshotJSON), &snapshot) == nil && snapshot.Template.Appealable
}
