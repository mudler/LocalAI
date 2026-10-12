// SPDX-License-Identifier: MIT
package workerstatus

import (
	"context"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestStatus(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Worker status") }

var _ = Describe("Worker status", func() {
	It("publishes private atomic snapshots and rejects old sessions and unsafe files", func() {
		root := GinkgoT().TempDir()
		path, generation, err := NewSession(root)
		Expect(err).NotTo(HaveOccurred())
		s := Snapshot{Schema: 1, Generation: generation, Sequence: 1, Time: time.Now(), Registration: "starting", HeartbeatInterval: 10 * time.Second}
		Expect(Write(path, s)).To(Succeed())
		got, err := Read(path, generation)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Sequence).To(Equal(uint64(1)))
		_, err = Read(path, "old")
		Expect(err).To(HaveOccurred())
		st, err := os.Stat(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(st.Mode().Perm()).To(Equal(os.FileMode(0600)))
		st, err = os.Stat(filepath.Dir(path))
		Expect(err).NotTo(HaveOccurred())
		Expect(st.Mode().Perm()).To(Equal(os.FileMode(0700)))
		Expect(os.Chmod(path, 0644)).To(Succeed())
		_, err = Read(path, generation)
		Expect(err).To(HaveOccurred())
		Expect(Write(path, s)).NotTo(Succeed())
		Expect(os.Remove(path)).To(Succeed())
		Expect(os.Symlink("missing", path)).To(Succeed())
		Expect(Write(path, s)).NotTo(Succeed())
		_, err = Read(path, generation)
		Expect(err).To(HaveOccurred())
	})
	It("requires all evidence and sequence progress, not repeated reads or early readyz", func() {
		now := time.Now()
		t := Tracker{Generation: "session", Started: now}
		s := Snapshot{Schema: 1, Generation: "session", Sequence: 1, Time: now, Registration: "healthy", HeartbeatInterval: 10 * time.Second, LocalReady: true}
		Expect(t.Classify(s, now)).NotTo(Equal("ready"))
		s.Credentials = true
		s.Connected = true
		s.Authenticated = true
		s.Subscriptions = true
		s.Armed = true
		s.Heartbeat = now
		s.Sequence++
		Expect(t.Classify(s, now)).To(Equal("ready"))
		Expect(t.Classify(s, now.Add(10*time.Second))).To(Equal("status_unavailable"))
		s.Sequence++
		s.Heartbeat = now.Add(-31 * time.Second)
		Expect(t.Classify(s, now.Add(11*time.Second))).To(Equal("heartbeat"))
		s.Sequence++
		s.Registration = "pending"
		s.RegistrationTime = now.Add(35 * time.Second)
		s.Time = s.RegistrationTime
		Expect(t.Classify(s, now.Add(35*time.Second))).To(Equal("pending"))
		s.Generation = "old"
		Expect(t.Classify(s, now)).To(Equal("status_unavailable"))
	})
})

var _ = Describe("Worker status security", func() {
	It("rejects malformed, oversized, future-schema, linked and unsafe-directory evidence", func() {
		path, generation, err := NewSession(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		for _, data := range []string{"{", `{"schema_version":99}`, string(make([]byte, MaxSize+1)), `{} {}`} {
			Expect(os.WriteFile(path, []byte(data), 0600)).To(Succeed())
			_, err = Read(path, generation)
			Expect(err).To(HaveOccurred())
		}
		Expect(os.Remove(path)).To(Succeed())
		target := filepath.Join(filepath.Dir(path), "other")
		Expect(os.WriteFile(target, []byte("{}"), 0600)).To(Succeed())
		Expect(os.Link(target, path)).To(Succeed())
		_, err = Read(path, generation)
		Expect(err).To(HaveOccurred())
		Expect(os.Remove(path)).To(Succeed())
		Expect(os.Chmod(filepath.Dir(path), 0755)).To(Succeed())
		_, err = Read(path, generation)
		Expect(err).To(HaveOccurred())
	})
	It("fails closed on publication failure and never adopts an existing session", func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		path, generation, err := NewSession(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		p, err := NewPublisher(ctx, path, generation, cancel)
		Expect(err).NotTo(HaveOccurred())
		_, err = NewPublisher(ctx, path, generation, cancel)
		Expect(err).To(HaveOccurred())
		p.Registration("healthy", true, nil)
		p.Arm(func() (bool, bool, bool, bool) { return true, true, true, true })
		p.Heartbeat(nil)
		s, err := Read(path, generation)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Armed).To(BeTrue())
		Expect(os.Chmod(path, 0644)).To(Succeed())
		p.Update(nil)
		Eventually(ctx.Done()).Should(BeClosed())
		_, err = Read(path, generation)
		Expect(err).To(HaveOccurred())
		p.Update(nil)
		Expect(os.IsNotExist(func() error { _, e := os.Stat(path); return e }())).To(BeTrue())
	})
	It("preserves explicit approval independently of credentials and expires pending evidence", func() {
		now := time.Now()
		s := Snapshot{Schema: 1, Generation: "test", Sequence: 1, Time: now, Registration: "unknown", Credentials: true, Authenticated: true, Connected: true, Subscriptions: true, Armed: true, LocalReady: true, Heartbeat: now, HeartbeatInterval: 10 * time.Second}
		tracker := Tracker{Generation: "test", Started: now}
		Expect(tracker.Classify(s, now)).To(Equal("registration"))
		s.Sequence++
		s.Registration = "pending"
		s.RegistrationTime = now.Add(-31 * time.Second)
		Expect(tracker.Classify(s, now)).To(Equal("registration"))
		s.Sequence++
		s.Registration = "starting"
		Expect(tracker.Classify(s, now.Add(31*time.Second))).To(Equal("registration"))
		s.Sequence++
		s.Registration = "healthy"
		s.Credentials = false
		Expect(tracker.Classify(s, now)).To(Equal("credentials"))
		s.Sequence--
		Expect(tracker.Classify(s, now)).To(Equal("status_unavailable"))
	})
})

var _ = Describe("Worker status atomicity", func() {
	It("serializes concurrent transitions and keeps complete snapshots", func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		path, generation, err := NewSession(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		p, err := NewPublisher(ctx, path, generation, cancel)
		Expect(err).NotTo(HaveOccurred())
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Go(func() {
				defer GinkgoRecover()
				for j := 0; j < 20; j++ {
					p.Update(nil)
					s, e := Read(path, generation)
					Expect(e).NotTo(HaveOccurred())
					Expect(s.Generation).To(Equal(generation))
				}
			})
		}
		wg.Wait()
		s, err := Read(path, generation)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Sequence).To(BeNumerically(">=", 81))
	})
	It("rejects a symlink run directory and owner mismatch where supported", func() {
		root := GinkgoT().TempDir()
		path, generation, err := NewSession(root)
		Expect(err).NotTo(HaveOccurred())
		link := filepath.Join(root, "link")
		Expect(os.Symlink(filepath.Dir(path), link)).To(Succeed())
		_, err = Read(filepath.Join(link, "status.json"), generation)
		Expect(err).To(HaveOccurred())
		if os.Geteuid() == 0 {
			s := Snapshot{Schema: 1, Generation: generation, Sequence: 1, Time: time.Now(), Registration: "starting", HeartbeatInterval: time.Second}
			Expect(Write(path, s)).To(Succeed())
			Expect(os.Chown(path, 65534, 65534)).To(Succeed())
			_, err = Read(path, generation)
			Expect(err).To(HaveOccurred())
			Expect(Write(path, s)).NotTo(Succeed())
		}
	})
})
