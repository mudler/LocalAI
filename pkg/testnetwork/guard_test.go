// SPDX-License-Identifier: MIT

package testnetwork_test

import (
	"context"
	"errors"
	"net"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/pkg/testnetwork"
)

var _ = Describe("Guard", func() {
	It("blocks a public IP before dialing", func() {
		_, err := testnetwork.LocalGuard().DialContext(context.Background(), "tcp", "203.0.113.1:443")
		Expect(err).To(MatchError(ContainSubstring("public dial blocked")))
	})

	It("allows loopback fixtures", func() {
		guard := testnetwork.LocalGuard()
		called := false
		guard.Dial = func(_ context.Context, network, address string) (net.Conn, error) {
			called = true
			Expect(network).To(Equal("tcp"))
			Expect(address).To(Equal("127.0.0.1:8080"))
			return nil, errors.New("fixture dial sentinel")
		}
		_, err := guard.DialContext(context.Background(), "tcp", "127.0.0.1:8080")
		Expect(err).To(MatchError("fixture dial sentinel"))
		Expect(called).To(BeTrue())
	})
})
