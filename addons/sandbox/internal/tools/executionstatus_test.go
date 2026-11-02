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
	for _, tc := range []struct{ active, readiness, isolation string }{
		{"gvisor", "available", "isolated"}, {"kvm", "available", "isolated"}, {"host", "available", "host"}, {"", "unavailable", "unknown"}, {"future", "unknown", "unknown"},
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
		})
	}
})
