package quack

import (
	"encoding/json"
	"strings"
)

// CaseContextValueInput is a value for one of the template's context fields.
type CaseContextValueInput struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value" swaggertype:"object"`
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

// validateContextValues checks values against the template's context fields
// and returns the stored JSON, any message links to capture as evidence, and
// whether any non-link value was given.
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
		value := CaseContextValueResponse{Key: field.Key, Label: field.Label, FieldType: field.FieldType, Required: field.Required}
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
			limit := 4000
			if field.FieldType == ContextFieldShortText {
				limit = 500
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
			var number json.Number
			decoder := json.NewDecoder(strings.NewReader(string(raw)))
			decoder.UseNumber()
			if decoder.Decode(&number) != nil {
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
		return "", nil, false, err
	}
	return string(body), links, hasOtherContext, nil
}

func parseContextValues(body string) []CaseContextValueResponse {
	var values []CaseContextValueResponse
	if json.Unmarshal([]byte(body), &values) != nil {
		return []CaseContextValueResponse{}
	}
	return values
}
