// SPDX-License-Identifier: MIT
package systemone

import (
	"encoding/json"
	"errors"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/schema"
)

func validRequest() *schema.SystemOneRequest {
	return &schema.SystemOneRequest{State: json.RawMessage(`{"text":"hello"}`), Questions: map[string]schema.SystemOneQuestion{"q": {Type: "noul", Criteria: json.RawMessage(`{"false":"absent","true":"present"}`)}}}
}
func TestSystemOne(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "SystemOne validation")
}

var _ = Describe("Shared validation", func() {
	It("preserves structured request fields", func() {
		req := validRequest()
		before, _ := json.Marshal(req)
		Expect(ValidateRequest(req)).To(Succeed())
		after, _ := json.Marshal(req)
		Expect(after).To(Equal(before))
	})
	It("rejects malformed and unbounded requests with typed errors", func() {
		cases := []func(*schema.SystemOneRequest){
			func(r *schema.SystemOneRequest) { r.State = nil },
			func(r *schema.SystemOneRequest) { r.State = json.RawMessage(` null `) },
			func(r *schema.SystemOneRequest) { r.State = json.RawMessage(`" "`) },
			func(r *schema.SystemOneRequest) { r.Questions = nil },
			func(r *schema.SystemOneRequest) { r.Questions["q"] = schema.SystemOneQuestion{Type: "other"} },
			func(r *schema.SystemOneRequest) {
				r.Questions["q"] = schema.SystemOneQuestion{Type: "noul", Criteria: json.RawMessage(`{"yes":"yes"}`)}
			},
			func(r *schema.SystemOneRequest) { r.State, _ = json.Marshal(strings.Repeat("a", MaxBodyBytes)) },
			func(r *schema.SystemOneRequest) {
				for i := 0; i < MaxQuestions; i++ {
					r.Questions[strings.Repeat("q", i+2)] = schema.SystemOneQuestion{Type: "noul"}
				}
			},
		}
		for _, mutate := range cases {
			req := validRequest()
			mutate(req)
			err := ValidateRequest(req)
			var typed *ValidationError
			Expect(errors.As(err, &typed)).To(BeTrue())
			Expect(typed.Kind).To(Equal(InvalidRequest))
		}
		Expect(ValidateRequest(nil)).NotTo(Succeed())
	})
	It("requires explicit native usecase but preserves legacy HTTP admission", func() {
		c := config.ModelConfig{}
		c.Name = "decision"
		c.Backend = "vllm-cpp"
		Expect(ModelAllowed(c)).To(Succeed())
		Expect(ValidateDecisionModel(c)).NotTo(Succeed())
		flags := config.FLAG_DECISIONS
		c.KnownUsecases = &flags
		Expect(ValidateDecisionModel(c)).To(Succeed())
		c.Backend = "does-not-exist"
		var typed *ValidationError
		Expect(errors.As(ValidateDecisionModel(c), &typed)).To(BeTrue())
		Expect(typed.Kind).To(Equal(UnsupportedBackend))
		flags = config.FLAG_TOKEN_CLASSIFY
		c.Backend = "vllm-cpp"
		Expect(ValidateDecisionModel(c)).NotTo(Succeed())
		Expect(ModelAllowed(c)).To(Succeed())
		Expect(UsesDecisionPipeline(c)).To(BeFalse())
	})
})
