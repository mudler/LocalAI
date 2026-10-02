package schema

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestSystemOnePreservesStructuredInput(t *testing.T) {
	for _, input := range []string{
		`{"state":{"messages":[{"content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,AA=="}}]}]},"questions":{"q":{"type":"choice","instructions":{"text":"choose"},"criteria":{"yes":null,"no":"negative"}}},"images":["data:image/png;base64,AA=="]}`,
		`{"state":"text","questions":{},"images":[]}`,
		`{"state":"text","questions":{},"images":null}`,
		`{"state":"text","questions":{}}`,
	} {
		t.Run(input, func(t *testing.T) {
			var req SystemOneRequest
			if err := json.Unmarshal([]byte(input), &req); err != nil {
				t.Fatal(err)
			}
			output, err := json.Marshal(req)
			if err != nil {
				t.Fatal(err)
			}
			var want, got map[string]interface{}
			json.Unmarshal([]byte(input), &want)
			json.Unmarshal(output, &got)
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("round trip changed input: want %s, got %s", input, output)
			}
		})
	}
}

func TestSystemOnePermutePreservesImages(t *testing.T) {
	input := `{"request":{"state":"text","questions":{},"images":[{"url":"data:image/png;base64,AA=="}]},"question":"q"}`
	var req SystemOnePermuteRequest
	if err := json.Unmarshal([]byte(input), &req); err != nil {
		t.Fatal(err)
	}
	output, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var want, got interface{}
	json.Unmarshal([]byte(input), &want)
	json.Unmarshal(output, &got)
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("round trip changed request: %s", output)
	}
}
