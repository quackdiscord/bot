package quack

import (
	"context"
	"encoding/json"
	"fmt"
)

// selectedLevel is the escalation level chosen for a new case and the case
// count that chose it.
type selectedLevel struct {
	Level            CaseTemplateLevel
	Actions          []CaseTemplateLevelAction
	MatchedCaseCount int64
}

// selectLevel picks the level with the highest threshold the member has
// reached, or the default level if none. The count includes the case being
// created, so a threshold of 3 fires on the member's third case.
func (s *CaseService) selectLevel(ctx context.Context, guildID, targetDiscordUserID string, template *ExpandedCaseTemplate) (*selectedLevel, error) {
	prior, err := s.store.CountTemplateCasesForTarget(ctx, CountTemplateCasesForTargetParams{
		GuildID:             guildID,
		TemplateID:          template.Template.ID,
		TargetDiscordUserID: targetDiscordUserID,
	})
	if err != nil {
		return nil, err
	}
	count := prior + 1

	var fallback, best *selectedLevel
	for _, expanded := range template.Levels {
		level := expanded.Level
		if len(expanded.Actions) > 1 {
			return nil, caseValidationError("template level has more than one enforcement action")
		}
		candidate := &selectedLevel{Level: level, Actions: expanded.Actions, MatchedCaseCount: count}
		if level.IsDefault {
			fallback = candidate
			continue
		}
		if level.TriggerCaseCount <= 0 {
			return nil, caseValidationError("escalation level trigger_case_count must be positive")
		}
		if count < int64(level.TriggerCaseCount) {
			continue
		}
		if best == nil || level.TriggerCaseCount > best.Level.TriggerCaseCount {
			best = candidate
		}
	}
	if fallback == nil {
		return nil, caseValidationError("template has no default level")
	}
	if best != nil {
		return best, nil
	}
	return fallback, nil
}

// actionType returns the level's action, or "" for a warning-only level.
func (l *selectedLevel) actionType() ActionType {
	if len(l.Actions) == 1 {
		return l.Actions[0].ActionType
	}
	return ""
}

// CaseSelectedLevel is the level a case was created at and the case count
// that selected it.
type CaseSelectedLevel struct {
	TemplateLevelDetails
	MatchedCaseCount int64 `json:"matched_case_count"`
}

// CaseTemplateSnapshotResponse is the template as it was when the case was
// created.
type CaseTemplateSnapshotResponse struct {
	Template      templateSnapshotTemplate       `json:"template"`
	SelectedLevel CaseSelectedLevel              `json:"selected_level"`
	Actions       []templateSnapshotAction       `json:"actions"`
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
	Appealable     bool   `json:"appealable"`
}

// templateSnapshotAction is the level action a case keeps.
type templateSnapshotAction struct {
	ID                     string     `json:"id"`
	ActionType             ActionType `json:"action_type"`
	TimeoutDurationSeconds int        `json:"timeout_duration_seconds,omitempty"`
	DeleteMessageSeconds   int        `json:"delete_message_seconds,omitempty"`
	MaxRetries             uint8      `json:"max_retries"`
}

// buildTemplateSnapshot freezes everything that decided a case's outcome
// into Case.TemplateSnapshotJSON.
func buildTemplateSnapshot(template CaseTemplate, fields []CaseTemplateContextField, valuesJSON string, level selectedLevel) (string, error) {
	snapshot := CaseTemplateSnapshotResponse{
		Template: templateSnapshotTemplate{
			ID:             template.ID,
			Slug:           template.Slug,
			Name:           template.Name,
			Version:        template.Version,
			ReasonTemplate: template.ReasonTemplate,
			Appealable:     template.Appealable,
		},
		SelectedLevel: CaseSelectedLevel{
			TemplateLevelDetails: templateLevelDetails(level.Level),
			MatchedCaseCount:     level.MatchedCaseCount,
		},
		Actions:       make([]templateSnapshotAction, 0, len(level.Actions)),
		ContextFields: contextFieldResponses(fields),
	}
	_ = json.Unmarshal([]byte(valuesJSON), &snapshot.ContextValues)
	for _, action := range level.Actions {
		settings := templateActionResponse(action)
		snapshot.Actions = append(snapshot.Actions, templateSnapshotAction{
			ID:                     action.ID,
			ActionType:             action.ActionType,
			TimeoutDurationSeconds: settings.TimeoutDurationSeconds,
			DeleteMessageSeconds:   settings.DeleteMessageSeconds,
			MaxRetries:             action.MaxRetries,
		})
	}
	body, err := json.Marshal(snapshot)
	if err != nil {
		return "", fmt.Errorf("marshal case template snapshot: %w", err)
	}
	return string(body), nil
}

// parseTemplateSnapshot decodes a case's snapshot, or returns nil if it has
// none. Older snapshots stored action settings under "config".
func parseTemplateSnapshot(snapshotJSON string) *CaseTemplateSnapshotResponse {
	var stored struct {
		Template      templateSnapshotTemplate       `json:"template"`
		SelectedLevel CaseSelectedLevel              `json:"selected_level"`
		Actions       []json.RawMessage              `json:"actions"`
		ContextFields []TemplateContextFieldResponse `json:"context_fields"`
		ContextValues []CaseContextValueResponse     `json:"context_values"`
	}
	if err := json.Unmarshal([]byte(snapshotJSON), &stored); err != nil || stored.Template.ID == "" {
		return nil
	}
	snapshot := CaseTemplateSnapshotResponse{
		Template:      stored.Template,
		SelectedLevel: stored.SelectedLevel,
		Actions:       make([]templateSnapshotAction, 0, len(stored.Actions)),
		ContextFields: stored.ContextFields,
		ContextValues: stored.ContextValues,
	}
	for _, raw := range stored.Actions {
		var action templateSnapshotAction
		if err := json.Unmarshal(raw, &action); err != nil {
			continue
		}
		if action.TimeoutDurationSeconds == 0 && action.DeleteMessageSeconds == 0 {
			var legacy struct {
				Config json.RawMessage `json:"config"`
			}
			if err := json.Unmarshal(raw, &legacy); err == nil && len(legacy.Config) > 0 {
				settings := decodeActionConfig(string(legacy.Config))
				action.TimeoutDurationSeconds = settings.DurationSeconds
				action.DeleteMessageSeconds = settings.DeleteMessageSeconds
			}
		}
		snapshot.Actions = append(snapshot.Actions, action)
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
