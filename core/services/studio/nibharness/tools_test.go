package nibharness

import (
	"encoding/json"
	"testing"
)

func TestNormalizeObjectSchemas(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "missing required on object with properties gains empty array",
			in:   `{"type":"object","properties":{"id":{"type":"string"}}}`,
			want: `{"type":"object","properties":{"id":{"type":"string"}},"required":[]}`,
		},
		{
			name: "bare object gains empty required",
			in:   `{"type":"object","properties":{}}`,
			want: `{"type":"object","properties":{},"required":[]}`,
		},
		{
			name: "existing required is kept",
			in:   `{"type":"object","properties":{"a":{"type":"string"}},"required":["a"]}`,
			want: `{"type":"object","properties":{"a":{"type":"string"}},"required":["a"]}`,
		},
		{
			name: "null required becomes empty array",
			in:   `{"type":"object","properties":{},"required":null}`,
			want: `{"type":"object","properties":{},"required":[]}`,
		},
		{
			name: "nested objects are normalized",
			in:   `{"type":"object","properties":{"cfg":{"type":"object","properties":{"x":{"type":"string"}}}},"required":["cfg"]}`,
			want: `{"type":"object","properties":{"cfg":{"type":"object","properties":{"x":{"type":"string"}},"required":[]}},"required":["cfg"]}`,
		},
		{
			name: "non-object schemas are untouched apart from object children",
			in:   `{"type":"array","items":{"type":"object","properties":{"q":{"type":"string"}}}}`,
			want: `{"type":"array","items":{"type":"object","properties":{"q":{"type":"string"}},"required":[]}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var in map[string]any
			if err := json.Unmarshal([]byte(tt.in), &in); err != nil {
				t.Fatal(err)
			}
			got := normalizeObjectSchemas(in)
			b, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			var gotN, wantN any
			if err := json.Unmarshal(b, &gotN); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(tt.want), &wantN); err != nil {
				t.Fatal(err)
			}
			if string(mustJSON(gotN)) != string(mustJSON(wantN)) {
				t.Fatalf("got  %s\nwant %s", mustJSON(gotN), mustJSON(wantN))
			}
		})
	}
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
