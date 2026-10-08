package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/testutil"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

// fakePicker hands out a scripted sequence of workers.
type fakePicker struct {
	mu    sync.Mutex
	nodes []string
	types map[string]string
	err   error
	asked [][]string
}

func (p *fakePicker) PickConnectedExcluding(_ context.Context, tried map[string]bool) (string, string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var skipped []string
	for id := range tried {
		skipped = append(skipped, id)
	}
	p.asked = append(p.asked, skipped)
	if p.err != nil {
		return "", "", p.err
	}
	for _, id := range p.nodes {
		if !tried[id] {
			return id, p.types[id], nil
		}
	}
	return "", "", errNoWorkerLeft
}

var errNoWorkerLeft = errors.New("no worker left")

type call struct {
	node, verb string
	payload    string
}

// fakeControl plays the control client. do decides what each call does.
type fakeControl struct {
	mu    sync.Mutex
	calls []call
	do    func(ctx context.Context, node, verb string, onProgress func(string, json.RawMessage)) (workerctl.RunReply, error)
}

func (f *fakeControl) CallStreaming(ctx context.Context, node, verb string, req, reply any, onProgress func(string, json.RawMessage)) error {
	raw, _ := req.(json.RawMessage)
	f.mu.Lock()
	f.calls = append(f.calls, call{node, verb, string(raw)})
	do := f.do
	f.mu.Unlock()
	r, err := do(ctx, node, verb, onProgress)
	if err != nil {
		return err
	}
	*(reply.(*workerctl.RunReply)) = r
	return nil
}

func (f *fakeControl) seen() []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]call(nil), f.calls...)
}

type progressLine struct{ nodeType, subject, raw string }

type fakeBroadcast struct {
	mu    sync.Mutex
	lines []progressLine
}

func (b *fakeBroadcast) Handle(nodeType, subject string, raw json.RawMessage) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lines = append(b.lines, progressLine{nodeType, subject, string(raw)})
	return true
}

type terminal struct{ jobID, status, result, errMsg string }

type fakeStore struct {
	mu   sync.Mutex
	got  []terminal
	fail error
}

func (s *fakeStore) UpdateJobStatus(jobID, status, result, errMsg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail != nil {
		return s.fail
	}
	s.got = append(s.got, terminal{jobID, status, result, errMsg})
	return nil
}

func (s *fakeStore) terminals() []terminal {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]terminal(nil), s.got...)
}

