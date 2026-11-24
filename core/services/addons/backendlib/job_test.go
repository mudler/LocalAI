// SPDX-License-Identifier: MIT
package backendlib

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/galleryop"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// jobLog keeps every status of every job and the cancels it holds.
type jobLog struct {
	mu      sync.Mutex
	logs    map[string][]galleryop.OpStatus
	cancels map[string]context.CancelFunc
	removed []string
}

func newJobLog() *jobLog {
	return &jobLog{logs: map[string][]galleryop.OpStatus{}, cancels: map[string]context.CancelFunc{}}
}

func (j *jobLog) UpdateStatus(id string, op *galleryop.OpStatus) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.logs[id] = append(j.logs[id], *op)
}

func (j *jobLog) StoreCancellation(id string, c context.CancelFunc) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.cancels[id] = c
}

func (j *jobLog) RemoveCancellation(id string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	delete(j.cancels, id)
	j.removed = append(j.removed, id)
}

func (j *jobLog) last(id string) galleryop.OpStatus {
	j.mu.Lock()
	defer j.mu.Unlock()
	h := j.logs[id]
	if len(h) == 0 {
		return galleryop.OpStatus{}
	}
	return h[len(h)-1]
}

func (j *jobLog) cancel(id string) context.CancelFunc {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.cancels[id]
}

func (j *jobLog) wasRemoved(id string) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, r := range j.removed {
		if r == id {
			return true
		}
	}
	return false
}

// gatedInstall reports the fetch step and waits for its gate or the end of
// its context.
type gatedInstall struct {
	gate chan struct{}
	err  error
	mu   sync.Mutex
	ctx  context.Context
}

func (g *gatedInstall) Install(ctx context.Context, _, _, _ string, p Progress) error {
	g.mu.Lock()
	g.ctx = ctx
	g.mu.Unlock()
	p.step(StepFetching)
	if p.Bytes != nil {
		p.Bytes("image", "1 MB", "2 MB", 50)
	}
	select {
	case <-g.gate:
	case <-ctx.Done():
		return ctx.Err()
	}
	p.step(StepWriting)
	return g.err
}

type actorKey struct{}

var _ = Describe("Library install jobs", func() {
	job := Job{SourceID: "lib", Backend: "whisper", Version: "0.2"}

	It("runs one install of a backend name at a time, whoever asks", func() {
		g := &gatedInstall{gate: make(chan struct{})}
		log := newJobLog()
		r := &JobRunner{Install: g.Install}
		first, err := r.Start(context.Background(), log, job)
		Expect(err).NotTo(HaveOccurred())
		// A second caller of the same runner, with the same backend from
		// another library, is refused while the first runs.
		_, err = r.Start(context.Background(), log, Job{SourceID: "other", Backend: "whisper", Version: "1"})
		Expect(err).To(MatchError(ErrInstalling))
		close(g.gate)
		Eventually(func() bool { return log.last(first).Processed }).Should(BeTrue())
		Eventually(func() error {
			_, err := r.Start(context.Background(), log, Job{SourceID: "other", Backend: "whisper", Version: "1"})
			return err
		}).Should(Succeed())
	})

	It("writes the step messages, the bytes of the fetch and the result", func() {
		g := &gatedInstall{gate: make(chan struct{})}
		log := newJobLog()
		r := &JobRunner{Install: g.Install}
		id, err := r.Start(context.Background(), log, Job{SourceID: "lib", Backend: "whisper", Version: "0.2", Upgrade: true})
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() float64 { return log.last(id).Progress }).Should(Equal(50.0))
		st := log.last(id)
		Expect(st.Phase).To(Equal(StepFetching))
		Expect(st.Message).To(Equal(StepMessage(StepFetching)))
		Expect(st.GalleryElementName).To(Equal("whisper"))
		Expect(st.Upgrade).To(BeTrue())
		Expect(st.Cancellable).To(BeTrue())
		close(g.gate)
		Eventually(func() bool { return log.last(id).Processed }).Should(BeTrue())
		Expect(log.last(id).Message).To(Equal(galleryop.MessageCompleted))
		Expect(log.wasRemoved(id)).To(BeTrue(), "the cancel of an ended job is dropped")
	})

	It("keeps the failed step as the phase and words the reason for the administrator", func() {
		g := &gatedInstall{gate: make(chan struct{}), err: fmt.Errorf("%w: %q", ErrCollision, "whisper")}
		close(g.gate)
		log := newJobLog()
		id, err := (&JobRunner{Install: g.Install}).Start(context.Background(), log, job)
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() bool { return log.last(id).Processed }).Should(BeTrue())
		st := log.last(id)
		Expect(st.Phase).To(Equal(StepWriting))
		Expect(st.Error).To(HaveOccurred())
		Expect(st.Message).To(HavePrefix("error: "))
		Expect(Reason(catalog.ErrNotFound)).To(ContainSubstring("no such entry"))
	})

	It("cancels through the cancel it registers, and keeps the values of the caller's context", func() {
		g := &gatedInstall{gate: make(chan struct{})}
		log := newJobLog()
		parent, stop := context.WithCancel(context.WithValue(context.Background(), actorKey{}, "admin"))
		id, err := (&JobRunner{Install: g.Install}).Start(parent, log, job)
		Expect(err).NotTo(HaveOccurred())
		// The caller's request ends; the install goes on.
		stop()
		Eventually(func() float64 { return log.last(id).Progress }).Should(Equal(50.0))
		g.mu.Lock()
		Expect(g.ctx.Value(actorKey{})).To(Equal("admin"))
		Expect(g.ctx.Err()).NotTo(HaveOccurred())
		g.mu.Unlock()
		log.cancel(id)()
		Eventually(func() bool { return log.last(id).Cancelled }).Should(BeTrue())
		Expect(log.wasRemoved(id)).To(BeTrue())
	})

	It("guards a synchronous install with the same names", func() {
		g := &gatedInstall{gate: make(chan struct{})}
		r := &JobRunner{Install: g.Install}
		_, err := r.Start(context.Background(), newJobLog(), job)
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Run(context.Background(), job)).To(MatchError(ErrInstalling))
		close(g.gate)
		Eventually(func() error { return r.Run(context.Background(), job) }).Should(Succeed())
	})

	It("needs an install and a status", func() {
		_, err := (&JobRunner{}).Start(context.Background(), newJobLog(), job)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, ErrInstalling)).To(BeFalse())
	})
})
