// SPDX-License-Identifier: MIT
package musicapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/mudler/LocalAI/core/services/music"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/http/endpoints/localai/musicrequest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("durable pre-resolution uploads", func() {
	var dir string
	var u *Uploads
	var now time.Time
	BeforeEach(func() {
		var e error
		dir, e = os.MkdirTemp("", "musicapi-upload-")
		Expect(e).NotTo(HaveOccurred())
		now = time.Now()
		u, e = NewUploads(dir, func() time.Time { return now })
		Expect(e).NotTo(HaveOccurred())
	})
	AfterEach(func() { Expect(u.Close()).To(Succeed()); Expect(os.RemoveAll(dir)).To(Succeed()) })
	makeUpload := func() *musicrequest.OwnedUpload {
		id, e := randomID()
		Expect(e).NotTo(HaveOccurred())
		v, e := u.Create(context.WithValue(context.Background(), entryKey{}, entry{id, now.Add(900 * time.Second)}))
		Expect(e).NotTo(HaveOccurred())
		return v
	}
	It("requires private handler entry context", func() { _, e := u.Create(context.Background()); Expect(e).To(HaveOccurred()) })
	It("writes private durable metadata without recording or notation and bounds expiry", func() {
		v := makeUpload()
		defer func() { Expect(v.Cleanup().Wait()).To(Succeed()) }()
		_, e := v.Writer.Write([]byte("private recording"))
		Expect(e).NotTo(HaveOccurred())
		Expect(v.Writer.Close()).To(Succeed())
		files, e := os.ReadDir(dir)
		Expect(e).NotTo(HaveOccurred())
		Expect(files).To(HaveLen(2))
		data, e := os.ReadFile(filepath.Join(dir, v.Ref+".json"))
		Expect(e).NotTo(HaveOccurred())
		Expect(string(data)).NotTo(ContainSubstring("recording"))
		var r uploadRecord
		Expect(json.Unmarshal(data, &r)).To(Succeed())
		Expect(r.Expires).To(BeTemporally("==", now.Add(4500*time.Second)))
		for _, f := range files {
			st, e := f.Info()
			Expect(e).NotTo(HaveOccurred())
			Expect(st.Mode().Perm()).To(Equal(os.FileMode(0600)))
		}
	})
	It("does not reclaim a live writer merely because time passes", func() {
		v := makeUpload()
		now = now.Add(2 * time.Hour)
		Expect(u.Reclaim(context.Background())).To(Succeed())
		_, e := v.Writer.Write([]byte("still owned"))
		Expect(e).NotTo(HaveOccurred())
		Expect(v.Cleanup().Wait()).To(Succeed())
	})
	It("reclaims expired recording after restart", func() {
		v := makeUpload()
		Expect(v.Writer.Close()).To(Succeed())
		Expect(u.root.Close()).To(Succeed())
		var e error
		u, e = NewUploads(dir, func() time.Time { return now })
		Expect(e).NotTo(HaveOccurred())
		now = now.Add(2 * time.Hour)
		Expect(u.Reclaim(context.Background())).To(Succeed())
		files, e := os.ReadDir(dir)
		Expect(e).NotTo(HaveOccurred())
		Expect(files).To(BeEmpty())
	})
	It("reclaims a durable pending record left before atomic rename", func() {
		id, _ := randomID()
		req, _ := randomID()
		data, e := json.Marshal(uploadRecord{ID: id, Request: req, Expires: now.Add(-time.Second)})
		Expect(e).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(dir, id+".pending"), data, 0600)).To(Succeed())
		Expect(u.Reclaim(context.Background())).To(Succeed())
		files, e := os.ReadDir(dir)
		Expect(e).NotTo(HaveOccurred())
		Expect(files).To(BeEmpty())
	})
	It("keeps cleanup and close idempotent", func() {
		v := makeUpload()
		Expect(v.Writer.Close()).To(Succeed())
		Expect(v.Writer.Close()).To(Succeed())
		Expect(v.Cleanup().Wait()).To(Succeed())
		Expect(v.Cleanup().Wait()).To(Succeed())
	})
	It("rejects non-private roots", func() {
		other := filepath.Join(dir, "public")
		Expect(os.Mkdir(other, 0755)).To(Succeed())
		_, e := NewUploads(other, time.Now)
		Expect(e).To(HaveOccurred())
		Expect(os.Remove(other)).To(Succeed())
	})
	It("acknowledges ALL old cleanup waiters when a repeated attempt succeeds", func() {
		v := makeUpload()
		Expect(v.Writer.Close()).To(Succeed())
		audio := filepath.Join(dir, v.Ref+".audio")
		Expect(os.Remove(audio)).To(Succeed())
		Expect(os.Mkdir(audio, 0700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(audio, "busy"), []byte("x"), 0600)).To(Succeed())
		first := v.Cleanup()
		second := v.Cleanup()
		Expect(first.AttemptError).To(HaveOccurred())
		Expect(second.AttemptError).To(HaveOccurred())
		Expect(first.Released).NotTo(Receive())
		Expect(second.Released).NotTo(Receive())
		Expect(os.RemoveAll(audio)).To(Succeed())
		third := v.Cleanup()
		Expect(third.Wait()).To(Succeed())
		var a, b error
		Eventually(first.Released, 100*time.Millisecond).Should(Receive(&a))
		Expect(a).NotTo(HaveOccurred())
		Eventually(second.Released, 100*time.Millisecond).Should(Receive(&b))
		Expect(b).NotTo(HaveOccurred())
		Expect(u.waiters).To(BeEmpty())
		Expect(u.Reclaim(context.Background())).To(Succeed())
	})
	It("reads at most one 64-entry directory page per reclamation call", func() {
		for i := 0; i < 140; i++ {
			Expect(os.WriteFile(filepath.Join(dir, fmt.Sprintf("noise-%03d", i)), nil, 0600)).To(Succeed())
		}
		counter := &countContext{Context: context.Background()}
		Expect(u.Reclaim(counter)).To(Succeed())
		Expect(counter.calls.Load()).To(BeNumerically("<=", 1))
		Expect(u.scan).NotTo(BeNil())
		remaining, e := u.scan.ReadDir(-1)
		Expect(e).NotTo(HaveOccurred())
		Expect(remaining).To(HaveLen(76))
		Expect(u.scan.Close()).To(Succeed())
		u.scan = nil
		// Noise counts against the budget; repeated calls must still reach expired records.
		id, _ := randomID()
		req, _ := randomID()
		data, _ := json.Marshal(uploadRecord{ID: id, Request: req, Expires: now.Add(-time.Second)})
		Expect(os.WriteFile(filepath.Join(dir, id+".json"), data, 0600)).To(Succeed())
		for i := 0; i < 8; i++ {
			Expect(u.Reclaim(context.Background())).To(Succeed())
			v := makeUpload()
			Expect(v.Cleanup().Wait()).To(Succeed())
		}
		_, e = os.Stat(filepath.Join(dir, id+".json"))
		Expect(os.IsNotExist(e)).To(BeTrue())
	})
	It("bounds cleanup dependency attempts without treating timeout as release", func() {
		parent, cancelParent := context.WithCancel(context.Background())
		cancelParent()
		ctx, cancel := cleanupAttempt(parent)
		defer cancel()
		Expect(ctx.Err()).NotTo(HaveOccurred())
		d, ok := ctx.Deadline()
		Expect(ok).To(BeTrue())
		Expect(time.Until(d)).To(BeNumerically(">", 29*time.Second))
		Expect(time.Until(d)).To(BeNumerically("<=", 30*time.Second))
		released := make(chan error, 2)
		task := music.CleanupTask{AttemptError: errors.New("attempt failed"), Released: released}
		done := make(chan error, 1)
		go func() { done <- task.Wait() }()
		cancel()
		released <- ctx.Err()
		Consistently(done, 20*time.Millisecond).ShouldNot(Receive())
		released <- nil
		Eventually(done).Should(Receive(HaveOccurred()))
	})

	It("allows concurrent Create and cleanup between bounded reclamation entries", func() {
		for i := 0; i < 140; i++ {
			Expect(os.WriteFile(filepath.Join(dir, fmt.Sprintf("noise-%03d", i)), nil, 0600)).To(Succeed())
		}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer GinkgoRecover()
			defer wg.Done()
			for i := 0; i < 10; i++ {
				Expect(u.Reclaim(context.Background())).To(Succeed())
			}
		}()
		go func() {
			defer GinkgoRecover()
			defer wg.Done()
			for i := 0; i < 10; i++ {
				v := makeUpload()
				Expect(v.Cleanup().Wait()).To(Succeed())
			}
		}()
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		Eventually(done, 3*time.Second).Should(BeClosed())
	})

})

type countContext struct {
	context.Context
	calls atomic.Int32
}

func (c *countContext) Err() error { c.calls.Add(1); return c.Context.Err() }
