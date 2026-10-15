package schema_test

import (
	"encoding/json"
	"reflect"
	"strings"

	"github.com/mudler/LocalAI/core/schema"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ElevenLabs sound generation request", func() {
	It("accepts string parameters", func() {
		var request schema.ElevenLabsSoundGenerationRequest
		Expect(json.Unmarshal([]byte(`{"text":"song","params":{"style":"jazz","cot":"true"}}`), &request)).To(Succeed())
		Expect(request.Params).To(Equal(map[string]string{"style": "jazz", "cot": "true"}))
	})

	It("rejects non-string parameter values", func() {
		var request schema.ElevenLabsSoundGenerationRequest
		err := json.Unmarshal([]byte(`{"text":"song","params":{"cot":true}}`), &request)
		Expect(err).To(MatchError(ContainSubstring("cannot unmarshal bool into Go struct field ElevenLabsSoundGenerationRequest.params of type string")))
	})

	It("does not expose source audio", func() {
		requestType := reflect.TypeOf(schema.ElevenLabsSoundGenerationRequest{})
		for i := 0; i < requestType.NumField(); i++ {
			field := requestType.Field(i)
			Expect(strings.Split(field.Tag.Get("json"), ",")[0]).ToNot(Equal("src"))
		}
	})
})
