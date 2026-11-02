// SPDX-License-Identifier: MIT
package music

import (
	"context"
	"errors"
	"fmt"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type ownedTestExecution struct {
	out     chan StageOutcome
	ack     chan error
	stopped chan struct{}
	once    sync.Once
	issuer  *CapabilityIssuer
}

func newOwnedExecution() *ownedTestExecution {
	return &ownedTestExecution{make(chan StageOutcome, 1), make(chan error, 1), make(chan struct{}), sync.Once{}, nil}
}
func (e *ownedTestExecution) Outcome() <-chan StageOutcome { return e.out }
func (e *ownedTestExecution) Settled() <-chan error        { return e.ack }
func (e *ownedTestExecution) Stop()                        { e.once.Do(func() { close(e.stopped) }) }

type ownedTestLease struct {
	execution   *ownedTestExecution
	started     chan StageRequest
	startErr    error
	releaseTask *CleanupTask
}

func (l *ownedTestLease) Start(_ context.Context, r StageRequest) (StageExecution, error) {
	l.started <- r
	if l.execution == nil {
		return nil, l.startErr
	}
	return l.execution, l.startErr
}
func (l *ownedTestLease) Release(context.Context) CleanupTask {
	if l.releaseTask != nil {
		return *l.releaseTask
	}
	return releasedTask(nil)
}

type ownedTestBackend struct {
	stages       chan StageRequest
	leases       []*ownedTestLease
	checked      chan struct{}
	denied       bool
	rechecked    *Selection
	admissionErr error
}

func (b *ownedTestBackend) Admit(_ context.Context, r StageRequest) (StageLease, error) {
	b.stages <- r
	if b.denied || len(b.leases) == 0 {
		return nil, errors.New("capacity")
	}
	l := b.leases[0]
	b.leases = b.leases[1:]
	return l, b.admissionErr
}
func (b *ownedTestBackend) CheckGeneration(_ context.Context, r ValidatedRequest) (Selection, error) {
	b.checked <- struct{}{}
	if b.rechecked != nil {
		return *b.rechecked, nil
	}
	return r.Generation, nil
}

type ownedTestStorage struct {
	cleaned                     chan struct{}
	prepareGate                 chan struct{}
	prepared                    chan struct{}
	fetchErr                    error
	cleanErr                    error
	cleanAck                    chan error
	issuer                      *CapabilityIssuer
	mu                          sync.Mutex
	commits, revokes, artifacts int
	cleanedHandles              []Candidate
	preparedHandles             []PreparedOutput
	tombstone                   bool
	revokeAck                   chan error
	revokeEntered               chan struct{}
	preparedFirst               bool
	artifactAck                 chan error
	artifactErr                 error
	replacement                 bool
	committedGate               chan struct{}
	commitEntered               chan struct{}
}

func (s *ownedTestStorage) Issuer() *CapabilityIssuer { return s.issuer }
func releasedTask(err error) CleanupTask {
	ch := make(chan error, 1)
	ch <- nil
	return CleanupTask{err, ch}
}
func (s *ownedTestStorage) CleanInput(context.Context, string, OwnedInput) CleanupTask {
	s.cleaned <- struct{}{}
	if s.cleanAck != nil {
		return CleanupTask{s.cleanErr, s.cleanAck}
	}
	return releasedTask(s.cleanErr)
}
func (s *ownedTestStorage) FetchValidate(_ context.Context, _ string, c Candidate) (Candidate, error) {
	if s.replacement {
		return s.issuer.Candidate("request", GenerationStage, "replacement"), s.fetchErr
	}
	return c, s.fetchErr
}
func (s *ownedTestStorage) Prepare(_ context.Context, id string, _ Candidate) (PreparedOutput, error) {
	if s.prepared != nil {
		s.prepared <- struct{}{}
	}
	if s.prepareGate != nil {
		<-s.prepareGate
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tombstone {
		return PreparedOutput{}, errors.New("revoked tombstone")
	}
	return s.issuer.Prepared(id, "private-prepared"), nil
}
func (s *ownedTestStorage) Revoke(context.Context, string) CleanupTask {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revokes++
	s.tombstone = true
	if s.revokeEntered != nil {
		close(s.revokeEntered)
	}
	if s.revokeAck != nil {
		return CleanupTask{Released: s.revokeAck}
	}
	return releasedTask(nil)
}
func (s *ownedTestStorage) CleanArtifact(_ context.Context, _ string, h Candidate, p PreparedOutput) CleanupTask {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.artifacts++
	s.cleanedHandles = append(s.cleanedHandles, h)
	s.preparedHandles = append(s.preparedHandles, p)
	if s.artifactAck != nil {
		return CleanupTask{s.artifactErr, s.artifactAck}
	}
	return releasedTask(s.artifactErr)
}
func (s *ownedTestStorage) Commit(context.Context, string, PreparedOutput) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.commitEntered != nil {
		close(s.commitEntered)
	}
	if s.committedGate != nil {
		<-s.committedGate
	}
	s.commits++
	return "output-id", nil
}
func goodRunnerRequest(op Operation) ValidatedRequest {
	sel := Selection{"canonical", "canonical", true, true, true, true}
	return ValidatedRequest{ID: "request", Principal: "principal", Operation: op, Transcription: sel, Generation: sel, Deadline: time.Now().Add(time.Minute), Feature: true, Quota: true, InputsValid: true, StorageReady: true}
}

type runnerAnswer struct {
	result Result
	err    error
}

func startOwnedRunner(ctx context.Context, r *Runner, in ValidatedRequest) <-chan runnerAnswer {
	if in.Source.token == nil {
		in.Source = r.Storage.Issuer().Input(in.ID, "source")
	}
	done := make(chan runnerAnswer, 1)
	go func() { res, err := r.Run(ctx, in); done <- runnerAnswer{res, err} }()
	return done
}
func newRunnerFixture(count int) (*Runner, *ownedTestBackend, *ownedTestStorage, []*ownedTestLease) {
	b := &ownedTestBackend{stages: make(chan StageRequest, 4), checked: make(chan struct{}, 1)}
	s := &ownedTestStorage{cleaned: make(chan struct{}, 4), issuer: NewCapabilityIssuer()}
	ls := make([]*ownedTestLease, count)
	for i := range ls {
		ls[i] = &ownedTestLease{execution: newOwnedExecution(), started: make(chan StageRequest, 1)}
		ls[i].execution.issuer = s.issuer
	}
	b.leases = append([]*ownedTestLease(nil), ls...)
	return &Runner{Backend: b, Storage: s, Publisher: s}, b, s, ls
}
func receiveStarted(l *ownedTestLease) StageRequest {
	var r StageRequest
	Eventually(l.started, time.Second).Should(Receive(&r))
	return r
}
func finishOwned(l *ownedTestLease, st Stage) {
	abc := "X:1\nK:C\n C D E F |\n"
	out := StageOutcome{ABC: &abc, Valid: true}
	if st == GenerationStage {
		out.Candidate = l.execution.issuer.Candidate("request", st, "private-audio")
	}
	l.execution.out <- out
	l.execution.ack <- nil
}

type admissionRaceBackend struct {
	lease   StageLease
	entered chan struct{}
	release chan struct{}
}

func (b *admissionRaceBackend) Admit(context.Context, StageRequest) (StageLease, error) {
	close(b.entered)
	<-b.release
	return b.lease, nil
}
func (*admissionRaceBackend) CheckGeneration(context.Context, ValidatedRequest) (Selection, error) {
	panic("unexpected recheck")
}

type releaseGatedLease struct {
	gate     chan struct{}
	released chan struct{}
	starts   int
}

func (l *releaseGatedLease) Start(context.Context, StageRequest) (StageExecution, error) {
	l.starts++
	panic("cancelled lease must not start")
}
func (l *releaseGatedLease) Release(context.Context) CleanupTask {
	<-l.gate
	close(l.released)
	return releasedTask(nil)
}

var _ = Describe("Music runner ownership", Label("traces"), func() {
	for _, op := range []Operation{Transcription, Generation, Cover} {
		op := op
		It("runs the real reducer with explicit settlement for workflow "+string(rune('0'+op)), func() {
			n := 1
			if op == Cover {
				n = 2
			}
			r, b, s, ls := newRunnerFixture(n)
			rules := []string{}
			r.Trace = func(t Transition) { rules = append(rules, t.Rule) }
			done := startOwnedRunner(context.Background(), r, goodRunnerRequest(op))
			first := receiveStarted(ls[0])
			finishOwned(ls[0], first.Stage)
			if op == Cover {
				next := receiveStarted(ls[1])
				Expect(next.Stage).To(Equal(GenerationStage))
				Expect(next.Generation.ABC).NotTo(BeNil())
				Expect(*next.Generation.ABC).To(Equal("X:1\nK:C\n C D E F |\n"))
				Eventually(b.checked, time.Second).Should(Receive())
				Eventually(s.cleaned, time.Second).Should(Receive())
				finishOwned(ls[1], GenerationStage)
			}
			var a runnerAnswer
			Eventually(done, time.Second).Should(Receive(&a))
			Expect(a.err).NotTo(HaveOccurred())
			Expect(a.result.ABC).NotTo(BeNil())
			Expect(rules).To(ContainElement("R3"))
			if op != Transcription {
				Expect(rules).To(ContainElement("R10"))
				Expect(a.result.OutputID).To(Equal("output-id"))
			}
		})
	}
	It("does not clean, hand off or return on outcome without actual settlement", func() {
		r, _, s, ls := newRunnerFixture(2)
		reduced := make(chan string, 32)
		r.Trace = func(t Transition) { reduced <- t.Rule }
		done := startOwnedRunner(context.Background(), r, goodRunnerRequest(Cover))
		receiveStarted(ls[0])
		abc := "X:1\nK:C\nC|\n"
		ls[0].execution.out <- StageOutcome{ABC: &abc, Valid: true}
		Eventually(ls[0].execution.stopped, time.Second).Should(BeClosed())
		Expect(s.cleaned).NotTo(Receive())
		Expect(ls[1].started).NotTo(Receive())
		Expect(done).NotTo(Receive())
		ls[0].execution.ack <- nil
		receiveStarted(ls[1])
		finishOwned(ls[1], GenerationStage)
		Eventually(done, time.Second).Should(Receive())
	})
	It("retains ownership after cancellation and preserves captured ABC", func() {
		r, _, s, ls := newRunnerFixture(1)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := startOwnedRunner(ctx, r, goodRunnerRequest(Transcription))
		receiveStarted(ls[0])
		abc := "X:1\nK:C\nC|\n"
		ls[0].execution.out <- StageOutcome{ABC: &abc, Valid: true}
		Eventually(ls[0].execution.stopped, time.Second).Should(BeClosed())
		cancel()
		Expect(done).NotTo(Receive())
		Expect(s.cleaned).NotTo(Receive())
		ls[0].execution.ack <- nil
		var a runnerAnswer
		Eventually(done, time.Second).Should(Receive(&a))
		Expect(a.err).To(HaveOccurred())
		Expect(a.result.ABC).NotTo(BeNil())
		Expect(*a.result.ABC).To(Equal(abc))
	})
	It("preflights both cover models before any admission", func() {
		r, b, _, _ := newRunnerFixture(0)
		in := goodRunnerRequest(Cover)
		in.Generation.Authorized = false
		in.Source = r.Storage.Issuer().Input(in.ID, "source")
		_, err := r.Run(context.Background(), in)
		Expect(err).To(MatchError(ErrPreflight))
		Expect(b.stages).NotTo(Receive())
	})
	It("treats fetch failure as fatal and retains exact ABC", func() {
		r, _, s, ls := newRunnerFixture(1)
		s.fetchErr = errors.New("fetch failed")
		done := startOwnedRunner(context.Background(), r, goodRunnerRequest(Generation))
		receiveStarted(ls[0])
		finishOwned(ls[0], GenerationStage)
		var a runnerAnswer
		Eventually(done, time.Second).Should(Receive(&a))
		Expect(a.err).To(HaveOccurred())
		Expect(a.result.OutputID).To(BeEmpty())
		Expect(a.result.ABC).NotTo(BeNil())
		Expect(s.commits).To(BeZero())
		Expect(s.artifacts).To(Equal(1))
	})
	It("revokes a late prepared handle when cancel wins the serialized commit", func() {
		r, _, s, ls := newRunnerFixture(1)
		s.prepared = make(chan struct{}, 1)
		s.prepareGate = make(chan struct{})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		cancelled := make(chan struct{}, 1)
		r.Trace = func(t Transition) {
			if t.Rule == "R8" {
				cancelled <- struct{}{}
			}
		}
		done := startOwnedRunner(ctx, r, goodRunnerRequest(Generation))
		receiveStarted(ls[0])
		finishOwned(ls[0], GenerationStage)
		Eventually(s.prepared, time.Second).Should(Receive())
		cancel()
		Eventually(cancelled, time.Second).Should(Receive())
		Expect(done).NotTo(Receive())
		close(s.prepareGate)
		var a runnerAnswer
		Eventually(done, time.Second).Should(Receive(&a))
		Expect(a.err).To(HaveOccurred())
		Expect(a.result.OutputID).To(BeEmpty())
		Expect(s.commits).To(BeZero())
		Expect(s.revokes).To(Equal(1))
		Expect(s.artifacts).To(Equal(1))
	})
	It("joins cancellation racing a granted admission without starting it", func() {
		r, _, s, _ := newRunnerFixture(0)
		l := &releaseGatedLease{gate: make(chan struct{}), released: make(chan struct{})}
		b := &admissionRaceBackend{l, make(chan struct{}), make(chan struct{})}
		r.Backend = b
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := startOwnedRunner(ctx, r, goodRunnerRequest(Transcription))
		Eventually(b.entered, time.Second).Should(BeClosed())
		cancel()
		close(b.release)
		Expect(done).NotTo(Receive())
		Expect(s.cleaned).NotTo(Receive())
		close(l.gate)
		Eventually(l.released, time.Second).Should(BeClosed())
		var a runnerAnswer
		Eventually(done, time.Second).Should(Receive(&a))
		Expect(a.err).To(HaveOccurred())
		Expect(l.starts).To(BeZero())
	})
	It("consumes an unbuffered settlement before outcome without deadlock", func() {
		r, _, _, ls := newRunnerFixture(1)
		ls[0].execution.ack = make(chan error)
		done := startOwnedRunner(context.Background(), r, goodRunnerRequest(Transcription))
		receiveStarted(ls[0])
		sent := make(chan struct{})
		go func() { ls[0].execution.ack <- nil; close(sent) }()
		Eventually(sent, time.Second).Should(BeClosed())
		Expect(done).NotTo(Receive())
		abc := "X:1\nK:C\nC|\n"
		ls[0].execution.out <- StageOutcome{ABC: &abc, Valid: true}
		var a runnerAnswer
		Eventually(done, time.Second).Should(Receive(&a))
		Expect(a.err).NotTo(HaveOccurred())
	})
	It("ignores a negative settlement acknowledgement until actual release", func() {
		r, _, s, ls := newRunnerFixture(1)
		done := startOwnedRunner(context.Background(), r, goodRunnerRequest(Transcription))
		receiveStarted(ls[0])
		abc := "X:1\nK:C\nC|\n"
		ls[0].execution.out <- StageOutcome{ABC: &abc, Valid: true}
		Eventually(ls[0].execution.stopped, time.Second).Should(BeClosed())
		ls[0].execution.ack <- errors.New("transport disconnected")
		Expect(done).NotTo(Receive())
		Expect(s.cleaned).NotTo(Receive())
		ls[0].execution.ack <- nil
		var a runnerAnswer
		Eventually(done, time.Second).Should(Receive(&a))
		Expect(a.err).NotTo(HaveOccurred())
	})
	It("keeps committed success when cancellation arrives after serialized R10", func() {
		r, _, s, ls := newRunnerFixture(1)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		r.Trace = func(t Transition) {
			if t.Rule == "R10" {
				cancel()
			}
		}
		done := startOwnedRunner(ctx, r, goodRunnerRequest(Generation))
		receiveStarted(ls[0])
		finishOwned(ls[0], GenerationStage)
		var a runnerAnswer
		Eventually(done, time.Second).Should(Receive(&a))
		Expect(a.err).NotTo(HaveOccurred())
		Expect(a.result.OutputID).To(Equal("output-id"))
		Expect(s.commits).To(Equal(1))
		Expect(s.revokes).To(BeZero())
	})
	It("does not stop a later execution when an old handle is stopped again", func() {
		r, _, _, ls := newRunnerFixture(2)
		done := startOwnedRunner(context.Background(), r, goodRunnerRequest(Cover))
		receiveStarted(ls[0])
		finishOwned(ls[0], TranscriptionStage)
		receiveStarted(ls[1])
		ls[0].execution.Stop()
		Expect(ls[1].execution.stopped).NotTo(BeClosed())
		finishOwned(ls[1], GenerationStage)
		Eventually(done, time.Second).Should(Receive())
	})
	It("deep clones caller ABC and materialized params at Run entry", func() {
		r, _, _, ls := newRunnerFixture(1)
		in := goodRunnerRequest(Generation)
		abc := "X:1\nK:C\nC|\n"
		in.GenerationInput.ABC = &abc
		in.GenerationInput.Params = &pb.MusicGenerationParams{Seed: 7}
		done := startOwnedRunner(context.Background(), r, in)
		started := receiveStarted(ls[0])
		*in.GenerationInput.ABC = "changed"
		in.GenerationInput.Params.Seed = 99
		Expect(*started.Generation.ABC).To(Equal("X:1\nK:C\nC|\n"))
		Expect(started.Generation.Params.Seed).To(Equal(uint32(7)))
		finishOwned(ls[0], GenerationStage)
		Eventually(done, time.Second).Should(Receive())
	})

	It("cleans a late actual artifact once only after cancellation and settlement", func() {
		r, _, s, ls := newRunnerFixture(1)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		observed := make(chan struct{}, 1)
		r.Trace = func(t Transition) {
			if t.Rule == "R8" {
				observed <- struct{}{}
			}
		}
		done := startOwnedRunner(ctx, r, goodRunnerRequest(Generation))
		receiveStarted(ls[0])
		cancel()
		Eventually(observed, time.Second).Should(Receive())
		ls[0].execution.out <- StageOutcome{Candidate: ls[0].execution.issuer.Candidate("request", GenerationStage, "late-private"), Valid: true}
		s.mu.Lock()
		Expect(s.artifacts).To(BeZero())
		s.mu.Unlock()
		Expect(done).NotTo(Receive())
		ls[0].execution.ack <- nil
		var a runnerAnswer
		Eventually(done, time.Second).Should(Receive(&a))
		Expect(a.err).To(HaveOccurred())
		Expect(s.artifacts).To(Equal(1))
		Expect(s.commits).To(BeZero())
	})
	It("never cleans a foreign candidate on failure", func() {
		r, _, s, ls := newRunnerFixture(1)
		done := startOwnedRunner(context.Background(), r, goodRunnerRequest(Generation))
		receiveStarted(ls[0])
		ls[0].execution.out <- StageOutcome{Candidate: NewCapabilityIssuer().Candidate("foreign", GenerationStage, "foreign-private"), Valid: true}
		ls[0].execution.ack <- nil
		var a runnerAnswer
		Eventually(done, time.Second).Should(Receive(&a))
		Expect(a.err).To(HaveOccurred())
		Expect(s.artifacts).To(BeZero())
		Expect(s.commits).To(BeZero())
	})
	It("denies capacity immediately without Start or retries", func() {
		r, b, _, _ := newRunnerFixture(0)
		b.denied = true
		in := goodRunnerRequest(Transcription)
		in.Source = r.Storage.Issuer().Input(in.ID, "source")
		a, err := r.Run(context.Background(), in)
		Expect(err).To(HaveOccurred())
		Expect(a.StageError).To(Equal("capacity_429"))
		Expect(b.stages).To(HaveLen(1))
	})

	It("records the real reducer rule and guard trace, not a parallel state machine", func() {
		r, _, _, ls := newRunnerFixture(1)
		var rules, guards []string
		r.Trace = func(t Transition) { rules = append(rules, t.Rule); guards = append(guards, t.Guard) }
		done := startOwnedRunner(context.Background(), r, goodRunnerRequest(Transcription))
		receiveStarted(ls[0])
		finishOwned(ls[0], TranscriptionStage)
		Eventually(done, time.Second).Should(Receive())
		Expect(rules).To(Equal([]string{"R1", "R1", "R3", "R4", "R4"}))
		Expect(guards).To(Equal([]string{
			"authorized preflight; immediate admission only",
			"matching immediate admission granted",
			"result captured; quiescence and release still required",
			"quiescence and slot release acknowledged; clean source",
			"released stage and source cleanup acknowledged",
		}))
	})

	It("rechecks revocation after transcription release and source cleanup without retranscription", func() {
		r, b, s, ls := newRunnerFixture(1)
		denied := goodRunnerRequest(Cover).Generation
		denied.Authorized = false
		b.rechecked = &denied
		done := startOwnedRunner(context.Background(), r, goodRunnerRequest(Cover))
		receiveStarted(ls[0])
		finishOwned(ls[0], TranscriptionStage)
		var a runnerAnswer
		Eventually(done, time.Second).Should(Receive(&a))
		Expect(a.err).To(HaveOccurred())
		Expect(a.result.StageError).To(Equal("generation_preflight"))
		Expect(a.result.ABC).NotTo(BeNil())
		Expect(*a.result.ABC).To(Equal("X:1\nK:C\n C D E F |\n"))
		Expect(b.stages).To(HaveLen(1))
		Expect(s.cleaned).To(HaveLen(1))
		Expect(s.commits).To(BeZero())
	})

	It("review regression: a failed input cleanup must not finish the ownership scope", func() {
		r, _, s, ls := newRunnerFixture(1)
		s.cleanErr = errors.New("input release unresolved")
		s.cleanAck = make(chan error, 1)
		done := startOwnedRunner(context.Background(), r, goodRunnerRequest(Transcription))
		receiveStarted(ls[0])
		finishOwned(ls[0], TranscriptionStage)
		Eventually(s.cleaned, time.Second).Should(Receive())
		Expect(done).NotTo(Receive())
		s.cleanAck <- nil
		var a runnerAnswer
		Eventually(done, time.Second).Should(Receive(&a))
		Expect(a.err).To(HaveOccurred())
		Expect(s.cleaned).To(HaveLen(0))
	})
	It("review regression: admission lease plus error must not start", func() {
		r, b, _, ls := newRunnerFixture(1)
		b.admissionErr = errors.New("admission denied with retained lease")
		done := startOwnedRunner(context.Background(), r, goodRunnerRequest(Transcription))
		started := false
		var a runnerAnswer
		select {
		case <-ls[0].started:
			started = true
			finishOwned(ls[0], TranscriptionStage)
			Eventually(done, time.Second).Should(Receive(&a))
		case a = <-done:
		case <-time.After(time.Second):
			Fail("request did not release admission lease")
		}
		Expect(a.err).To(HaveOccurred())
		Expect(started).To(BeFalse(), "denied lease started inference")
	})
	It("review regression: a same-request foreign handle is not cleanup authority", func() {
		r, _, s, ls := newRunnerFixture(1)
		done := startOwnedRunner(context.Background(), r, goodRunnerRequest(Generation))
		receiveStarted(ls[0])
		ls[0].execution.out <- StageOutcome{Candidate: NewCapabilityIssuer().Candidate("request", GenerationStage, "unregistered-foreign-key"), Valid: false}
		ls[0].execution.ack <- nil
		Eventually(done, time.Second).Should(Receive())
		Expect(s.artifacts).To(BeZero(), "foreign key gained cleanup authority solely from request label")
	})

	It("retains failed artifact cleanup until definitive acknowledgement", func() {
		r, _, s, ls := newRunnerFixture(1)
		s.fetchErr = errors.New("fetch")
		s.artifactErr = errors.New("delete unresolved")
		s.artifactAck = make(chan error, 1)
		done := startOwnedRunner(context.Background(), r, goodRunnerRequest(Generation))
		receiveStarted(ls[0])
		finishOwned(ls[0], GenerationStage)
		Eventually(func() int { s.mu.Lock(); defer s.mu.Unlock(); return s.artifacts }, time.Second).Should(Equal(1))
		Expect(done).NotTo(Receive())
		s.artifactAck <- nil
		var a runnerAnswer
		Eventually(done, time.Second).Should(Receive(&a))
		Expect(a.err).To(HaveOccurred())
	})
	It("retains a denied lease on release failure until an explicit ack", func() {
		r, b, _, ls := newRunnerFixture(1)
		b.admissionErr = errors.New("denied")
		ack := make(chan error, 1)
		task := CleanupTask{errors.New("release attempt"), ack}
		ls[0].releaseTask = &task
		done := startOwnedRunner(context.Background(), r, goodRunnerRequest(Transcription))
		Eventually(b.stages, time.Second).Should(HaveLen(1))
		Expect(done).NotTo(Receive())
		Expect(ls[0].started).NotTo(Receive())
		ack <- nil
		var a runnerAnswer
		Eventually(done, time.Second).Should(Receive(&a))
		Expect(a.err).To(HaveOccurred())
	})
	It("joins Start failure with no execution through lease release acknowledgement", func() {
		r, _, _, ls := newRunnerFixture(1)
		ls[0].execution = nil
		ls[0].startErr = errors.New("start failed")
		ack := make(chan error, 1)
		task := CleanupTask{errors.New("release failed"), ack}
		ls[0].releaseTask = &task
		done := startOwnedRunner(context.Background(), r, goodRunnerRequest(Transcription))
		receiveStarted(ls[0])
		Expect(done).NotTo(Receive())
		ack <- nil
		var a runnerAnswer
		Eventually(done, time.Second).Should(Receive(&a))
		Expect(a.err).To(HaveOccurred())
	})
	It("cleans both old and replacement candidates exactly once on failure", func() {
		r, _, s, ls := newRunnerFixture(1)
		s.replacement = true
		s.fetchErr = errors.New("replacement invalid")
		done := startOwnedRunner(context.Background(), r, goodRunnerRequest(Generation))
		receiveStarted(ls[0])
		finishOwned(ls[0], GenerationStage)
		Eventually(done, time.Second).Should(Receive())
		Expect(s.artifacts).To(Equal(2))
	})
	It("keeps commit authority when cancel competes with an already entered commit", func() {
		r, _, s, ls := newRunnerFixture(1)
		s.commitEntered = make(chan struct{})
		s.committedGate = make(chan struct{})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := startOwnedRunner(ctx, r, goodRunnerRequest(Generation))
		receiveStarted(ls[0])
		finishOwned(ls[0], GenerationStage)
		Eventually(s.commitEntered, time.Second).Should(BeClosed())
		cancel()
		Expect(done).NotTo(Receive())
		close(s.committedGate)
		var a runnerAnswer
		Eventually(done, time.Second).Should(Receive(&a))
		Expect(a.err).NotTo(HaveOccurred())
		Expect(s.revokes).To(BeZero())
	})
	It("rejects a forged source minted by another issuer with identical request and key", func() {
		r, b, _, _ := newRunnerFixture(0)
		in := goodRunnerRequest(Transcription)
		in.Source = NewCapabilityIssuer().Input("request", "source")
		_, err := r.Run(context.Background(), in)
		Expect(err).To(MatchError(ErrPreflight))
		Expect(b.stages).To(HaveLen(0))
	})

	It("cleans the superseded candidate on success without deleting the published candidate", func() {
		r, _, s, ls := newRunnerFixture(1)
		s.replacement = true
		done := startOwnedRunner(context.Background(), r, goodRunnerRequest(Generation))
		receiveStarted(ls[0])
		finishOwned(ls[0], GenerationStage)
		var a runnerAnswer
		Eventually(done, time.Second).Should(Receive(&a))
		Expect(a.err).NotTo(HaveOccurred())
		Expect(s.artifacts).To(Equal(1))
		Expect(s.commits).To(Equal(1))
	})

	It("retirement regression: acknowledged input token cannot be replayed", func() {
		r, _, s, ls := newRunnerFixture(1)
		in := goodRunnerRequest(Transcription)
		in.Source = s.issuer.Input(in.ID, "source")
		done := startOwnedRunner(context.Background(), r, in)
		receiveStarted(ls[0])
		finishOwned(ls[0], TranscriptionStage)
		Eventually(done, time.Second).Should(Receive())
		_, stillValid := s.issuer.InputKey(in.Source)
		Expect(stillValid).To(BeFalse(), "released token retains authority and issuer record")
	})
	It("retirement regression: an owned transcription candidate is not dropped", func() {
		r, _, s, ls := newRunnerFixture(1)
		done := startOwnedRunner(context.Background(), r, goodRunnerRequest(Transcription))
		receiveStarted(ls[0])
		abc := "X:1\nK:C\nC|\n"
		h := s.issuer.Candidate("request", TranscriptionStage, "transcription-owned")
		ls[0].execution.out <- StageOutcome{ABC: &abc, Candidate: h, Valid: true}
		ls[0].execution.ack <- nil
		Eventually(done, time.Second).Should(Receive())
		Expect(s.artifacts).To(Equal(1), "accepted transcription capability vanished from cleanup ledger")
	})

	It("retires exact tokens without accumulating issuer records across completed requests", func() {
		r, b, s, ls := newRunnerFixture(2)
		for _, l := range ls {
			b.leases = []*ownedTestLease{l}
			done := startOwnedRunner(context.Background(), r, goodRunnerRequest(Generation))
			receiveStarted(l)
			finishOwned(l, GenerationStage)
			Eventually(done, time.Second).Should(Receive())
			s.issuer.mu.Lock()
			Expect(s.issuer.records).To(HaveLen(0))
			Expect(s.issuer.claims).To(HaveLen(0))
			s.issuer.mu.Unlock()
		}
	})
	It("denies concurrent replay without cleaning or retiring the live owner's token", func() {
		r, _, s, ls := newRunnerFixture(1)
		in := goodRunnerRequest(Transcription)
		in.Source = s.issuer.Input(in.ID, "source")
		done := startOwnedRunner(context.Background(), r, in)
		receiveStarted(ls[0])
		_, err := r.Run(context.Background(), in)
		Expect(err).To(MatchError(ErrPreflight))
		Expect(s.cleaned).NotTo(Receive())
		_, valid := s.issuer.InputKey(in.Source)
		Expect(valid).To(BeTrue())
		finishOwned(ls[0], TranscriptionStage)
		Eventually(done, time.Second).Should(Receive())
		_, err = r.Run(context.Background(), in)
		Expect(err).To(MatchError(ErrPreflight))
	})
	It("leaves unconsumed input valid when preflight is rejected", func() {
		r, _, s, _ := newRunnerFixture(0)
		in := goodRunnerRequest(Transcription)
		in.Source = s.issuer.Input(in.ID, "borrowed")
		in.Quota = false
		_, err := r.Run(context.Background(), in)
		Expect(err).To(MatchError(ErrPreflight))
		_, valid := s.issuer.InputKey(in.Source)
		Expect(valid).To(BeTrue())
		Expect(s.cleaned).NotTo(Receive())
	})
	It("keeps the exact transcription token live until cleanup ack then retires it", func() {
		r, _, s, ls := newRunnerFixture(1)
		s.artifactAck = make(chan error, 1)
		s.artifactErr = errors.New("attempt failed")
		done := startOwnedRunner(context.Background(), r, goodRunnerRequest(Transcription))
		receiveStarted(ls[0])
		h := s.issuer.Candidate("request", TranscriptionStage, "private-transcription")
		abc := "X:1\nK:C\nC|\n"
		ls[0].execution.out <- StageOutcome{ABC: &abc, Candidate: h, Valid: true}
		ls[0].execution.ack <- nil
		Eventually(func() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.cleanedHandles) }, time.Second).Should(Equal(1))
		s.mu.Lock()
		Expect(s.cleanedHandles).To(Equal([]Candidate{h}))
		s.mu.Unlock()
		_, valid := s.issuer.CandidateKey(h)
		Expect(valid).To(BeTrue())
		Expect(done).NotTo(Receive())
		s.artifactAck <- nil
		Eventually(done, time.Second).Should(Receive())
		_, valid = s.issuer.CandidateKey(h)
		Expect(valid).To(BeFalse())
	})

	for _, prepareFirst := range []bool{false, true} {
		prepareFirst := prepareFirst
		It("retains revoke acknowledgement barrier with late Prepare order "+fmt.Sprint(prepareFirst), func() {
			r, _, s, ls := newRunnerFixture(1)
			s.prepareGate = make(chan struct{})
			s.prepared = make(chan struct{}, 1)
			s.revokeAck = make(chan error, 1)
			s.revokeEntered = make(chan struct{})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := startOwnedRunner(ctx, r, goodRunnerRequest(Generation))
			receiveStarted(ls[0])
			finishOwned(ls[0], GenerationStage)
			Eventually(s.prepared, time.Second).Should(Receive())
			cancel()
			Eventually(s.revokeEntered, time.Second).Should(BeClosed())
			if prepareFirst {
				close(s.prepareGate)
				Expect(done).NotTo(Receive())
				s.revokeAck <- nil
			} else {
				s.revokeAck <- nil
				Expect(done).NotTo(Receive())
				close(s.prepareGate)
			}
			var a runnerAnswer
			Eventually(done, time.Second).Should(Receive(&a))
			Expect(a.err).To(HaveOccurred())
			Expect(s.commits).To(BeZero())
			Expect(s.tombstone).To(BeTrue())
			Expect(s.issuer.records).To(HaveLen(0))
		})
	}

})
