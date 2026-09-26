package nodes

import (
	"context"
	"net"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// A frontend keeps two caches of one *http.Client per worker: the control
// client's, built on the first verb issued to a node, and the HTTP file
// stager's, built on the first file staged to it. Both are keyed by node ID and
// neither used to be pruned, so each grew once per distinct worker for the life
// of the process. A worker pool that re-registers under a new id on every
// rollout is not bounded by the fleet.
//
// Each cached entry is more than a map slot: it holds an http.Transport whose
// idle connections are streams on that worker's tunnel, kept until
// IdleConnTimeout even after the tunnel has gone.
var _ = Describe("per-node HTTP client caches", func() {
	// aDialer stands in for a worker's tunnel dialler. It is never called: what
	// these specs assert is which client the cache hands back, not what that
	// client can reach.
	aDialer := func(string) func(ctx context.Context, network, addr string) (net.Conn, error) {
		return func(context.Context, string, string) (net.Conn, error) { return nil, nil }
	}

	Describe("ControlClient", func() {
		var c *ControlClient

		BeforeEach(func() { c = NewControlClient(aDialer, "token") })

		It("reuses one client per node until that node departs", func() {
			first, err := c.clientFor("node-1")
			Expect(err).NotTo(HaveOccurred())
			again, err := c.clientFor("node-1")
			Expect(err).NotTo(HaveOccurred())
			Expect(again).To(BeIdenticalTo(first),
				"a verb issued twice must reuse the tunnel stream the transport already holds")
		})

		It("drops a departed node's client rather than holding it for the life of the process", func() {
			first, err := c.clientFor("node-1")
			Expect(err).NotTo(HaveOccurred())

			c.ForgetNode("node-1")
			Expect(c.clients).NotTo(HaveKey("node-1"))

			rebuilt, err := c.clientFor("node-1")
			Expect(err).NotTo(HaveOccurred())
			Expect(rebuilt).NotTo(BeIdenticalTo(first),
				"a worker that comes back gets a client over whatever tunnel it has now")
		})

		It("leaves the other nodes' clients alone", func() {
			kept, err := c.clientFor("node-2")
			Expect(err).NotTo(HaveOccurred())
			_, err = c.clientFor("node-1")
			Expect(err).NotTo(HaveOccurred())

			c.ForgetNode("node-1")

			again, err := c.clientFor("node-2")
			Expect(err).NotTo(HaveOccurred())
			Expect(again).To(BeIdenticalTo(kept))
		})

		It("is a no-op for a node it never built a client for", func() {
			Expect(func() { c.ForgetNode("never-seen") }).NotTo(Panic())
			Expect(c.clients).To(BeEmpty())
		})
	})

	Describe("HTTPFileStager", func() {
		var h *HTTPFileStager

		BeforeEach(func() {
			h = NewHTTPFileStager(func(string) (string, error) { return "worker:1", nil }, "token", aDialer)
		})

		It("reuses one client per node until that node departs", func() {
			first, err := h.clientFor("node-1")
			Expect(err).NotTo(HaveOccurred())
			again, err := h.clientFor("node-1")
			Expect(err).NotTo(HaveOccurred())
			Expect(again).To(BeIdenticalTo(first),
				"a client built per request would open a fresh tunnel stream for every chunk of a multi-gigabyte upload")
		})

		It("drops a departed node's client rather than holding it for the life of the process", func() {
			first, err := h.clientFor("node-1")
			Expect(err).NotTo(HaveOccurred())

			h.ForgetNode("node-1")
			Expect(h.clients).NotTo(HaveKey("node-1"))

			rebuilt, err := h.clientFor("node-1")
			Expect(err).NotTo(HaveOccurred())
			Expect(rebuilt).NotTo(BeIdenticalTo(first))
		})

		It("leaves the other nodes' clients alone", func() {
			kept, err := h.clientFor("node-2")
			Expect(err).NotTo(HaveOccurred())
			_, err = h.clientFor("node-1")
			Expect(err).NotTo(HaveOccurred())

			h.ForgetNode("node-1")

			again, err := h.clientFor("node-2")
			Expect(err).NotTo(HaveOccurred())
			Expect(again).To(BeIdenticalTo(kept))
		})
	})

	Describe("S3FileStager", func() {
		It("does not drop the control client's entry on behalf of a stager that has no state", func() {
			// The object-store stager keeps nothing per node; its only per-node
			// client belongs to the ControlClient, which the deployment shares
			// with every other control verb and registers separately. Dropping
			// it from here as well would evict a cache this stager does not own
			// and would do it twice per departure, invisibly.
			control := NewControlClient(aDialer, "token")
			kept, err := control.clientFor("node-1")
			Expect(err).NotTo(HaveOccurred())

			NewS3FileStager(nil, control).ForgetNode("node-1")

			again, err := control.clientFor("node-1")
			Expect(err).NotTo(HaveOccurred())
			Expect(again).To(BeIdenticalTo(kept))
		})
	})
})
