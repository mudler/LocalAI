// SPDX-License-Identifier: MIT

package cluster

import (
	"net"
	"time"

	"github.com/libp2p/go-yamux/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// This spec is in-package because the header deadline it exercises is a
// production constant measured in seconds, and a spec that waited it out would
// be the slowest in the suite. The seam is unexported for the same reason the
// worker tunnel's is: it is a test knob, not an operator knob.
var _ = Describe("A peer stream that never says what it wants", func() {
	It("is refused rather than left holding a relay goroutine", func() {
		// Without a deadline on the opening frame, a peer that opens a stream
		// and then goes quiet parks a goroutine and a stream slot until the
		// whole session dies. A peer need not be malicious to do it: a dialler
		// killed between OpenStream and its first write leaves exactly this.
		relay := newRelay(NewTunnelRegistry(nil, "me"), 50*time.Millisecond, 0)
		store := NewSessionStore(relay.Stream)
		DeferCleanup(store.CloseAll)

		a, b := net.Pipe()
		accepted, err := yamux.Server(a, nil, nil)
		Expect(err).ToNot(HaveOccurred())
		peer, err := yamux.Client(b, nil, nil)
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() {
			_ = peer.Close()
			_ = accepted.Close()
		})
		store.Accept("peer-1", accepted)

		stream, err := peer.OpenStream(GinkgoT().Context())
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = stream.Close() })

		// Read with no deadline of our own: what is being asserted is that the
		// RELAY answered, and a deadline here would be satisfied by a stream
		// left parked just as well.
		replies := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			replies <- ReadRelayReply(stream)
		}()
		var reply error
		Eventually(replies, "10s").Should(Receive(&reply))
		Expect(reply).To(MatchError(ErrRelayRequestInvalid))

		ends := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			_, err := stream.Read(make([]byte, 1))
			ends <- err
		}()
		Eventually(ends, "10s").Should(Receive(HaveOccurred()))
	})
})
