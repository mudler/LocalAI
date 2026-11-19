package p2psplit

import (
	"context"
	"net"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/p2pnet"
)

func internalHost() *p2pnet.Host {
	GinkgoHelper()
	h, err := p2pnet.New(context.Background(), p2pnet.Config{
		IdentityPath: filepath.Join(GinkgoT().TempDir(), "identity.key"),
		ListenAddrs:  []string{"/ip4/127.0.0.1/tcp/0"},
		Gate:         p2pnet.NewAllowlist(),
	})
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(h.Close)
	return h
}

var _ = Describe("the forwarder of a worker", func() {
	It("keeps its port when the need of the worker changes", func() {
		remote, server := internalHost(), internalHost()
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		m := &Manager{cfg: Config{Host: server, Ports: NewPortBook()}, ctx: ctx}
		d := Device{ID: "d1", Peer: remote.ID()}
		w := &worker{}

		m.mu.Lock()
		first := m.llamaForwarderLocked(d, w)
		m.mu.Unlock()
		Expect(first).ToNot(BeEmpty())
		Expect(w.fwdNeed).To(Equal(p2pnet.NeedBulk))

		// "Use anyway" on a relay of the user changes the need of the worker.
		d.AllowSlowPath, w.own = true, true
		m.mu.Lock()
		second := m.llamaForwarderLocked(d, w)
		m.mu.Unlock()
		Expect(w.fwdNeed).To(Equal(anywayNeed))
		Expect(second).To(Equal(first), "the new forwarder binds the port of the old one")
		DeferCleanup(w.fwd.Close)
	})
})

var _ = Describe("closing the manager", func() {
	It("does not wait for a connection that has sent no hello", func() {
		server := internalHost()
		m, err := NewManager(context.Background(), Config{
			Host:    server,
			Devices: func(context.Context) ([]Device, error) { return nil, nil },
		})
		Expect(err).ToNot(HaveOccurred())

		// A connection that a handler waits on for ever: nothing is sent. The
		// handler is part of m.wg, as the ones of the listeners are.
		a, b := net.Pipe()
		DeferCleanup(func() { _ = a.Close(); _ = b.Close() })
		m.wg.Go(func() {
			m.tracked(func(c net.Conn) { _, _ = c.Read(make([]byte, 1)) })(b)
		})
		Eventually(func() int {
			m.connMu.Lock()
			defer m.connMu.Unlock()
			return len(m.conns)
		}).Should(Equal(1))

		closed := make(chan struct{})
		go func() {
			defer GinkgoRecover()
			_ = m.Close()
			close(closed)
		}()
		Eventually(closed, 10*time.Second).Should(BeClosed())
	})
})
