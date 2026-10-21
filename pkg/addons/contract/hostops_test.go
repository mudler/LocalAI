// SPDX-License-Identifier: MIT
package contract

import (
	"encoding/json"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("host operation wire types", func() {
	It("round-trip", func() {
		req := HostRequest{OperationID: "op1", Op: "finetune.get", Arguments: json.RawMessage(`{"jobId":"j1"}`)}
		b, err := json.Marshal(req)
		Expect(err).ToNot(HaveOccurred())
		got, err := DecodeHostRequest(b, 1<<20)
		Expect(err).ToNot(HaveOccurred())
		Expect(got.OperationID).To(Equal("op1"))
		Expect(got.Op).To(Equal("finetune.get"))
		Expect(string(got.Arguments)).To(Equal(`{"jobId":"j1"}`))

		resp := HostResponse{Error: &HostError{Code: "scope", Message: "no"}}
		b, _ = json.Marshal(resp)
		var back HostResponse
		Expect(json.Unmarshal(b, &back)).To(Succeed())
		Expect(back.Error).ToNot(BeNil())
		Expect(back.Error.Code).To(Equal("scope"))
		Expect(back.Error.Error()).ToNot(BeEmpty())

		b, _ = json.Marshal(HostResponse{Result: json.RawMessage(`{"ok":true}`)})
		Expect(json.Unmarshal(b, &back)).To(Succeed())
		Expect(string(back.Result)).To(Equal(`{"ok":true}`))
	})

	DescribeTable("the strict decoder refuses what it does not know",
		func(body string) {
			_, err := DecodeHostRequest([]byte(body), 1<<20)
			Expect(err).To(HaveOccurred(), "accepted")
		},
		Entry("unknown field", `{"operationId":"a","op":"finetune.get","arguments":{},"userId":"x"}`),
		Entry("duplicate key", `{"operationId":"a","operationId":"b","op":"finetune.get"}`),
		Entry("trailing", `{"operationId":"a","op":"finetune.get"} {}`),
		Entry("not an object", `[]`),
		Entry("empty", ``),
	)

	It("refuses an oversized body", func() {
		_, err := DecodeHostRequest([]byte(`{"operationId":"a","op":"finetune.get"}`), 10)
		Expect(err).To(HaveOccurred(), "an oversized body was accepted")
	})

	DescribeTable("an operation name has the dotted shape",
		func(op string, want bool) {
			Expect(ValidHostOp(op)).To(Equal(want), "%q", op)
		},
		Entry("finetune.get", "finetune.get", true),
		Entry("quantize.start", "quantize.start", true),
		Entry("finetune.a.b", "finetune.a.b", true),
		Entry("empty", "", false),
		Entry("no dot", "finetune", false),
		Entry("leading dot", ".get", false),
		Entry("uppercase", "Finetune.get", false),
		Entry("trailing space", "finetune.get ", false),
		Entry("slash", "finetune/get", false),
		Entry("trailing newline", "finetune.get\n", false),
		Entry("hyphen", "fine-tune.get", false),
		Entry("empty verb", "finetune.", false),
		Entry("too long", strings.Repeat("a", 70)+".get", false),
	)
})
