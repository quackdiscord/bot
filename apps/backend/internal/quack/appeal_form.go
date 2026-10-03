package quack

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// AppealQuestionType is the input control for an appeal question.
type AppealQuestionType string

// Appeal question types.
const (
	AppealQuestionShortText AppealQuestionType = "short_text"
	AppealQuestionLongText  AppealQuestionType = "long_text"
	AppealQuestionBoolean   AppealQuestionType = "boolean"
)

// AppealQuestion is one question on a guild's appeal form.
type AppealQuestion struct {
	ID       string             `json:"id"`
	Prompt   string             `json:"prompt"`
	Type     AppealQuestionType `json:"type"`
	Required bool               `json:"required"`
	Position int                `json:"position"`
}

// AppealAnswer is a member's answer to one question.
type AppealAnswer struct {
	QuestionID string `json:"question_id"`
	Value      any    `json:"value"`
}

// AppealSettingsResponse is the form new appeals in a guild will use.
// Default is set when the guild has not customized it.
type AppealSettingsResponse struct {
	GuildID   string           `json:"guild_id"`
	Questions []AppealQuestion `json:"questions"`
	Default   bool             `json:"default"`
}

// DefaultAppealQuestions returns the form used by guilds that have not
// customized theirs.
func DefaultAppealQuestions() []AppealQuestion {
	return []AppealQuestion{
		{ID: "reason", Prompt: "Why should this case be reconsidered?", Type: AppealQuestionLongText, Required: true, Position: 0},
		{ID: "context", Prompt: "Is there any additional context staff should review?", Type: AppealQuestionLongText, Required: false, Position: 1},
	}
}

// GetSettings returns the guild's appeal form.
func (s *AppealService) GetSettings(ctx context.Context, guildID string) (*AppealSettingsResponse, error) {
	guildID = strings.TrimSpace(guildID)
	if guildID == "" {
		return nil, appealValidationError("guild is required")
	}
	settings, err := s.store.GetGuildAppealSettings(ctx, guildID)
	if err != nil {
		return nil, err
	}
	if settings == nil {
		return &AppealSettingsResponse{GuildID: guildID, Questions: DefaultAppealQuestions(), Default: true}, nil
	}
	questions, err := decodeQuestions(settings.QuestionsJSON)
	if err != nil {
		return nil, err
	}
	return &AppealSettingsResponse{GuildID: settings.GuildID, Questions: questions}, nil
}

// UpdateSettings replaces the guild's appeal form. Existing appeals keep
// the form they were submitted with.
func (s *AppealService) UpdateSettings(ctx context.Context, guildContext *GuildStaffContext, questions []AppealQuestion) (*AppealSettingsResponse, error) {
	if guildContext == nil || guildContext.Guild == nil || guildContext.Staff == nil || !guildContext.Can(PermissionActionGuildSettingsWrite) {
		return nil, ErrAppealPermissionDenied
	}
	normalized, err := validateQuestions(questions)
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(normalized)
	settings, err := s.store.UpdateGuildAppealSettings(ctx, UpdateGuildAppealSettingsParams{
		Settings: GuildAppealSettings{
			GuildID:                guildContext.Guild.ID,
			QuestionsJSON:          string(body),
			UpdatedByDiscordUserID: guildContext.Staff.DiscordUserID,
		},
		Audit: appealAudit(ctx, guildContext.Guild.ID, guildContext.Staff.DiscordUserID, guildContext.PermissionBits,
			string(AuditActionAppealSettingsUpdate), "guild_appeal_settings", "", AuditResultSuccess),
	})
	if err != nil {
		return nil, err
	}
	return &AppealSettingsResponse{GuildID: settings.GuildID, Questions: normalized}, nil
}

// validateQuestions checks a form of one to ten questions with unique IDs
// and positions 0..n-1, and returns it sorted by position.
func validateQuestions(questions []AppealQuestion) ([]AppealQuestion, error) {
	if len(questions) == 0 || len(questions) > 10 {
		return nil, appealValidationError("appeal form must contain between 1 and 10 questions")
	}
	normalized := slices.Clone(questions)
	slices.SortStableFunc(normalized, func(a, b AppealQuestion) int { return a.Position - b.Position })
	seen := map[string]bool{}
	for i := range normalized {
		q := &normalized[i]
		q.ID = strings.TrimSpace(q.ID)
		q.Prompt = strings.TrimSpace(q.Prompt)
		if q.ID == "" || len(q.ID) > 64 || q.Prompt == "" || len([]rune(q.Prompt)) > 300 || seen[q.ID] || q.Position != i {
			return nil, appealValidationError("appeal questions require unique ids and contiguous ordering")
		}
		seen[q.ID] = true
		switch q.Type {
		case AppealQuestionShortText, AppealQuestionLongText, AppealQuestionBoolean:
		default:
			return nil, appealValidationError("appeal question type is unsupported")
		}
	}
	return normalized, nil
}

// validateAnswers checks answers against the form and returns them in form
// order with text trimmed.
func validateAnswers(questions []AppealQuestion, answers []AppealAnswer) ([]AppealAnswer, error) {
	byID := map[string]AppealAnswer{}
	for _, answer := range answers {
		answer.QuestionID = strings.TrimSpace(answer.QuestionID)
		if _, duplicate := byID[answer.QuestionID]; answer.QuestionID == "" || duplicate {
			return nil, appealValidationError("answers must have unique question ids")
		}
		byID[answer.QuestionID] = answer
	}
	normalized := make([]AppealAnswer, 0, len(questions))
	for _, question := range questions {
		answer, present := byID[question.ID]
		if !present {
			if question.Required {
				return nil, appealValidationError("required appeal answer is missing")
			}
			continue
		}
		delete(byID, question.ID)
		if question.Type == AppealQuestionBoolean {
			if _, ok := answer.Value.(bool); !ok {
				return nil, appealValidationError("boolean appeal answer is invalid")
			}
		} else {
			value, ok := answer.Value.(string)
			value = strings.TrimSpace(value)
			if !ok || len([]rune(value)) > 4000 || (question.Required && value == "") {
				return nil, appealValidationError("text appeal answer is invalid")
			}
			answer.Value = value
		}
		normalized = append(normalized, answer)
	}
	if len(byID) != 0 {
		return nil, appealValidationError("answer references an unknown question")
	}
	return normalized, nil
}

func decodeQuestions(body string) ([]AppealQuestion, error) {
	var questions []AppealQuestion
	if err := json.Unmarshal([]byte(body), &questions); err != nil {
		return nil, fmt.Errorf("decode appeal questions: %w", err)
	}
	return validateQuestions(questions)
}

// GuildAppealSettings holds a guild's custom appeal form. Appeals snapshot the
// form when submitted, so edits only affect future appeals.
type GuildAppealSettings struct {
	ULIDModel
	GuildID                string
	QuestionsJSON          string
	UpdatedByDiscordUserID string
}
