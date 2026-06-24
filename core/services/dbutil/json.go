package dbutil

import "encoding/json"

// MarshalJSON marshals a value to a JSON string for storage.
// Returns "" on nil, error, or empty-equivalent values ("null", "[]", "{}").
func MarshalJSON(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	s := string(b)
	if s == "null" || s == "[]" || s == "{}" {
		return ""
	}
	return s
}

// UnmarshalJSON unmarshals a JSON string into the target.
// Returns nil on empty input.
func UnmarshalJSON(s string, v any) error {
	if s == "" {
		return nil
	}
	return json.Unmarshal([]byte(s), v)
}
