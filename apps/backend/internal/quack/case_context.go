package quack

import (
	"encoding/json"
	"fmt"
	"strings"
)

// CaseContextValueInput is a value for one of the template's context fields.
type CaseContextValueInput struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

// CaseContextValueResponse is a context field and its value as stored on a
// case.
type CaseContextValueResponse struct {
	Key       string           `json:"key"`
	Label     string           `json:"label"`
	FieldType ContextFieldType `json:"type"`
	Required  bool             `json:"required"`
	Value     any              `json:"value"`
}

// Context value length limits, in runes.
const (
	maxShortContextRunes = 500
	maxLongContextRunes  = 4000
)

// validateContextValues checks values against the template's context fields
// and returns the stored JSON, any message links to capture as evidence, and
// whether any non-link value was given. Message links count as evidence, not
// as context the member can see.
func validateContextValues(fields []CaseTemplateContextField, inputs []CaseContextValueInput) (valuesJSON string, links []string, hasOtherContext bool, err error) {
	byKey := make(map[string]json.RawMessage, len(inputs))
	for _, input := range inputs {
		key := strings.ToLower(strings.TrimSpace(input.Key))
		if key == "" {
			return "", nil, false, caseValidationError("context value key is required")
		}
		if _, duplicate := byKey[key]; duplicate {
			return "", nil, false, caseValidationError("duplicate context value")
		}
		byKey[key] = input.Value
	}
	values := make([]CaseContextValueResponse, 0, len(fields))
	links = []string{}
	for _, field := range fields {
		raw, provided := byKey[field.Key]
		delete(byKey, field.Key)
		value := CaseContextValueResponse{
			Key:       field.Key,
			Label:     field.Label,
			FieldType: field.FieldType,
			Required:  field.Required,
		}
		if !provided || len(raw) == 0 || string(raw) == "null" {
			if field.Required {
				return "", nil, false, caseValidationError("required context value is missing: " + field.Key)
			}
			values = append(values, value)
			continue
		}
		switch field.FieldType {
		case ContextFieldShortText, ContextFieldLongText, ContextFieldMessageLink:
			var text string
			if json.Unmarshal(raw, &text) != nil {
				return "", nil, false, caseValidationError("context value has wrong type: " + field.Key)
			}
			text = strings.TrimSpace(text)
			limit := maxLongContextRunes
			if field.FieldType == ContextFieldShortText {
				limit = maxShortContextRunes
			}
			if text == "" && field.Required {
				return "", nil, false, caseValidationError("required context value is empty: " + field.Key)
			}
			if len([]rune(text)) > limit {
				return "", nil, false, caseValidationError("context value is too long: " + field.Key)
			}
			value.Value = text
			if field.FieldType == ContextFieldMessageLink && text != "" {
				links = append(links, text)
			} else if text != "" {
				hasOtherContext = true
			}
		case ContextFieldBoolean:
			var boolean bool
			if json.Unmarshal(raw, &boolean) != nil {
				return "", nil, false, caseValidationError("context value has wrong type: " + field.Key)
			}
			value.Value = boolean
			hasOtherContext = true
		case ContextFieldNumber:
			var decoded any
			decoder := json.NewDecoder(strings.NewReader(string(raw)))
			decoder.UseNumber()
			if !json.Valid(raw) || decoder.Decode(&decoded) != nil {
				return "", nil, false, caseValidationError("context value has wrong type: " + field.Key)
			}
			number, ok := decoded.(json.Number)
			if !ok {
				return "", nil, false, caseValidationError("context value has wrong type: " + field.Key)
			}
			if _, err := number.Float64(); err != nil {
				return "", nil, false, caseValidationError("context number is invalid: " + field.Key)
			}
			value.Value = number
			hasOtherContext = true
		default:
			return "", nil, false, caseValidationError("context field type is invalid")
		}
		values = append(values, value)
	}
	if len(byKey) > 0 {
		return "", nil, false, caseValidationError("unknown context value")
	}
	body, err := json.Marshal(values)
	if err != nil {
		return "", nil, false, fmt.Errorf("marshal context values: %w", err)
	}
	return string(body), links, hasOtherContext, nil
}

// parseContextValues decodes a case's stored context values, reading
// anything malformed as none.
func parseContextValues(body string) []CaseContextValueResponse {
	var values []CaseContextValueResponse
	if json.Unmarshal([]byte(body), &values) != nil {
		return []CaseContextValueResponse{}
	}
	return values
}
