// SPDX-License-Identifier: MIT
package runner

import (
	"context"
	"errors"
	"sync"

	"github.com/mudler/LocalAI/core/services/agentruntime/nibadapter"
	pc "github.com/mudler/LocalAI/core/services/agentruntime/privatecheckpoint"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type shellFake struct {
	mu                      sync.Mutex
	run, joined             chan struct{}
	cancel, exports, closes int
	joinErr, restoreErr     error
	joinOnce                sync.Once
	runOnce                 sync.Once
	callback                func()
}

func (f *shellFake) Run(context.Context, []byte) error { <-f.run; return nil }
func (f *shellFake) Cancel()                           { f.mu.Lock(); f.cancel++; f.mu.Unlock() }
func (f *shellFake) AwaitQuiescence(ctx context.Context) error {
	select {
	case <-f.joined:
		return f.joinErr
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (f *shellFake) Export() (nibadapter.Checkpoint, []byte, error) {
	f.mu.Lock()
	f.exports++
	f.mu.Unlock()
	return nibadapter.Checkpoint{}, nil, errors.New("private-failure")
}
func (f *shellFake) Close() error { f.mu.Lock(); f.closes++; f.mu.Unlock(); return nil }
func (f *shellFake) New(_ context.Context, _ identity, _ *pc.PrivatePayload, cb func()) (nativeSession, error) {
	f.callback = cb
	return f, f.restoreErr
}
func (f *shellFake) finishRun()         { f.runOnce.Do(func() { close(f.run) }) }
func (f *shellFake) finishJoin()        { f.joinOnce.Do(func() { close(f.joined) }) }
func (f *shellFake) counts() (int, int) { f.mu.Lock(); defer f.mu.Unlock(); return f.cancel, f.exports }

type shellHost struct {
	mu            sync.Mutex
	next, settled int
	deny, success bool
	nextItem      *handoff
}

func (h *shellHost) Check(context.Context, identity) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.deny {
		return errors.New("private-failure")
	}
	return nil
}
func (h *shellHost) SaveRequest(context.Context, identity) (pc.SaveRequest, error) {
	return pc.SaveRequest{}, errors.New("private-failure")
}
func (h *shellHost) Settle(context.Context, settlementRequest) (*handoff, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.settled++
	if h.success {
		return h.nextItem, nil
	}
	return nil, errors.New("private-failure")
}
func (h *shellHost) StartNext(context.Context, handoff) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.next++
	return nil
}
func (h *shellHost) Now() uint64 { return 1 }

var _ = Describe("Runner effect shell", func() {
	var f *shellFake
	var h *shellHost
	var c *coordinator
	var cancel context.CancelFunc
	BeforeEach(func() {
		f = &shellFake{run: make(chan struct{}), joined: make(chan struct{})}
		h = &shellHost{}
		ctx, stop := context.WithCancel(context.Background())
		cancel = stop
		var e error
		c, e = newCoordinator(ctx, fixture(), shellDependencies{Factory: f, Authority: h, Provenance: h, Settlement: h, Handoff: h, Clock: h, Codec: &pc.Service{}}, nil)
		Expect(e).NotTo(HaveOccurred())
	})
	AfterEach(func() { cancel(); f.finishRun(); f.finishJoin(); c.waitWorkers() })
	It("fails closed for missing trusted dependencies", func() {
		_, e := newCoordinator(context.Background(), fixture(), shellDependencies{}, nil)
		Expect(e).To(HaveOccurred())
	})
	It("rejects a typed nil session factory", func() {
		var n *shellFake
		d := c.deps
		d.Factory = n
		_, e := newCoordinator(context.Background(), fixture(), d, nil)
		Expect(e).To(HaveOccurred())
	})
	It("constructs one fresh session for the immutable attempt", func() {
		Expect(c.begin()).To(Succeed())
		Eventually(c.phase).Should(Equal("Running"))
		Expect(c.begin()).To(Succeed())
	})
	It("detaches without cancelling the server turn", func() {
		Expect(c.begin()).To(Succeed())
		Eventually(c.phase).Should(Equal("Running"))
		Expect(c.detach()).To(Succeed())
		a, b := f.counts()
		Expect(a).To(BeZero())
		Expect(b).To(BeZero())
	})
	It("rejects stale epoch callbacks", func() {
		Expect(c.begin()).To(Succeed())
		Eventually(c.phase).Should(Equal("Running"))
		id := fixture().fields().id.clone()
		id.fields().epoch++
		Expect(c.deliver("TurnFinished", id, nil)).To(Succeed())
		Expect(c.phase()).To(Equal("Running"))
	})
	It("rejects stale attempt callbacks", func() {
		Expect(c.begin()).To(Succeed())
		Eventually(c.phase).Should(Equal("Running"))
		id := fixture().fields().id.clone()
		id.fields().attempt++
		Expect(c.deliver("TurnFinished", id, nil)).To(Succeed())
		Expect(c.phase()).To(Equal("Running"))
	})
	It("stop cancels and waits for genuine join", func() {
		Expect(c.begin()).To(Succeed())
		Eventually(c.phase).Should(Equal("Running"))
		Expect(c.stop()).To(Succeed())
		Eventually(func() int { a, _ := f.counts(); return a }).Should(Equal(1))
		Expect(c.phase()).To(Equal("Cancelling"))
		_, b := f.counts()
		Expect(b).To(BeZero())
	})
	It("lease loss cancels and never releases FIFO", func() {
		Expect(c.begin()).To(Succeed())
		Eventually(c.phase).Should(Equal("Running"))
		Expect(c.leaseLost()).To(Succeed())
		Eventually(func() int { a, _ := f.counts(); return a }).Should(Equal(1))
		Expect(c.phase()).To(Equal("Cancelling"))
	})
	It("timeout never supplies quiescence", func() {
		Expect(c.begin()).To(Succeed())
		Eventually(c.phase).Should(Equal("Running"))
		Expect(c.stop()).To(Succeed())
		Expect(c.timeout()).To(Succeed())
		Expect(c.phase()).To(Equal("Interrupted"))
		_, b := f.counts()
		Expect(b).To(BeZero())
	})
	It("unknown journal holds input and blocks ordinary admission", func() {
		Expect(c.begin()).To(Succeed())
		Eventually(c.phase).Should(Equal("Running"))
		Expect(c.unknown()).To(Succeed())
		Expect(c.begin()).To(Succeed())
		Expect(c.phase()).To(Equal("Reconciliation"))
		Expect(c.input()).To(Equal([]byte("private-input")))
	})
	It("failed session restore holds input", func() {
		f.restoreErr = errors.New("private-failure")
		Expect(c.begin()).To(Succeed())
		Eventually(c.phase).Should(Equal("Interrupted"))
		Expect(c.input()).To(Equal([]byte("private-input")))
	})
	It("cancellation rejects late streaming callbacks", func() {
		Expect(c.begin()).To(Succeed())
		Eventually(c.phase).Should(Equal("Running"))
		Expect(c.stop()).To(Succeed())
		f.callback()
		Expect(c.phase()).To(Equal("Cancelling"))
	})
	It("server lifetime cancellation cancels independently of attachment", func() {
		Expect(c.begin()).To(Succeed())
		Eventually(c.phase).Should(Equal("Running"))
		cancel()
		Eventually(func() int { a, _ := f.counts(); return a }).Should(BeNumerically(">=", 1))
	})
	It("authorization failure never constructs or starts a session", func() {
		h.deny = true
		Expect(c.begin()).To(Succeed())
		Eventually(c.phase).Should(Equal("Interrupted"))
		a, b := f.counts()
		Expect(a + b).To(BeZero())
	})
	It("failed join cannot export or release FIFO", func() {
		Expect(c.begin()).To(Succeed())
		Eventually(c.phase).Should(Equal("Running"))
		f.joinErr = errors.New("private-failure")
		Expect(c.stop()).To(Succeed())
		f.finishJoin()
		Eventually(c.phase).Should(Equal("Interrupted"))
		_, b := f.counts()
		Expect(b).To(BeZero())
	})
	It("seal failure retains input and does not settle", func() {
		Expect(c.begin()).To(Succeed())
		Eventually(c.phase).Should(Equal("Running"))
		f.finishRun()
		f.finishJoin()
		Eventually(c.phase).Should(Equal("Interrupted"))
		Expect(c.input()).To(Equal([]byte("private-input")))
		Expect(h.settled).To(BeZero())
	})
	It("settlement failure holds input and FIFO", func() {
		d := fixture()
		d.fields().ciphertext = []byte("cipher")
		c.mu.Lock()
		c.state = settling{d}
		c.execute(makeEffect("SettleCiphertext", d))
		c.mu.Unlock()
		Eventually(c.phase).Should(Equal("Interrupted"))
		Expect(c.input()).To(Equal([]byte("private-input")))
		Expect(h.next).To(BeZero())
	})
	It("successful settlement hands off a fresh identity without rebinding old state", func() {
		d := fixture()
		d.fields().ciphertext = []byte("cipher")
		next := fixture()
		next.fields().id.fields().attempt++
		next.fields().input = []byte("next-input")
		h.success = true
		h.nextItem = &handoff{initial: next}
		c.mu.Lock()
		c.state = settling{d}
		c.execute(makeEffect("SettleCiphertext", d))
		c.mu.Unlock()
		Eventually(c.phase).Should(Equal("Idle"))
		Eventually(func() int { h.mu.Lock(); defer h.mu.Unlock(); return h.next }).Should(Equal(1))
		c.mu.Lock()
		Expect(c.state.payload().fields().id.equal(d.fields().id)).To(BeTrue())
		c.mu.Unlock()
		Expect(c.input()).To(BeEmpty())
	})
	It("private shell diagnostics do not retain plaintext", func() {
		Expect(shellDiagnostic(c)).NotTo(ContainSubstring("private-input"))
		Expect(shellDiagnostic(c)).NotTo(ContainSubstring("private-principal"))
	})
})
