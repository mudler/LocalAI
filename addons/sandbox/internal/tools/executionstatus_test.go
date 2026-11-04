// SPDX-License-Identifier: MIT
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/sandbox/internal/backends"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

type typedSettings struct {
	Settings
	status backends.Status
}

func (s typedSettings) Status() backends.Status { return s.status }

var _ = Describe("typed execution status", func() {
	for _, tc := range []struct {
		active, readiness, isolation string
		enforced                     bool
	}{
		{"gvisor", "available", "isolated", true}, {"kvm", "available", "isolated", true}, {"host", "available", "host", false}, {"", "unavailable", "unknown", false}, {"future", "unknown", "unknown", false},
	} {
		It(fmt.Sprintf("reports %s as %s with %s isolation", tc.active, tc.readiness, tc.isolation), func() {
			r := New(nil, typedSettings{status: backends.Status{Active: tc.active}}, nil)
			var env contract.Envelope
			env.Request.OperationID = "typed-read"
			env.Request.Arguments = json.RawMessage(`{"tool":"execution-status"}`)
			result, err := r.Read(context.Background(), env)
			Expect(err).NotTo(HaveOccurred())
			status, err := contract.DecodeExecutionStatus(result.Payload)
			Expect(err).NotTo(HaveOccurred())
			Expect(status.Readiness).To(Equal(tc.readiness))
			Expect(status.Isolation).To(Equal(tc.isolation))
			Expect(status.NetworkPolicyEnforced).To(Equal(tc.enforced))
		})
	}
})

var _ = Describe("typed execution status without settings", func() {
	It("reports unavailable with a reason, not unknown, when the add-on started without settings", func() {
		r := New(nil, nil, nil)
		var env contract.Envelope
		env.Request.OperationID = "typed-read"
		env.Request.Arguments = json.RawMessage(`{"tool":"execution-status"}`)
		result, err := r.Read(context.Background(), env)
		Expect(err).NotTo(HaveOccurred())
		status, err := contract.DecodeExecutionStatus(result.Payload)
		Expect(err).NotTo(HaveOccurred())
		Expect(status.Readiness).To(Equal("unavailable"))
		Expect(status.Explanation).NotTo(BeEmpty())
		Expect(status.NetworkPolicyEnforced).To(BeFalse())
	})
	It("explains an active backend it does not know", func() {
		r := New(nil, typedSettings{status: backends.Status{Active: "future"}}, nil)
		var env contract.Envelope
		env.Request.OperationID = "typed-read"
		env.Request.Arguments = json.RawMessage(`{"tool":"execution-status"}`)
		result, err := r.Read(context.Background(), env)
		Expect(err).NotTo(HaveOccurred())
		status, err := contract.DecodeExecutionStatus(result.Payload)
		Expect(err).NotTo(HaveOccurred())
		Expect(status.Readiness).To(Equal("unknown"))
		Expect(status.Explanation).NotTo(BeEmpty())
	})
})
