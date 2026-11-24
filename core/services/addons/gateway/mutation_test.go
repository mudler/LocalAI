// SPDX-License-Identifier: MIT
package gateway

import (
	"errors"

	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("mutation classification", func() {
	It("treats every action except the known reads as a mutation", func() {
		for _, action := range []string{"install", "enable", "disable", "uninstall", "confirm", "forget", "stop", "unknown-future-action"} {
			var unknown *OutcomeUnknown
			e := mutationOutcome(contract.Request{Action: action, OperationID: "op-" + action}, ErrUnavailable)
			Expect(errors.As(e, &unknown)).To(BeTrue(), "action %s: %v", action, e)
			Expect(unknown.OperationID).To(Equal("op-" + action))
		}
		for _, action := range []string{"read", "operation-status"} {
			Expect(mutationOutcome(contract.Request{Action: action, OperationID: "x"}, ErrUnavailable)).To(MatchError(ErrUnavailable))
		}
	})
})
