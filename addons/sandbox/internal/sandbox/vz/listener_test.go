package vz

import (
	"context"
	"io"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/guestagent"
	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox/vz/vmm"
)

// The host listens on the agent port only to read the ready frame. A guest
// process can open connections to any host port, so the listener must take
// one connection and then go.
var _ = Describe("the ready listener of the guest agent port", func() {
	It("is removed once the guest is ready", func() {
		f := &farm{}
		b := testBackend(f, vmm.HostLimits{})
		newFixture(b, nil).create()
		m := f.last()
		Expect(m.listening(guestagent.AgentPort)).To(BeFalse(), "the listener stayed after the ready frame")
		_, ok := m.connect(guestagent.AgentPort)
		Expect(ok).To(BeFalse(), "a late connection was accepted")
		// The host still dials the guest on the same port.
		c, err := m.Dial(context.Background(), guestagent.AgentPort)
		Expect(err).To(Succeed())
		_ = c.Close()
	})

	It("closes every connection after the first at once and spawns nothing", func() {
		f := &farm{configure: func(m *fakeMachine) { m.silent = true }}
		b := testBackend(f, vmm.HostLimits{})
		b.opts.ReadyTimeout = 30 * time.Second
		fx := newFixture(b, nil)
		errc := make(chan error, 1)
		go func() {
			sb, err := b.Create(context.Background(), fx.spec)
			if err == nil {
				DeferCleanup(func() { _ = sb.Destroy(context.Background()) })
			}
			errc <- err
		}()
		var m *fakeMachine
		Eventually(func() bool {
			m = f.last()
			return m != nil && m.listening(guestagent.AgentPort)
		}, 5*time.Second, time.Millisecond).Should(BeTrue())
		first, ok := m.connect(guestagent.AgentPort)
		Expect(ok).To(BeTrue())
		defer func() { _ = first.Close() }()
		// Let the goroutine of the first connection start, then count.
		time.Sleep(50 * time.Millisecond)
		before := runtime.NumGoroutine()
		for range 500 {
			c, ok := m.connect(guestagent.AgentPort)
			if !ok {
				break // the listener is gone: also fine
			}
			_ = c.SetReadDeadline(time.Now().Add(time.Second))
			_, err := c.Read(make([]byte, 1))
			Expect(err).To(Equal(io.EOF), "an extra connection was kept open")
			_ = c.Close()
		}
		Expect(runtime.NumGoroutine()).To(BeNumerically("<=", before+3), "extra connections spawned goroutines")
		// The first connection still carries the ready frame.
		Expect(guestagent.SendReady(first)).To(Succeed())
		Eventually(errc, 5*time.Second).Should(Receive(BeNil()))
		Expect(m.listening(guestagent.AgentPort)).To(BeFalse())
	})
})