var _ = Describe("The agent driver", func() {
	var (
		ctx       context.Context
		picker    *fakePicker
		control   *fakeControl
		broadcast *fakeBroadcast
		store     *fakeStore
		bus       *testutil.FakeBus
		driver    *AgentDriver
	)

	ciPayload := func(jobID string) []byte {
		raw, err := json.Marshal(JobEvent{JobID: jobID, TaskID: "t1"})
		Expect(err).ToNot(HaveOccurred())
		return raw
	}

	BeforeEach(func() {
		c, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		ctx = c
		picker = &fakePicker{nodes: []string{"w1", "w2", "w3", "w4"}, types: map[string]string{"w1": "agent", "w2": "agent", "w3": "agent", "w4": "agent"}}
		control = &fakeControl{do: func(context.Context, string, string, func(string, json.RawMessage)) (workerctl.RunReply, error) {
			return workerctl.RunReply{}, nil
		}}
		broadcast = &fakeBroadcast{}
		store = &fakeStore{}
		bus = testutil.NewFakeBus()
		var err error
		driver, err = NewAgentDriver(AgentDriverConfig{Picker: picker, Control: control, Broadcast: broadcast, Store: store, Hints: bus})
		Expect(err).ToNot(HaveOccurred())
		Expect(driver.Start(ctx)).To(Succeed())
		DeferCleanup(driver.Stop)
	})

	handler := func(kind messaging.WorkKind) messaging.WorkHandler {
		GinkgoHelper()
		h, err := driver.Handler(kind)
		Expect(err).ToNot(HaveOccurred())
		return h
	}

	Describe("a run", func() {
		It("sends the payload to the verb of its kind on the worker the picker chose", func() {
			Expect(handler(messaging.WorkAgentRun)(ctx, []byte(`{"agent_name":"a"}`), nil)).To(Succeed())
			Expect(handler(messaging.WorkMCPCI)(ctx, ciPayload("j1"), nil)).To(Succeed())
			calls := control.seen()
			Expect(calls).To(HaveLen(2))
			Expect(calls[0]).To(Equal(call{"w1", workerctl.VerbAgentExecute, `{"agent_name":"a"}`}))
			Expect(calls[1].verb).To(Equal(workerctl.VerbMCPCIRun))
			Expect(calls[1].payload).To(MatchJSON(string(ciPayload("j1"))))
		})

		It("publishes the lines that name a subject, with the node type of the worker, and drops the others", func() {
			control.do = func(_ context.Context, _, _ string, onProgress func(string, json.RawMessage)) (workerctl.RunReply, error) {
				onProgress("agent.a.events.u", json.RawMessage(`{"n":1}`))
				onProgress("", json.RawMessage(`{"private":"tick"}`))
				onProgress("jobs.j1.progress", json.RawMessage(`{"n":2}`))
				return workerctl.RunReply{}, nil
			}
			Expect(handler(messaging.WorkAgentRun)(ctx, []byte(`{}`), nil)).To(Succeed())
			Expect(broadcast.lines).To(Equal([]progressLine{
				{"agent", "agent.a.events.u", `{"n":1}`},
				{"agent", "jobs.j1.progress", `{"n":2}`},
			}))
		})

		It("writes the terminal state of a job before it lets the claim be completed", func() {
			control.do = func(context.Context, string, string, func(string, json.RawMessage)) (workerctl.RunReply, error) {
				return workerctl.RunReply{JobID: "j1", Status: "completed", Result: "42"}, nil
			}
			Expect(handler(messaging.WorkMCPCI)(ctx, ciPayload("j1"), nil)).To(Succeed())
			Expect(store.terminals()).To(Equal([]terminal{{"j1", "completed", "42", ""}}))
		})

		It("closes a job whose reply names no status as failed, because the one outcome to avoid is a job left running", func() {
			control.do = func(context.Context, string, string, func(string, json.RawMessage)) (workerctl.RunReply, error) {
				return workerctl.RunReply{JobID: "j1"}, nil
			}
			Expect(handler(messaging.WorkMCPCI)(ctx, ciPayload("j1"), nil)).To(Succeed())
			got := store.terminals()
			Expect(got).To(HaveLen(1))
			Expect(got[0].status).To(Equal("failed"))
			Expect(got[0].errMsg).ToNot(BeEmpty())
		})

		It("writes no job for a run that names none", func() {
			Expect(handler(messaging.WorkAgentRun)(ctx, []byte(`{}`), nil)).To(Succeed())
			Expect(store.terminals()).To(BeEmpty())
		})

		It("keeps the claim, and does not release it, when the terminal state cannot be written", func() {
			store.fail = errors.New("database is away")
			control.do = func(context.Context, string, string, func(string, json.RawMessage)) (workerctl.RunReply, error) {
				return workerctl.RunReply{JobID: "j1", Status: "completed"}, nil
			}
			err := handler(messaging.WorkMCPCI)(ctx, ciPayload("j1"), nil)
			Expect(errors.Is(err, ErrKeepClaim)).To(BeTrue(), "the work ran, so releasing would run it twice; got %v", err)
		})

		It("returns the failure of a call that obtained no answer, so that the claim is released", func() {
			control.do = func(context.Context, string, string, func(string, json.RawMessage)) (workerctl.RunReply, error) {
				return workerctl.RunReply{}, fmt.Errorf("the stream ended before its reply line: %w", errors.New("unexpected EOF"))
			}
			err := handler(messaging.WorkMCPCI)(ctx, ciPayload("j1"), nil)
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, ErrKeepClaim)).To(BeFalse())
			Expect(store.terminals()).To(BeEmpty(), "a broken link is not a verdict about the job")
		})

		It("returns the error of the picker when no worker is connected", func() {
			picker.err = errors.New("no agent worker holds a tunnel")
			err := handler(messaging.WorkAgentRun)(ctx, []byte(`{}`), nil)
			Expect(err).To(MatchError(ContainSubstring("no agent worker")))
			Expect(control.seen()).To(BeEmpty())
		})

		It("offers the run to the next worker when the first has no free slot, up to three", func() {
			control.do = func(_ context.Context, node, _ string, _ func(string, json.RawMessage)) (workerctl.RunReply, error) {
				if node == "w3" {
					return workerctl.RunReply{}, nil
				}
				return workerctl.RunReply{}, fmt.Errorf("control request: %w", workerctl.ErrWorkerBusy)
			}
			Expect(handler(messaging.WorkAgentRun)(ctx, []byte(`{}`), nil)).To(Succeed())
			Expect(control.seen()).To(HaveLen(3))

			control.mu.Lock()
			control.calls = nil
			control.do = func(context.Context, string, string, func(string, json.RawMessage)) (workerctl.RunReply, error) {
				return workerctl.RunReply{}, workerctl.ErrWorkerBusy
			}
			control.mu.Unlock()
			err := handler(messaging.WorkAgentRun)(ctx, []byte(`{}`), nil)
			Expect(errors.Is(err, workerctl.ErrWorkerBusy)).To(BeTrue())
			Expect(control.seen()).To(HaveLen(3), "a fourth worker is not tried")
		})

		It("does not offer a run to a second worker after a failure that may have started it", func() {
			control.do = func(context.Context, string, string, func(string, json.RawMessage)) (workerctl.RunReply, error) {
				return workerctl.RunReply{}, errors.New("the stream ended before its reply line")
			}
			Expect(handler(messaging.WorkAgentRun)(ctx, []byte(`{}`), nil)).ToNot(Succeed())
			Expect(control.seen()).To(HaveLen(1))
		})

		It("leaves a plain task alone, as the NATS carrier does: nothing serves it and the job stays as it is", func() {
			Expect(handler(messaging.WorkTask)(ctx, ciPayload("j1"), nil)).To(Succeed())
			Expect(control.seen()).To(BeEmpty())
			Expect(store.terminals()).To(BeEmpty())
		})

		It("refuses a kind it does not know", func() {
			_, err := driver.Handler(messaging.WorkKind("nonsense"))
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("the cancel of a job", func() {
		running := func() (started chan struct{}, finished chan error) {
			started = make(chan struct{})
			finished = make(chan error, 1)
			control.do = func(ctx context.Context, _, _ string, _ func(string, json.RawMessage)) (workerctl.RunReply, error) {
				close(started)
				<-ctx.Done()
				return workerctl.RunReply{}, ctx.Err()
			}
			go func() { finished <- handler(messaging.WorkMCPCI)(ctx, ciPayload("j1"), nil) }()
			return started, finished
		}

		It("ends the stream of the job, so the run on the worker dies with its request, and records the job as cancelled", func() {
			started, finished := running()
			Eventually(started, 5*time.Second).Should(BeClosed())

			Expect(bus.Publish(messaging.SubjectJobCancel("j1"), CancelEvent{JobID: "j1"})).To(Succeed())

			var err error
			Eventually(finished, 5*time.Second).Should(Receive(&err))
			Expect(err).ToNot(HaveOccurred(), "the cancel is an answer, so the claim is completed and not released")
			Expect(store.terminals()).To(HaveLen(1))
			Expect(store.terminals()[0].jobID).To(Equal("j1"))
			Expect(store.terminals()[0].status).To(Equal("cancelled"))
		})

		It("ignores the cancel of a job that runs elsewhere or nowhere", func() {
			started, finished := running()
			Eventually(started, 5*time.Second).Should(BeClosed())
			Expect(bus.Publish(messaging.SubjectJobCancel("other"), CancelEvent{JobID: "other"})).To(Succeed())
			Consistently(finished, 300*time.Millisecond).ShouldNot(Receive())
			Expect(store.terminals()).To(BeEmpty())
			// Let the run go: the context of the spec ends it.
		})

		It("treats the stop of the replica as a failure to answer and not as a cancel", func() {
			started := make(chan struct{})
			control.do = func(ctx context.Context, _, _ string, _ func(string, json.RawMessage)) (workerctl.RunReply, error) {
				close(started)
				<-ctx.Done()
				return workerctl.RunReply{}, ctx.Err()
			}
			parent, stop := context.WithCancel(ctx)
			finished := make(chan error, 1)
			go func() { finished <- handler(messaging.WorkMCPCI)(parent, ciPayload("j1"), nil) }()
			Eventually(started, 5*time.Second).Should(BeClosed())
			stop()
			var err error
			Eventually(finished, 5*time.Second).Should(Receive(&err))
			Expect(err).To(HaveOccurred())
			Expect(store.terminals()).To(BeEmpty())
		})

		It("stops hearing cancels after Stop", func() {
			started, finished := running()
			Eventually(started, 5*time.Second).Should(BeClosed())
			driver.Stop()
			Expect(bus.Publish(messaging.SubjectJobCancel("j1"), CancelEvent{JobID: "j1"})).To(Succeed())
			Consistently(finished, 300*time.Millisecond).ShouldNot(Receive())
		})

		It("does not leave a cancel function behind for a run that ended", func() {
			Expect(handler(messaging.WorkMCPCI)(ctx, ciPayload("j1"), nil)).To(Succeed())
			Expect(driver.cancels.Cancel("j1")).To(BeFalse())
		})
	})

	It("is built only with what it needs", func() {
		good := AgentDriverConfig{Picker: picker, Control: control, Broadcast: broadcast, Store: store, Hints: bus}
		for name, mutate := range map[string]func(*AgentDriverConfig){
			"no picker":  func(c *AgentDriverConfig) { c.Picker = nil },
			"no control": func(c *AgentDriverConfig) { c.Control = nil },
		} {
			cfg := good
			mutate(&cfg)
			_, err := NewAgentDriver(cfg)
			Expect(err).To(HaveOccurred(), name)
		}
		_, err := NewAgentDriver(AgentDriverConfig{Picker: picker, Control: control})
		Expect(err).ToNot(HaveOccurred(), "a driver with no store and no bus still drives; it records and cancels nothing")
	})
})
