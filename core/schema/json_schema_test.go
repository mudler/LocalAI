package schema_test

import (
	"encoding/json"

	. "github.com/mudler/LocalAI/core/schema"
	functions "github.com/mudler/LocalAI/pkg/functions"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("JsonSchema response format", func() {
	// Shaped like the payload the OpenAI SDK sends for a Pydantic model with a
	// nested model: properties reference #/$defs through $ref, so the grammar
	// can only be built when $defs survives decoding.
	const pydanticPayload = `{
		"type": "json_schema",
		"json_schema": {
			"name": "Recipe",
			"strict": true,
			"schema": {
				"type": "object",
				"properties": {
					"name": {"type": "string"},
					"ingredients": {"type": "array", "items": {"$ref": "#/$defs/Ingredient"}}
				},
				"required": ["name", "ingredients"],
				"additionalProperties": false,
				"$defs": {
					"Ingredient": {
						"type": "object",
						"properties": {"text": {"type": "string"}},
						"required": ["text"],
						"additionalProperties": false
					}
				}
			}
		}
	}`

	It("keeps $defs when decoding the request schema", func() {
		req := JsonSchemaRequest{}
		Expect(json.Unmarshal([]byte(pydanticPayload), &req)).To(Succeed())

		dat, err := json.Marshal(req.JsonSchema.Schema)
		Expect(err).NotTo(HaveOccurred())

		decoded := map[string]any{}
		Expect(json.Unmarshal(dat, &decoded)).To(Succeed())
		Expect(decoded).To(HaveKey("$defs"))
		Expect(decoded["$defs"]).To(HaveKey("Ingredient"))
	})

	It("builds a grammar instead of falling back to unconstrained output", func() {
		req := JsonSchemaRequest{}
		Expect(json.Unmarshal([]byte(pydanticPayload), &req)).To(Succeed())

		dat, err := json.Marshal(req.JsonSchema.Schema)
		Expect(err).NotTo(HaveOccurred())
		decoded := map[string]any{}
		Expect(json.Unmarshal(dat, &decoded)).To(Succeed())
		defs, _ := decoded["$defs"].(map[string]any)

		// Same structure chat.go builds from the decoded schema.
		fs := functions.JSONFunctionStructure{
			AnyOf: []functions.Item{req.JsonSchema.Schema},
			Defs:  defs,
		}
		grammar, err := fs.Grammar()
		Expect(err).NotTo(HaveOccurred())
		Expect(grammar).NotTo(BeEmpty())
	})
})
