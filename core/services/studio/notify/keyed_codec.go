package notify

import "encoding/json"

// encodeParams and decodeParams keep Params as JSON text in a store. The
// encoding of a map is sorted by key, so equal maps give equal text.
func encodeParams(p map[string]any) (string, error) {
	if len(p) == 0 {
		return "", nil
	}
	b, err := json.Marshal(p)
	return string(b), err
}

func decodeParams(s string) map[string]any {
	if s == "" {
		return nil
	}
	var p map[string]any
	if json.Unmarshal([]byte(s), &p) != nil {
		return nil
	}
	return p
}

func encodeIds(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	b, _ := json.Marshal(ids)
	return string(b)
}

func decodeIds(s string) []string {
	if s == "" {
		return nil
	}
	var ids []string
	if json.Unmarshal([]byte(s), &ids) != nil {
		return nil
	}
	return ids
}
