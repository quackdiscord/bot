package quack

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Limits on template actions. Admins control retries; Discord sets the
// other two.
const (
	// MaxTemplateSafeRetries is the most automatic retries a template action
	// may allow.
	MaxTemplateSafeRetries = 10
	// MaxTimeoutDurationSeconds is Discord's 28-day timeout limit.
	MaxTimeoutDurationSeconds = 28 * 24 * 60 * 60
	// MaxBanDeleteMessageSeconds is Discord's seven-day limit on deleting a
	// banned member's messages.
	MaxBanDeleteMessageSeconds = 7 * 24 * 60 * 60
	// MaxCaseDecayDays caps a template's decay window at 100 years, which
	// keeps the cutoff arithmetic safe.
	MaxCaseDecayDays = 36500
)

// maxContextFields bounds how much a moderator is asked to fill in per case.
const maxContextFields = 10

// templateSlugPattern is the shape of template slugs and context field keys.
var templateSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,63}$`)

// actionConfig is a level action's settings as stored in ConfigJSON and
// copied into each execution's ConfigSnapshotJSON. Only the setting that
// belongs to the action type is set.
type actionConfig struct {
	DurationSeconds      int `json:"duration_seconds,omitempty"`
	DeleteMessageSeconds int `json:"delete_message_seconds,omitempty"`
	// RequestedBy is set only on reversals queued automatically when their
	// case was voided: the staff member who voided it. Their permission to
	// reverse is checked when the reversal runs.
	RequestedBy string `json:"requested_by,omitempty"`
}

// decodeActionConfig reads a stored action configuration. Malformed JSON
// reads as no settings, which enforcement then rejects.
func decodeActionConfig(body string) actionConfig {
	var config actionConfig
	if err := json.Unmarshal([]byte(body), &config); err != nil {
		return actionConfig{}
	}
	return config
}

// validate checks input and normalizes it into storable records. templateID
// is the template being updated, so its own slug doesn't count as taken.
func (s *TemplateService) validate(ctx context.Context, guildContext *GuildStaffContext, templateID string, input TemplateInput) (*ExpandedCaseTemplate, error) {
	slug := strings.ToLower(strings.TrimSpace(input.Slug))
	if !templateSlugPattern.MatchString(slug) {
		return nil, templateValidationError("slug must be 2-64 lowercase letters, numbers, underscores, or hyphens")
	}
	existing, err := s.store.GetCaseTemplateBySlug(ctx, guildContext.Guild.ID, slug)
	if err != nil {
		return nil, err
	}
	if existing != nil && existing.ID != templateID {
		return nil, templateValidationError("slug already exists for this guild")
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return nil, templateValidationError("name is required")
	}
	reason := strings.TrimSpace(input.ReasonTemplate)
	if reason == "" {
		return nil, templateValidationError("reason_template is required")
	}
	if input.CaseDecayDays < 0 || input.CaseDecayDays > MaxCaseDecayDays {
		return nil, templateValidationError(fmt.Sprintf("case_decay_days must be between 0 and %d; 0 counts all-time history", MaxCaseDecayDays))
	}
	levels, err := normalizeLevels(input.Levels)
	if err != nil {
		return nil, err
	}
	fields, err := normalizeContextFields(input.ContextFields)
	if err != nil {
		return nil, err
	}
	return &ExpandedCaseTemplate{
		Template: CaseTemplate{
			GuildID:                guildContext.Guild.ID,
			Slug:                   slug,
			Name:                   name,
			Description:            strings.TrimSpace(input.Description),
			ReasonTemplate:         reason,
			CaseDecayDays:          input.CaseDecayDays,
			Appealable:             input.Appealable,
			CreatedByDiscordUserID: guildContext.Staff.DiscordUserID,
			UpdatedByDiscordUserID: guildContext.Staff.DiscordUserID,
		},
		ContextFields: fields,
		Levels:        levels,
	}, nil
}

// normalizeContextFields validates up to ten fields with unique keys and
// positions. A zero position means "in input order".
func normalizeContextFields(inputs []TemplateContextFieldInput) ([]CaseTemplateContextField, error) {
	if len(inputs) > maxContextFields {
		return nil, templateValidationError(fmt.Sprintf("at most %d context fields are allowed", maxContextFields))
	}
	keys := map[string]bool{}
	positions := map[int]bool{}
	fields := make([]CaseTemplateContextField, 0, len(inputs))
	for i, input := range inputs {
		key := strings.ToLower(strings.TrimSpace(input.Key))
		if !templateSlugPattern.MatchString(key) {
			return nil, templateValidationError("context field key must be 2-64 lowercase letters, numbers, underscores, or hyphens")
		}
		if keys[key] {
			return nil, templateValidationError("context field keys must be unique")
		}
		keys[key] = true
		label := strings.TrimSpace(input.Label)
		if label == "" || len([]rune(label)) > 100 {
			return nil, templateValidationError("context field label must be 1-100 characters")
		}
		if !validContextFieldType(input.FieldType) {
			return nil, templateValidationError("context field type is invalid")
		}
		position := input.Position
		if position == 0 {
			position = i + 1
		}
		if position < 1 {
			return nil, templateValidationError("context field position must be positive")
		}
		if positions[position] {
			return nil, templateValidationError("context field positions must be unique")
		}
		positions[position] = true
		fields = append(fields, CaseTemplateContextField{
			Key:       key,
			Label:     label,
			FieldType: input.FieldType,
			Position:  position,
			Required:  input.Required,
		})
	}
	return fields, nil
}

// normalizeLevels requires exactly one default level, distinct positive
// thresholds on the others, and at most one action per level.
func normalizeLevels(inputs []TemplateLevelInput) ([]ExpandedCaseTemplateLevel, error) {
	if len(inputs) == 0 {
		return nil, templateValidationError("at least one level is required")
	}
	levels := make([]ExpandedCaseTemplateLevel, 0, len(inputs))
	defaults := 0
	thresholds := make(map[int]bool, len(inputs))
	for i, input := range inputs {
		name := strings.TrimSpace(input.Name)
		if name == "" {
			return nil, templateValidationError("level name is required")
		}
		if input.IsDefault {
			defaults++
			if input.TriggerCaseCount != 0 {
				return nil, templateValidationError("default level cannot have escalation triggers")
			}
		} else {
			if input.TriggerCaseCount <= 0 {
				return nil, templateValidationError("escalation level trigger_case_count must be positive")
			}
			if thresholds[input.TriggerCaseCount] {
				return nil, templateValidationError("escalation level trigger_case_count values must be distinct")
			}
			thresholds[input.TriggerCaseCount] = true
		}
		actions, err := normalizeActions(input.Actions)
		if err != nil {
			return nil, err
		}
		position := input.Position
		if position == 0 {
			position = i + 1
		}
		if position < 0 {
			return nil, templateValidationError("level position must be non-negative")
		}
		levels = append(levels, ExpandedCaseTemplateLevel{
			Level: CaseTemplateLevel{
				Position:         position,
				Name:             name,
				IsDefault:        input.IsDefault,
				TriggerCaseCount: input.TriggerCaseCount,
				NotifyUser:       input.NotifyUser,
			},
			Actions: actions,
		})
	}
	if defaults == 0 {
		return nil, templateValidationError("exactly one default level is required")
	}
	if defaults > 1 {
		return nil, templateValidationError("only one default level is allowed")
	}
	return levels, nil
}

// normalizeActions validates a level's action. Warnings and DMs are not
// actions: every case is a warning, and the level's notify_user sends the DM.
func normalizeActions(inputs []TemplateActionInput) ([]CaseTemplateLevelAction, error) {
	if len(inputs) > 1 {
		return nil, templateValidationError("a template level can contain at most one enforcement action")
	}
	actions := make([]CaseTemplateLevelAction, 0, len(inputs))
	for _, input := range inputs {
		switch input.ActionType {
		case "record_warning":
			return nil, templateValidationError("record_warning is not a template action; creating a case records the warning")
		case ActionSendDM:
			return nil, templateValidationError("send_dm is not a template action; set notify_user on the level")
		case ActionTimeoutUser, ActionKickUser, ActionBanUser:
		default:
			return nil, templateValidationError("action_type is invalid")
		}
		if input.MaxRetries < 0 || input.MaxRetries > MaxTemplateSafeRetries {
			return nil, templateValidationError(fmt.Sprintf("max_retries must be between 0 and %d", MaxTemplateSafeRetries))
		}
		config, err := normalizeActionConfig(input)
		if err != nil {
			return nil, err
		}
		actions = append(actions, CaseTemplateLevelAction{
			ActionType: input.ActionType,
			ConfigJSON: config,
			MaxRetries: uint8(input.MaxRetries),
		})
	}
	return actions, nil
}

// normalizeActionConfig checks the action's setting and encodes it.
func normalizeActionConfig(input TemplateActionInput) (string, error) {
	var config actionConfig
	switch input.ActionType {
	case ActionTimeoutUser:
		if input.TimeoutDurationSeconds <= 0 || input.TimeoutDurationSeconds > MaxTimeoutDurationSeconds {
			return "", templateValidationError(fmt.Sprintf("timeout_duration_seconds must be between 1 and %d", MaxTimeoutDurationSeconds))
		}
		if input.DeleteMessageSeconds != 0 {
			return "", templateValidationError("delete_message_seconds is only valid for ban actions")
		}
		config.DurationSeconds = input.TimeoutDurationSeconds
	case ActionKickUser:
		if input.TimeoutDurationSeconds != 0 || input.DeleteMessageSeconds != 0 {
			return "", templateValidationError("kick actions do not accept action settings")
		}
	case ActionBanUser:
		if input.TimeoutDurationSeconds != 0 {
			return "", templateValidationError("timeout_duration_seconds is only valid for timeout actions")
		}
		if input.DeleteMessageSeconds < 0 || input.DeleteMessageSeconds > MaxBanDeleteMessageSeconds {
			return "", templateValidationError(fmt.Sprintf("delete_message_seconds must be between 0 and %d", MaxBanDeleteMessageSeconds))
		}
		config.DeleteMessageSeconds = input.DeleteMessageSeconds
	}
	body, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("marshal template action config: %w", err)
	}
	return string(body), nil
}
