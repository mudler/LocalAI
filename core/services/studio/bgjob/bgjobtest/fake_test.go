package bgjobtest_test

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/bgjob"
	"github.com/mudler/LocalAI/core/services/studio/bgjob/bgjobtest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the fake observer", func() {
	var (
		f   *bgjobtest.Fake
		now time.Time
		id  string
		ctx = context.Background()
	)
	obs := func(since int64) bgjob.Observation {
		GinkgoHelper()
		o, err := f.Observe(ctx, bgjob.ObserveRequest{Owner: "alice", Chat: "c1", ID: id, Since: since})
		Expect(err).NotTo(HaveOccurred())
		return o
	}
	BeforeEach(func() {
		f = bgjobtest.New()
		now = time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
		f.Now = func() time.Time { return now }
		id = f.Start(bgjob.Info{Owner: "alice", Chat: "c1", Label: "build", Provider: "builtin", Instance: "n1"})
	})

	It("reads output from an absolute offset and gives the next one", func() {
		f.Write(id, "hello ")
		f.Write(id, "world")
		o := obs(0)
		Expect(string(o.Output)).To(Equal("hello world"))
		Expect(o.Next).To(Equal(int64(11)))
		Expect(string(obs(6).Output)).To(Equal("world"))
		Expect(obs(o.Next).Output).To(BeEmpty())
		Expect(o.Tail).To(Equal("hello world"))
		Expect(o.State).To(Equal(bgjob.StateRunning))
		Expect(o.Total).To(Equal(int64(11)))
	})

	It("reports idle time by its own clock, and none once finished", func() {
		f.Write(id, "x")
		now = now.Add(10 * time.Minute)
		Expect(obs(0).Idle).To(Equal(10 * time.Minute))
		f.Write(id, "y")
		Expect(obs(0).Idle).To(BeZero())
		now = now.Add(time.Minute)
		f.Exit(id, 3)
		o := obs(0)
		Expect(o.Idle).To(BeZero())
		Expect(o.State).To(Equal(bgjob.StateExited))
		Expect(o.State.Finished()).To(BeTrue())
		Expect(o.Exit).To(Equal(3))
		Expect(o.End).To(Equal(bgjob.EndExit))
		Expect(o.Ended).To(Equal(now))
	})

	It("ignores output after the end and keeps the first ending", func() {
		f.Finish(id, bgjob.StateKilled, bgjob.EndAgentEnded, 143)
		f.Write(id, "late")
		f.Exit(id, 0)
		o := obs(0)
		Expect(o.Output).To(BeEmpty())
		Expect(o.End).To(Equal(bgjob.EndAgentEnded))
		Expect(o.Exit).To(Equal(143))
	})

	It("moves a lost offset to the oldest byte kept", func() {
		f.Keep = 4
		f.Write(id, "abcdefgh")
		o := obs(0)
		Expect(o.From).To(Equal(int64(4)))
		Expect(string(o.Output)).To(Equal("efgh"))
		Expect(o.Next).To(Equal(int64(8)))
		Expect(o.Total).To(Equal(int64(8)))
	})

	It("never ends a page inside a character", func() {
		f.Write(id, "aéb") // 4 bytes: a, two for the accent, b
		o, err := f.Observe(ctx, bgjob.ObserveRequest{Owner: "alice", Chat: "c1", ID: id, MaxBytes: 2})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(o.Output)).To(Equal("a"))
		Expect(o.Next).To(Equal(int64(1)))
	})

	It("limits the tail and the page", func() {
		f.Write(id, strings.Repeat("z", 3000))
		o := obs(0)
		Expect(o.Tail).To(HaveLen(bgjob.DefaultTailBytes))
		Expect(o.Output).To(HaveLen(3000))
		o, _ = f.Observe(ctx, bgjob.ObserveRequest{Owner: "alice", Chat: "c1", ID: id, MaxBytes: 100, TailBytes: 5})
		Expect(o.Output).To(HaveLen(100))
		Expect(o.Tail).To(Equal("zzzzz"))
	})

	It("answers unknown for another owner, another chat, a missing job and a forgotten job", func() {
		for _, req := range []bgjob.ObserveRequest{
			{Owner: "bob", Chat: "c1", ID: id},
			{Owner: "alice", Chat: "c2", ID: id},
			{Owner: "alice", Chat: "c1", ID: "job-nope"},
		} {
			_, err := f.Observe(ctx, req)
			Expect(err).To(MatchError(bgjob.ErrUnknown))
		}
		f.Forget(id)
		_, err := f.Observe(ctx, bgjob.ObserveRequest{Owner: "alice", Chat: "c1", ID: id})
		Expect(err).To(MatchError(bgjob.ErrUnknown))
	})

	It("shows a sub-agent's job to its parent, and the parent chat is in the info", func() {
		cid := f.Start(bgjob.Info{Owner: "alice", Chat: "child", Parent: "c1", Label: "sub"})
		o, err := f.Observe(ctx, bgjob.ObserveRequest{Owner: "alice", Chat: "c1", ID: cid})
		Expect(err).NotTo(HaveOccurred())
		Expect(o.Chat).To(Equal("child"))
		Expect(o.Parent).To(Equal("c1"))
		_, err = f.Observe(ctx, bgjob.ObserveRequest{Owner: "alice", Chat: "other", ID: cid})
		Expect(err).To(MatchError(bgjob.ErrUnknown))
	})

	It("lists running jobs first, then the newest finished, for the chat and its children", func() {
		now = now.Add(time.Minute)
		second := f.Start(bgjob.Info{Owner: "alice", Chat: "c1"})
		now = now.Add(time.Minute)
		third := f.Start(bgjob.Info{Owner: "alice", Chat: "child", Parent: "c1"})
		f.Start(bgjob.Info{Owner: "alice", Chat: "elsewhere"})
		f.Exit(third, 0)
		list, err := f.List(ctx, "alice", "c1")
		Expect(err).NotTo(HaveOccurred())
		var ids []string
		for _, i := range list {
			ids = append(ids, i.ID)
		}
		Expect(ids).To(Equal([]string{second, id, third}))
	})

	It("fails every call with Err, as a node that cannot be reached", func() {
		f.Err = errors.New("timeout")
		_, err := f.Observe(ctx, bgjob.ObserveRequest{Owner: "alice", Chat: "c1", ID: id})
		Expect(err).To(MatchError("timeout"))
		Expect(errors.Is(err, bgjob.ErrUnknown)).To(BeFalse())
		_, err = f.List(ctx, "alice", "c1")
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("the states", func() {
	It("knows which are final", func() {
		for _, s := range []bgjob.State{bgjob.StateStarting, bgjob.StateRunning, bgjob.StateEnding} {
			Expect(s.Finished()).To(BeFalse(), string(s))
		}
		for _, s := range []bgjob.State{bgjob.StateExited, bgjob.StateKilled, bgjob.StateFailed, bgjob.StateLost} {
			Expect(s.Finished()).To(BeTrue(), string(s))
		}
	})
})
