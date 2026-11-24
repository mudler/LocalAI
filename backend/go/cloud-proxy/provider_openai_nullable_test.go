package main

import (
	"encoding/json"
	"testing"

	. "github.com/onsi/gomega"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// A tool schema as LocalAI's built-in tools produce it (jsonschema-go marks
// slices nullable). Mistral rejects `"type":["null","array"]` with code 3260.
const nullableToolSchema = `[{"type":"function","function":{"name":"embed","description":"Embed texts.",` +
	`"parameters":{"type":"object","required":["input"],"properties":{` +
	`"input":{"description":"The texts to embed.","type":["null","array"],"items":{"type":"string"}},` +
	`"model":{"type":"string"},` +
	`"either":{"type":["string","integer","null"]},` +
	`"nested":{"type":"object","properties":{"tags":{"type":["array","null"],"items":{"type":"string"}}}}}}}}]`

func sentToolSchema(t *testing.T, tools string) map[string]any {
	t.Helper()
	g := NewWithT(t)
	srv, captured := fakeOpenAIUpstream(t, func(_ openAIRequest) (int, string, string) {
		return 200, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`, "application/json"
	})
	defer srv.Close()
	cp := newTranslateCloudProxy(t, srv.URL)
	_, err := cp.Predict(&pb.PredictOptions{Messages: []*pb.Message{{Role: "user", Content: "x"}}, Tools: tools})
	g.Expect(err).NotTo(HaveOccurred())
	var got []map[string]any
	g.Expect(json.Unmarshal(captured.Tools, &got)).To(Succeed())
	g.Expect(got).To(HaveLen(1))
	return got[0]["function"].(map[string]any)["parameters"].(map[string]any)
}

func TestPredict_OpenAI_CollapsesNullableToolTypes(t *testing.T) {
	g := NewWithT(t)
	params := sentToolSchema(t, nullableToolSchema)
	props := params["properties"].(map[string]any)

	g.Expect(props["input"].(map[string]any)["type"]).To(Equal("array"))
	g.Expect(props["input"].(map[string]any)["items"]).To(Equal(map[string]any{"type": "string"}))
	nested := props["nested"].(map[string]any)["properties"].(map[string]any)
	g.Expect(nested["tags"].(map[string]any)["type"]).To(Equal("array"))
	// A real union keeps its non-null members.
	g.Expect(props["either"].(map[string]any)["type"]).To(Equal([]any{"string", "integer"}))
	// Untouched parts stay as they were.
	g.Expect(props["model"].(map[string]any)["type"]).To(Equal("string"))
	g.Expect(params["required"]).To(Equal([]any{"input"}))
}

func TestPredict_OpenAI_DropsNullRequired(t *testing.T) {
	g := NewWithT(t)
	// Shape of the built-in list_files tool: no required parameters, emitted as null.
	params := sentToolSchema(t, `[{"type":"function","function":{"name":"list_files","parameters":`+
		`{"type":"object","required":null,"properties":{"path":{"type":"string","enum":null}}}}}]`)
	g.Expect(params).NotTo(HaveKey("required"))
	g.Expect(params["properties"].(map[string]any)["path"]).To(Equal(map[string]any{"type": "string"}))
}

func TestPredict_OpenAI_PlainToolSchemaUnchanged(t *testing.T) {
	g := NewWithT(t)
	plain := `[{"type":"function","function":{"name":"f","parameters":{"type":"object","properties":{"q":{"type":"string"}}}}}]`
	params := sentToolSchema(t, plain)
	g.Expect(params).To(Equal(map[string]any{"type": "object", "properties": map[string]any{"q": map[string]any{"type": "string"}}}))
	g.Expect(collapseNullableTypes(json.RawMessage(`not json`))).To(Equal(json.RawMessage(`not json`)))
	g.Expect(collapseNullableTypes(nil)).To(BeNil())
}
