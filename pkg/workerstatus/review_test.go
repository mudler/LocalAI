// SPDX-License-Identifier: MIT
package workerstatus

import (
	"context"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"os"
	"time"
)

var _ = Describe("Worker status review regressions", func() {
	It("rejects expired approval even with fresh heartbeat and publication", func() {
		now := time.Now()
		for _, approval := range []time.Time{{}, now.Add(-31 * time.Second)} {
			s := Snapshot{PID: os.Getpid(), Schema: Schema, Generation: "g", Sequence: 1, Time: now, Registration: "healthy", RegistrationTime: approval, Credentials: true, Authenticated: true, Connected: true, Subscriptions: true, Armed: true, LocalReady: true, Heartbeat: now, HeartbeatInterval: 10 * time.Second}
			tracker := Tracker{ExpectedPID: os.Getpid(), Generation: "g", Started: now}
			Expect(tracker.Classify(s, now)).To(Equal("registration"))
		}
	})
	It("does not grant unseen stale publications a new lease", func() {
		now := time.Now()
		s := Snapshot{PID: os.Getpid(), Schema: Schema, Generation: "g", Sequence: 1, Time: now.Add(-time.Hour), Registration: "healthy", RegistrationTime: now.Add(-time.Hour), Credentials: true, Authenticated: true, Connected: true, Subscriptions: true, Armed: true, LocalReady: true, Heartbeat: now.Add(-time.Hour), HeartbeatInterval: 10 * time.Second}
		tracker := Tracker{ExpectedPID: os.Getpid(), Generation: "g", Started: now}
		Expect(tracker.Classify(s, now)).To(Equal("status_unavailable"))
	})
})

var _ = Describe("Worker status admission", func() {
	It("rejects wrong identities, future timestamps and stale progress after a pause", func() {
		now := time.Now()
		s := Snapshot{PID: os.Getpid(), Schema: Schema, Generation: "g", Sequence: 1, Time: now, Registration: "starting", HeartbeatInterval: time.Second}
		tracker := Tracker{ExpectedPID: os.Getpid(), Generation: "g", Started: now}
		bad := s
		bad.PID++
		Expect(tracker.Classify(bad, now)).To(Equal("status_unavailable"))
		bad = s
		bad.Time = now.Add(time.Second)
		Expect(tracker.Classify(bad, now)).To(Equal("status_unavailable"))
		bad = s
		bad.RegistrationTime = now.Add(time.Second)
		Expect(tracker.Classify(bad, now)).To(Equal("status_unavailable"))
		Expect(tracker.Classify(s, now)).To(Equal("starting"))
		s.Sequence++
		Expect(tracker.Classify(s, now.Add(time.Minute))).To(Equal("status_unavailable"))
	})
	It("activates only complete inherited opt-in and records its actual PID", func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		GinkgoT().Setenv(PathEnv, "")
		GinkgoT().Setenv(GenerationEnv, "")
		p, err := FromEnvironment(ctx, cancel)
		Expect(err).NotTo(HaveOccurred())
		Expect(p).To(BeNil())
		path, generation, err := NewSession(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		GinkgoT().Setenv(PathEnv, path)
		_, err = FromEnvironment(ctx, cancel)
		Expect(err).To(HaveOccurred())
		GinkgoT().Setenv(GenerationEnv, generation)
		p, err = FromEnvironment(ctx, cancel)
		Expect(err).NotTo(HaveOccurred())
		Expect(p).NotTo(BeNil())
		s, err := Read(path, generation)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.PID).To(Equal(os.Getpid()))
	})
})
