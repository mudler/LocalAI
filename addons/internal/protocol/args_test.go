// SPDX-License-Identifier: MIT
package protocol_test

import (
	"encoding/json"
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	. "github.com/mudler/LocalAI/addons/internal/protocol"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

var _ = Describe("tool argument helpers", func() {
	envWith := func(args string) contract.Envelope {
		var env contract.Envelope
		env.Request.Arguments = json.RawMessage(args)
		return env
	}

	It("encodes a payload", func() {
		Expect(string(Payload(map[string]any{"ok": true}))).To(Equal(`{"ok":true}`))
	})

	It("reads the tool of a request", func() {
		tool, err := ToolOf(envWith(`{"tool":"x-list","name":"a"}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(tool).To(Equal("x-list"))
	})

	DescribeTable("refuses a request with no tool",
		func(args string) {
			_, err := ToolOf(envWith(args))
			Expect(err).To(MatchError(ErrBadRequest))
		},
		Entry("empty tool", `{"tool":""}`),
		Entry("no tool", `{}`),
		Entry("not an object", `[]`),
		Entry("no arguments", ``),
	)

	It("decodes arguments into a closed struct", func() {
		var a struct {
			Name string `json:"name"`
		}
		Expect(DecodeArgs(json.RawMessage(`{"name":"a"}`), &a)).To(Succeed())
		Expect(a.Name).To(Equal("a"))
		Expect(DecodeArgs(nil, &a)).To(Succeed())
	})

	DescribeTable("refuses arguments that do not match",
		func(args string) {
			var a struct {
				Name string `json:"name"`
			}
			Expect(DecodeArgs(json.RawMessage(args), &a)).To(MatchError(ErrBadRequest))
		},
		Entry("unknown field", `{"name":"a","other":1}`),
		Entry("trailing value", `{"name":"a"} {}`),
		Entry("wrong type", `{"name":1}`),
	)

	DescribeTable("maps a host refusal to a protocol error",
		func(code string, want error) {
			err := FromHostError(fmt.Errorf("op: %w", &contract.HostError{Code: code, Message: "why"}))
			Expect(err).To(MatchError(want))
			Expect(err.Error()).To(HaveSuffix(": why"))
		},
		Entry("bad request", contract.HostBadRequest, ErrBadRequest),
		Entry("too large", contract.HostTooLarge, ErrBadRequest),
		Entry("not found", contract.HostNotFound, ErrNotFound),
		Entry("conflict", contract.HostConflict, ErrConflict),
		Entry("feature", contract.HostFeature, ErrForbidden),
		Entry("scope", contract.HostScope, ErrForbidden),
	)

	It("keeps any other failure as it is", func() {
		other := errors.New("boom")
		Expect(FromHostError(other)).To(BeIdenticalTo(other))
		unavailable := &contract.HostError{Code: contract.HostUnavailable, Message: "later"}
		Expect(FromHostError(unavailable)).To(BeIdenticalTo(error(unavailable)))
	})
})
