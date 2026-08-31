package cluster_test

import (
	"errors"
	"io"
	"net"
	"time"

	"github.com/mudler/LocalAI/core/services/cluster"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Splice", func() {
	// pipePair returns two connected in-memory conns.
	newPair := func() (net.Conn, net.Conn) { return net.Pipe() }

	It("copies bytes in both directions", func() {
		aLeft, aRight := newPair()
		bLeft, bRight := newPair()

		done := make(chan error, 1)
		go func() { done <- cluster.Splice(aRight, bLeft) }()

		go func() {
			_, _ = aLeft.Write([]byte("ping"))
		}()
		buf := make([]byte, 4)
		Expect(bRight.SetReadDeadline(time.Now().Add(5 * time.Second))).To(Succeed())
		_, err := io.ReadFull(bRight, buf)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(buf)).To(Equal("ping"))

		go func() {
			_, _ = bRight.Write([]byte("pong"))
		}()
		Expect(aLeft.SetReadDeadline(time.Now().Add(5 * time.Second))).To(Succeed())
		_, err = io.ReadFull(aLeft, buf)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(buf)).To(Equal("pong"))

		Expect(aLeft.Close()).To(Succeed())
		Eventually(done, "5s").Should(Receive())
	})

	It("returns when one side closes, and closes the other", func() {
		aLeft, aRight := newPair()
		bLeft, bRight := newPair()

		done := make(chan error, 1)
		go func() { done <- cluster.Splice(aRight, bLeft) }()

		Expect(aLeft.Close()).To(Succeed())
		Eventually(done, "5s").Should(Receive(BeNil()))

		// The far side must have been closed too, so a read there fails
		// rather than blocking forever. The read runs in a goroutine and is
		// polled instead of carrying a read deadline: net.Pipe refuses to set
		// a deadline once *either* end is closed, so a deadline here would
		// fail exactly when Splice did its job.
		reads := make(chan error, 1)
		go func() {
			_, err := bRight.Read(make([]byte, 1))
			reads <- err
		}()
		var err error
		Eventually(reads, "5s").Should(Receive(&err))
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, io.EOF)).To(BeTrue())
	})

	It("does not leak a goroutine when both sides close", func() {
		aLeft, aRight := newPair()
		bLeft, bRight := newPair()

		done := make(chan error, 1)
		go func() { done <- cluster.Splice(aRight, bLeft) }()

		Expect(aLeft.Close()).To(Succeed())
		Expect(bRight.Close()).To(Succeed())
		Eventually(done, "5s").Should(Receive())
	})

	// The three specs above only ever tear down an idle splice: at the moment
	// of Close no copy is parked inside a Write. A relayed inference response
	// is the opposite case, a reader that walks away mid-body while 50MB is
	// still being pushed at it, so this covers the direction that is blocked
	// in Write rather than in Read when its peer disappears.
	It("returns when the reader disappears while a write is in flight", func() {
		aLeft, aRight := newPair()
		bLeft, bRight := newPair()

		done := make(chan error, 1)
		go func() { done <- cluster.Splice(aRight, bLeft) }()

		// Nothing ever reads from bRight, so the a->b direction parks inside
		// Write on an unbuffered pipe with the payload half-delivered.
		payload := make([]byte, 1<<20)
		writes := make(chan error, 1)
		go func() {
			_, err := aLeft.Write(payload)
			writes <- err
		}()

		Expect(bRight.Close()).To(Succeed())
		Eventually(done, "5s").Should(Receive())

		// The abandoned writer must be released as well, and only Splice
		// closing its end can do that: no deadline is set on aLeft, so a
		// splice that forgot to close would leave this write parked forever.
		Eventually(writes, "5s").Should(Receive(HaveOccurred()))
	})
})
