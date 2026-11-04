// SPDX-License-Identifier: MIT
package contract

import (
	"strings"

	model "github.com/mudler/LocalAI/pkg/addons/model/executionstatus"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Execution status payload", func() {
	It("validates every enum pair and normalizes nonavailable isolation", func() {
		for _, r := range []string{"available", "unavailable", "unknown"} {
			for _, i := range []string{"isolated", "host", "unknown"} {
				v, err := DecodeExecutionStatus([]byte(`{"readiness":"` + r + `","isolation":"` + i + `","explanation":"` + strings.Repeat("界", 200) + `"}`))
				Expect(err).NotTo(HaveOccurred())
				Expect(v.Readiness).To(Equal(r))
				if r != "available" {
					Expect(v.Isolation).To(Equal("unknown"))
				} else {
					Expect(v.Isolation).To(Equal(i))
				}
			}
		}
	})
	DescribeTable("rejects invalid payloads without partial success", func(b string) {
		v, err := DecodeExecutionStatus([]byte(b))
		Expect(err).To(HaveOccurred())
		Expect(v.Readiness).To(Equal("unknown"))
		Expect(v.Isolation).To(Equal("unknown"))
	},
		Entry("missing isolation", `{"readiness":"available"}`),
		Entry("missing readiness", `{"isolation":"host"}`),
		Entry("bad readiness", `{"readiness":"ok","isolation":"host"}`),
		Entry("bad isolation", `{"readiness":"available","isolation":"container"}`),
		Entry("duplicate", `{"readiness":"available","readiness":"unknown","isolation":"host"}`),
		Entry("unknown field", `{"readiness":"available","isolation":"host","ok":true}`),
		Entry("outputs", `{"readiness":"available","isolation":"host","outputs":[]}`),
		Entry("trailing", `{"readiness":"available","isolation":"host"}{}`),
		Entry("null", `null`),
		Entry("null explanation", `{"readiness":"available","isolation":"host","explanation":null}`),
		Entry("empty explanation", `{"readiness":"available","isolation":"host","explanation":""}`),
		Entry("oversized explanation", `{"readiness":"available","isolation":"host","explanation":"`+strings.Repeat("界", 201)+`"}`),
		Entry("markup", `{"readiness":"available","isolation":"host","explanation":"<b>OK</b>"}`),
		Entry("control", `{"readiness":"available","isolation":"host","explanation":"a\nb"}`),
		Entry("direction", `{"readiness":"available","isolation":"host","explanation":"a\u202eb"}`),
		Entry("oversized payload", strings.Repeat(" ", 2049)+`{}`),
	)
	It("reports network-policy enforcement only for an available isolated provider", func() {
		for raw, want := range map[string]bool{
			`{"readiness":"available","isolation":"isolated","networkPolicyEnforced":true}`:   true,
			`{"readiness":"available","isolation":"isolated","networkPolicyEnforced":false}`:  false,
			`{"readiness":"available","isolation":"isolated"}`:                                false,
			`{"readiness":"available","isolation":"host","networkPolicyEnforced":true}`:       false,
			`{"readiness":"unavailable","isolation":"isolated","networkPolicyEnforced":true}`: false,
		} {
			v, err := DecodeExecutionStatus([]byte(raw))
			Expect(err).NotTo(HaveOccurred(), raw)
			Expect(v.NetworkPolicyEnforced).To(Equal(want), raw)
		}
	})
	It("refuses a non-boolean enforcement claim", func() {
		v, err := DecodeExecutionStatus([]byte(`{"readiness":"available","isolation":"isolated","networkPolicyEnforced":"yes"}`))
		Expect(err).To(HaveOccurred())
		Expect(v.NetworkPolicyEnforced).To(BeFalse())
	})
	It("bridges validation into the existing pure model boundary", func() {
		for _, tc := range []struct {
			raw   string
			valid bool
			value model.Value
		}{
			{`{"readiness":"available","isolation":"host"}`, true, model.Value{Readiness: model.Available, Isolation: model.Host}},
			{`{"readiness":"unavailable","isolation":"isolated"}`, true, model.Value{Readiness: model.Unavailable, Isolation: model.UnknownIsolation}},
			{`{"readiness":"available","isolation":"host","outputs":[]}`, false, model.Unknown()},
		} {
			v, err := DecodeExecutionStatus([]byte(tc.raw))
			payload := model.InvalidPayload()
			if err == nil {
				payload = model.ValidPayload(model.Value{Readiness: model.Readiness(v.Readiness), Isolation: model.Isolation(v.Isolation)})
			}
			b := model.Binding{Addon: "renamed"}
			tr := model.Reduce(model.Pending{B: b, Q: "q", Deadline: 10}, model.Reply{B: b, Q: "q", Payload: payload, Now: 1})
			if tc.valid {
				Expect(tr.Rule).To(Equal(model.R3))
			} else {
				Expect(tr.Rule).To(Equal(model.R4))
			}
		}
	})
})
