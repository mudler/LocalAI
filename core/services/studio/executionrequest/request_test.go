// SPDX-License-Identifier: MIT
package executionrequest_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	shell "github.com/mudler/LocalAI/core/services/studio/executionrequest"
	model "github.com/mudler/LocalAI/pkg/addons/model/executionstatus"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestRequest(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Request-local execution observation")
}

type timer struct {
	fn      func()
	stopped bool
}

func (t *timer) Stop() bool { old := t.stopped; t.stopped = true; return !old }

type clock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*timer
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *clock) AfterFunc(_ time.Duration, fn func()) shell.Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &timer{fn: fn}
	c.timers = append(c.timers, t)
	return t
}
func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	ts := append([]*timer(nil), c.timers...)
	c.mu.Unlock()
	for _, t := range ts {
		t.fn()
	}
}

// deadlineConsumer delivers an expiry at the validation boundary without a wall timer.
type deadlineConsumer struct {
	context.Context
	deadline time.Time
	done     chan struct{}
}

func (c *deadlineConsumer) Deadline() (time.Time, bool) { return c.deadline, true }
func (c *deadlineConsumer) Done() <-chan struct{}       { return c.done }
func (c *deadlineConsumer) Err() error {
	select {
	case <-c.done:
		return context.DeadlineExceeded
	default:
		return nil
	}
}

