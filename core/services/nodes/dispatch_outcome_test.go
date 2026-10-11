// SPDX-License-Identifier: MIT
package nodes

import (
	"context"
	"errors"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/diagnostics"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var _ = Describe("Dispatch outcome regression", func() {
	It("classifies context and gRPC cancellation", func() {
		for _, tc := range []struct {
			err  error
			want diagnostics.Outcome
		}{
			{nil, diagnostics.OutcomeOK},
			{errors.New("private"), diagnostics.OutcomeError},
			{status.Error(codes.Unavailable, "private"), diagnostics.OutcomeError},
			{context.Canceled, diagnostics.OutcomeCanceled},
			{context.DeadlineExceeded, diagnostics.OutcomeCanceled},
			{status.Error(codes.Canceled, "private"), diagnostics.OutcomeCanceled},
			{status.Error(codes.DeadlineExceeded, "private"), diagnostics.OutcomeCanceled},
			{fmt.Errorf("wrapped: %w", status.Error(codes.Canceled, "private")), diagnostics.OutcomeCanceled},
		} {
			Expect(dispatchOutcome(tc.err)).To(Equal(tc.want), "error: %v", tc.err)
		}
	})
})
