package quack

import (
	"encoding/json"
	"testing"
)

// FuzzNormalizeTemplatePolicyJSON exercises the bounded JSON object boundary
// used by imported policy and action configuration payloads.
func FuzzNormalizeTemplatePolicyJSON(f *testing.F) {
	for _, seed := range []string{`{}`, `{"duration_seconds":60}`, `null`, `[]`, `{bad`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		normalized, err := normalizeJSONObject(json.RawMessage(value))
		if err != nil {
			return
		}
		var object map[string]any
		if json.Unmarshal([]byte(normalized), &object) != nil || object == nil {
			t.Fatalf("successful normalization returned a non-object: %q", normalized)
		}
	})
}

// FuzzStructuredContextValue exercises type, length, and malformed-JSON
// handling without requiring persistence or Discord adapters.
func FuzzStructuredContextValue(f *testing.F) {
	f.Add("summary", `"visible context"`)
	f.Add("summary", `null`)
	f.Add("summary", `{bad`)
	f.Fuzz(func(t *testing.T, key, value string) {
		fields := []CaseTemplateContextField{{Key: "summary", Label: "Summary", FieldType: ContextFieldShortText, Position: 1, Required: true}}
		body, _, _, err := validateContextValues(fields, []CaseContextValueInput{{Key: key, Value: json.RawMessage(value)}})
		if err != nil {
			return
		}
		var decoded []CaseContextValueResponse
		if json.Unmarshal([]byte(body), &decoded) != nil || len(decoded) != 1 {
			t.Fatalf("successful context validation returned an invalid snapshot: %q", body)
		}
	})
}
