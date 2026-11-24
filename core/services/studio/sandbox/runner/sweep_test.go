//go:build linux || darwin

package runner_test

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/sandbox"
	"github.com/mudler/LocalAI/core/services/studio/sandbox/runner"
	"github.com/mudler/LocalAI/core/services/studio/sandbox/sbtest"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// flakyBackend makes sandboxes whose Suspend fails with err, and counts the
// sandboxes it made and the suspends it saw.
type flakyBackend struct {
	sandbox.Backend
	mu       sync.Mutex
	err      error
	created  int
	suspends int
}

func (b *flakyBackend) Create(ctx context.Context, spec sandbox.Spec) (sandbox.Sandbox, error) {
	sb, err := b.Backend.Create(ctx, spec)
	if err != nil {
		return nil, err
	}
	b.mu.Lock()
	b.created++
	b.mu.Unlock()
	return &flakySandbox{Sandbox: sb, b: b}, nil
}

func (b *flakyBackend) counts() (created, suspends int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.created, b.suspends
}

type flakySandbox struct {
	sandbox.Sandbox
	b *flakyBackend
}

func (s *flakySandbox) Suspend(context.Context) (sandbox.SnapshotRef, error) {
	s.b.mu.Lock()
	defer s.b.mu.Unlock()
	s.b.suspends++
	return sandbox.SnapshotRef{}, s.b.err
}

var _ = Describe("the sweep of idle sandboxes", func() {
	var (
		rig *scopedRig
		fb  *flakyBackend
		m   *runner.Manager
		now time.Time
	)
	BeforeEach(func() {
		rig = newScopedRig(workspace.ModeWrite)
		fb = &flakyBackend{Backend: sbtest.NewFakeBackend(GinkgoT().TempDir())}
		now = time.Now()
		var err error
		m, err = runner.New(runner.Options{
			Backend:   func() (sandbox.Backend, error) { return fb, nil },
			Store:     rig.st,
			Actor:     rig.actor,
			View:      rig.view,
			IdleAfter: time.Minute,
			Now:       func() time.Time { return now },
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = m.Close(context.Background()) })
		rig.m = m
		rig.must("echo one")
		now = now.Add(2 * time.Minute)
	})

	It("drops a sandbox whose guest is gone, so the next call makes a fresh one", func() {
		fb.err = errors.Join(sandbox.ErrGone, errors.New("the guest exited"))
		m.Sweep(context.Background())
		Expect(rig.must("echo two")).To(ContainSubstring("two"))
		created, _ := fb.counts()
		Expect(created).To(Equal(2))
	})

	for name, cause := range map[string]error{
		"a failed checkpoint": errors.New("remote: no request is being served"),
		"a busy sandbox":      sandbox.ErrBusy,
	} {
		It("waits before it suspends again after "+name+", and keeps the sandbox usable", func() {
			fb.err = cause
			m.Sweep(context.Background())
			now = now.Add(time.Minute)
			m.Sweep(context.Background())
			_, suspends := fb.counts()
			Expect(suspends).To(Equal(1), "the next sweep a minute later does not try again")
			now = now.Add(time.Hour)
			m.Sweep(context.Background())
			_, suspends = fb.counts()
			Expect(suspends).To(Equal(2), "it tries again after the wait")
			Expect(rig.must("echo three")).To(ContainSubstring("three"))
			created, _ := fb.counts()
			Expect(created).To(Equal(1), "the sandbox that could not sleep still serves")
		})
	}
})