var _ = Describe("Reducer shell", func() {
	var b model.Binding
	var c *clock
	var io shell.IO
	BeforeEach(func() {
		b = model.Binding{Actor: "actor/credential", Addon: "renamed", Generation: "1", Incarnation: "first", ExecutionTool: "run", StatusTool: "health"}
		c = &clock{now: time.Now()}
		io.Validate = func(context.Context, model.Binding) error { return nil }
		io.Read = func(context.Context, model.Binding, model.ObservationID, time.Time) (model.Payload, error) {
			return model.ValidPayload(model.Value{Readiness: model.Available, Isolation: model.Isolated}), nil
		}
	})
	It("coalesces concurrent prompt/status/egress consumers into one exact read", func() {
		entered := make(chan struct{})
		release := make(chan struct{})
		calls := 0
		var mu sync.Mutex
		io.Read = func(ctx context.Context, got model.Binding, q model.ObservationID, deadline time.Time) (model.Payload, error) {
			mu.Lock()
			calls++
			mu.Unlock()
			if got != b || q != "q" || !deadline.Equal(c.Now().Add(5*time.Second)) {
				return model.InvalidPayload(), errors.New("wrong binding")
			}
			close(entered)
			<-release
			return model.ValidPayload(model.Value{Readiness: model.Available, Isolation: model.Isolated}), nil
		}
		r := shell.New(context.Background(), b, "q", model.Eligible, io, c)
		defer r.Close()
		results := make(chan model.Value, 3)
		for range 3 {
			go func() { results <- r.Observe(context.Background()) }()
		}
		<-entered
		close(release)
		for range 3 {
			Expect(<-results).To(Equal(model.Value{Readiness: model.Available, Isolation: model.Isolated}))
		}
		mu.Lock()
		Expect(calls).To(Equal(1))
		mu.Unlock()
		Expect(r.Observe(context.Background()).Isolation).To(Equal(model.Isolated))
		reads := 0
		for _, tr := range r.Trace() {
			for _, e := range tr.Effects {
				if _, ok := e.(model.Read); ok {
					reads++
				}
			}
		}
		Expect(reads).To(Equal(1))
	})
	for _, kind := range []string{"failure", "malformed"} {
		It("never retries settled "+kind, func() {
			calls := 0
			io.Read = func(context.Context, model.Binding, model.ObservationID, time.Time) (model.Payload, error) {
				calls++
				if kind == "failure" {
					return model.InvalidPayload(), errors.New("offline")
				}
				return model.InvalidPayload(), nil
			}
			r := shell.New(context.Background(), b, "q", model.Eligible, io, c)
			defer r.Close()
			Expect(r.Observe(context.Background())).To(Equal(model.Unknown()))
			Expect(r.Observe(context.Background())).To(Equal(model.Unknown()))
			Expect(calls).To(Equal(1))
			Expect(r.Trace()[2].Rule).To(Equal(model.R4))
		})
	}
	It("settles missing contracts without dispatch", func() {
		io.Read = func(context.Context, model.Binding, model.ObservationID, time.Time) (model.Payload, error) {
			Fail("unexpected dispatch")
			return model.InvalidPayload(), nil
		}
		r := shell.New(context.Background(), b, "q", "no-contract", io, c)
		defer r.Close()
		Expect(r.Observe(context.Background())).To(Equal(model.Unknown()))
	})
	It("times out at five seconds, cancels the read and ignores late success", func() {
		entered := make(chan struct{})
		canceled := make(chan struct{})
		release := make(chan struct{})
		returned := make(chan struct{})
		io.Read = func(ctx context.Context, _ model.Binding, _ model.ObservationID, _ time.Time) (model.Payload, error) {
			close(entered)
			<-ctx.Done()
			close(canceled)
			<-release
			defer close(returned)
			return model.ValidPayload(model.Value{Readiness: model.Available, Isolation: model.Isolated}), nil
		}
		r := shell.New(context.Background(), b, "q", model.Eligible, io, c)
		defer r.Close()
		out := make(chan model.Value, 1)
		go func() { out <- r.Observe(context.Background()) }()
		<-entered
		c.advance(5 * time.Second)
		Expect(<-out).To(Equal(model.Unknown()))
		<-canceled
		close(release)
		<-returned
		Expect(r.Observe(context.Background())).To(Equal(model.Unknown()))
	})
	It("uses the earlier parent deadline", func() {
		deadline := c.Now().Add(time.Second)
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		defer cancel()
		io.Read = func(_ context.Context, _ model.Binding, _ model.ObservationID, d time.Time) (model.Payload, error) {
			if !d.Equal(deadline) {
				return model.InvalidPayload(), errors.New("wrong deadline")
			}
			return model.ValidPayload(model.Value{Readiness: model.Available, Isolation: model.Host}), nil
		}
		r := shell.New(ctx, b, "q", model.Eligible, io, c)
		defer r.Close()
		Expect(r.Observe(context.Background()).Isolation).To(Equal(model.Host))
	})
	for _, reason := range []string{"close", "parent-cancel", "disable", "same-generation-incarnation", "current-selection"} {
		It("withdraws pending reads on "+reason, func() {
			entered := make(chan struct{})
			canceled := make(chan struct{})
			release := make(chan struct{})
			io.Read = func(ctx context.Context, _ model.Binding, _ model.ObservationID, _ time.Time) (model.Payload, error) {
				close(entered)
				<-ctx.Done()
				close(canceled)
				<-release
				return model.ValidPayload(model.Value{Readiness: model.Available, Isolation: model.Isolated}), nil
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r := shell.New(ctx, b, "q", model.Eligible, io, c)
			defer r.Close()
			out := make(chan model.Value, 1)
			go func() { out <- r.Observe(context.Background()) }()
			<-entered
			switch reason {
			case "close":
				r.Close()
			case "parent-cancel":
				cancel()
			default:
				r.Invalidate(model.Cause(reason))
			}
			Expect(<-out).To(Equal(model.Unknown()))
			<-canceled
			close(release)
			Expect(r.Observe(context.Background())).To(Equal(model.Unknown()))
		})
	}
	It("checks freshness after the carrier returns and on every later consumption", func() {
		var mu sync.Mutex
		valid := true
		io.Validate = func(context.Context, model.Binding) error {
			mu.Lock()
			defer mu.Unlock()
			if !valid {
				return errors.New("replaced")
			}
			return nil
		}
		r := shell.New(context.Background(), b, "q", model.Eligible, io, c)
		defer r.Close()
		Expect(r.Observe(context.Background()).Isolation).To(Equal(model.Isolated))
		mu.Lock()
		valid = false
		mu.Unlock()
		Expect(r.Observe(context.Background())).To(Equal(model.Unknown()))
	})
	It("rejects a stale successful carrier reply before publishing", func() {
		io.Validate = func(context.Context, model.Binding) error { return errors.New("incarnation replaced") }
		r := shell.New(context.Background(), b, "q", model.Eligible, io, c)
		defer r.Close()
		Expect(r.Observe(context.Background())).To(Equal(model.Unknown()))
		for _, tr := range r.Trace() {
			Expect(tr.Rule).NotTo(Equal(model.R3))
		}
	})
	It("keeps independent actors and requests separate", func() {
		r := shell.New(context.Background(), b, "q", model.Eligible, io, c)
		defer r.Close()
		other := b
		other.Actor = "other"
		s := shell.New(context.Background(), other, "other-q", model.Eligible, io, c)
		defer s.Close()
		r.Invalidate("permission")
		Expect(r.Observe(context.Background())).To(Equal(model.Unknown()))
		Expect(s.Observe(context.Background()).Isolation).To(Equal(model.Isolated))
	})
	It("does not return a positive result to a consumer canceled during freshness validation", func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		validations := 0
		io.Validate = func(context.Context, model.Binding) error {
			validations++
			if validations == 2 {
				cancel()
			}
			return nil
		}
		r := shell.New(context.Background(), b, "q", model.Eligible, io, c)
		defer r.Close()
		Expect(r.Observe(ctx)).To(Equal(model.Unknown()))
	})

	for _, kind := range []string{"canceled", "deadline-exceeded"} {
		It("keeps consumer validation "+kind+" local to the consumer", func() {
			var consumer context.Context
			var expire func()
			wantErr := context.Canceled
			if kind == "canceled" {
				consumer, expire = context.WithCancel(context.Background())
				defer expire()
			} else {
				ctx := &deadlineConsumer{Context: context.Background(), deadline: c.Now().Add(time.Second), done: make(chan struct{})}
				consumer = ctx
				expire = func() { close(ctx.done) }
				wantErr = context.DeadlineExceeded
			}
			reads := 0
			io.Read = func(context.Context, model.Binding, model.ObservationID, time.Time) (model.Payload, error) {
				reads++
				return model.ValidPayload(model.Value{Readiness: model.Available, Isolation: model.Isolated}), nil
			}
			io.Validate = func(ctx context.Context, _ model.Binding) error {
				if ctx == consumer {
					expire()
					return ctx.Err()
				}
				return nil
			}
			r := shell.New(context.Background(), b, "q", model.Eligible, io, c)
			defer r.Close()
			Expect(r.Observe(context.Background()).Isolation).To(Equal(model.Isolated))
			Expect(r.Observe(consumer)).To(Equal(model.Unknown()))
			Expect(consumer.Err()).To(Equal(wantErr))
			Expect(r.Observe(context.Background()).Isolation).To(Equal(model.Isolated))
			Expect(reads).To(Equal(1))
			rules := []model.Rule{}
			readEffects := 0
			for _, tr := range r.Trace() {
				rules = append(rules, tr.Rule)
				for _, effect := range tr.Effects {
					if _, ok := effect.(model.Read); ok {
						readEffects++
					}
				}
			}
			Expect(rules).To(Equal([]model.Rule{model.R1, model.R2, model.R3, model.R7, model.R7}))
			Expect(readEffects).To(Equal(1))
		})
	}

	It("matches the normative success/duplicate-probe/close rule and effect trace", func() {
		r := shell.New(context.Background(), b, "q", model.Eligible, io, c)
		Expect(r.Observe(context.Background()).Isolation).To(Equal(model.Isolated))
		Expect(r.Observe(context.Background()).Isolation).To(Equal(model.Isolated))
		r.Close()
		trace := r.Trace()
		rules := []model.Rule{}
		for _, tr := range trace {
			rules = append(rules, tr.Rule)
		}
		Expect(rules).To(Equal([]model.Rule{model.R1, model.R2, model.R3, model.R7, model.R10}))
		Expect(trace[0].Effects).To(Equal([]model.Effect{model.Publish{Value: model.Unknown()}}))
		Expect(trace[1].Effects).To(Equal([]model.Effect{model.Read{B: b, Q: "q", Deadline: int64(5 * time.Second)}, model.Publish{Value: model.Unknown()}}))
		Expect(trace[2].Effects).To(Equal([]model.Effect{model.Publish{Value: model.Value{Readiness: model.Available, Isolation: model.Isolated}}}))
		Expect(trace[3].Effects).To(BeEmpty())
		Expect(trace[4].Effects).To(Equal([]model.Effect{model.Publish{Value: model.Unknown()}}))
		trace[1].Effects[0] = model.Cancel{Q: "tampered"}
		Expect(r.Trace()[1].Effects[0]).To(BeAssignableToTypeOf(model.Read{}))
	})
	It("expires before the first probe without dispatch", func() {
		calls := 0
		io.Read = func(context.Context, model.Binding, model.ObservationID, time.Time) (model.Payload, error) {
			calls++
			return model.InvalidPayload(), nil
		}
		r := shell.New(context.Background(), b, "q", model.Eligible, io, c)
		defer r.Close()
		c.advance(5 * time.Second)
		Expect(r.Observe(context.Background())).To(Equal(model.Unknown()))
		Expect(calls).To(BeZero())
		Expect(r.Trace()[1].Rule).To(Equal(model.R9))
	})
	It("fails closed when the freshness callback is absent", func() {
		io.Validate = nil
		r := shell.New(context.Background(), b, "q", model.Eligible, io, c)
		defer r.Close()
		Expect(r.Observe(context.Background())).To(Equal(model.Unknown()))
	})

	It("normalizes unavailable readiness and keeps Closed absorbing", func() {
		io.Read = func(context.Context, model.Binding, model.ObservationID, time.Time) (model.Payload, error) {
			return model.ValidPayload(model.Value{Readiness: model.Unavailable, Isolation: model.Isolated}), nil
		}
		r := shell.New(context.Background(), b, "q", model.Eligible, io, c)
		Expect(r.Observe(context.Background())).To(Equal(model.Value{Readiness: model.Unavailable, Isolation: model.UnknownIsolation}))
		r.Close()
		r.Invalidate("late")
		Expect(r.Observe(context.Background())).To(Equal(model.Unknown()))
		Expect(r.Trace()[len(r.Trace())-1].State).To(Equal(model.Closed{}))
	})
})
