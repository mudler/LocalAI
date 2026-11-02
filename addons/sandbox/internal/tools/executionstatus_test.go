// SPDX-License-Identifier: MIT
package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mudler/LocalAI/addons/sandbox/internal/backends"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

type typedSettings struct {
	Settings
	status backends.Status
}

func (s typedSettings) Status() backends.Status { return s.status }

func TestTypedExecutionStatus(t *testing.T) {
	for _, tc := range []struct{ active, readiness, isolation string }{
		{"gvisor", "available", "isolated"}, {"kvm", "available", "isolated"}, {"host", "available", "host"}, {"", "unavailable", "unknown"}, {"future", "unknown", "unknown"},
	} {
		t.Run(tc.active+tc.readiness, func(t *testing.T) {
			r := New(nil, typedSettings{status: backends.Status{Active: tc.active}}, nil)
			var env contract.Envelope
			env.Request.OperationID = "typed-read"
			env.Request.Arguments = json.RawMessage(`{"tool":"execution-status"}`)
			result, err := r.Read(context.Background(), env)
			if err != nil {
				t.Fatal(err)
			}
			status, err := contract.DecodeExecutionStatus(result.Payload)
			if err != nil {
				t.Fatal(err)
			}
			if status.Readiness != tc.readiness || status.Isolation != tc.isolation {
				t.Fatalf("unexpected status: %+v", status)
			}
		})
	}
}
