// SPDX-License-Identifier: MIT
package gateway

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Wire request payloads", func() {
	It("serves exactly one routing payload per request", func() {
		Expect(wireRequest{}.exclusive()).To(BeFalse())
		Expect(wireRequest{Apps: &appsCall{}}.exclusive()).To(BeTrue())
		Expect(wireRequest{Apps: &appsCall{}, Relay: &RelayRequest{}}.exclusive()).To(BeFalse())
		Expect(wireRequest{Library: &LibraryCall{}, Resource: &ResourceCall{}}.exclusive()).To(BeFalse())
		Expect(wireRequest{Proposal: &ProposalCall{}, Artifact: &artifactCall{}}.exclusive()).To(BeFalse())
	})
})
