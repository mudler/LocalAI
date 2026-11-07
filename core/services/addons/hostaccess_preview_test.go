// SPDX-License-Identifier: MIT
package addons

import (
	"context"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/updates"
	"net/http/httptest"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the host access preview of a release", func() {
	It("downloads and verifies the package once, so the next read of the same release is immediate", func() {
		rig := newUpdateRig(false)
		rig.lib.archiveDelay = 500 * time.Millisecond

		start := time.Now()
		first, e := rig.m.PreviewRelease(ctx, "test", "sample", "1.2.3")
		firstTook := time.Since(start)
		Expect(e).NotTo(HaveOccurred())
		Expect(firstTook).To(BeNumerically(">=", 500*time.Millisecond))

		start = time.Now()
		second, e := rig.m.PreviewRelease(ctx, "test", "sample", "1.2.3")
		secondTook := time.Since(start)
		Expect(e).NotTo(HaveOccurred())
		GinkgoWriter.Printf("preview: first %s, second %s\n", firstTook, secondTook)
		Expect(second).To(Equal(first))
		Expect(secondTook).To(BeNumerically("<", 100*time.Millisecond))
	})

	It("reads again when the library lists another package for the same version", func() {
		rig := newUpdateRig(false)
		first, e := rig.m.PreviewRelease(ctx, "test", "sample", "1.2.3")
		Expect(e).NotTo(HaveOccurred())
		rig.lib.publish("1.2.3", "", "other-bytes", catalog.Requirements{})
		rig.lib.refresh()
		second, e := rig.m.PreviewRelease(ctx, "test", "sample", "1.2.3")
		Expect(e).NotTo(HaveOccurred())
		Expect(second.Digest).NotTo(Equal(first.Digest))
	})

	It("does not wait behind a read of the same release that is in flight", func() {
		rig := newUpdateRig(false)
		rig.lib.archiveDelay = 400 * time.Millisecond
		done := make(chan time.Duration, 2)
		for i := 0; i < 2; i++ {
			go func() {
				defer GinkgoRecover()
				start := time.Now()
				_, e := rig.m.PreviewRelease(ctx, "test", "sample", "1.2.3")
				Expect(e).NotTo(HaveOccurred())
				done <- time.Since(start)
			}()
		}
		a, b := <-done, <-done
		GinkgoWriter.Printf("two concurrent previews: %s and %s\n", a, b)
		Expect(max(a, b)).To(BeNumerically("<", 700*time.Millisecond))
	})
})

var _ = Describe("the update status after a restart", func() {
	It("is a recent check once the owner has refreshed in the background, not unknown", func() {
		r := newUpdateRig(false)
		reg := httptest.NewServer(registry.New())
		DeferCleanup(reg.Close)
		artifact := "oci://" + strings.TrimPrefix(reg.URL, "http://") + "/local-ai-addons:sample-development"
		current := pushDevelopment(artifact, "0.0.0-dev.aaaaaaaaaaaa", "dev-1")
		r.m.installer.Lookup = func(context.Context, string, string) (string, error) { return current, nil }
		fresh := func() {
			r.m.EnableUpdates(updates.Config{Now: func() time.Time { return *r.clk }, MinRefresh: time.Second, TTL: time.Hour})
		}
		fresh()
		r.lib.mu.Lock()
		r.lib.releases = append(r.lib.releases, catalog.Release{ID: "sample", Name: "Sample", Version: "0.0.0-development", Channel: "development",
			Artifact: artifact, Mutable: true, OS: "linux", Arch: "amd64"})
		r.lib.mu.Unlock()
		r.lib.refresh()
		_, e := r.m.Submit(ctx, actor, LifecycleCommand{OperationID: "install", AddonID: "sample", Action: "install", SourceID: "test", Version: "0.0.0-development"})
		Expect(e).NotTo(HaveOccurred())

		// A restart empties the cache of the check. A moving tag has no
		// resolved digest then, and the page read unknown on every visit.
		fresh()
		before, e := r.m.UpdateStatus(ctx, actor, "sample")
		Expect(e).NotTo(HaveOccurred())
		Expect(before.State).To(Equal(updates.Unknown))

		Expect(r.m.RefreshUpdates(ctx)).To(Equal(1))
		after, e := r.m.UpdateStatus(ctx, actor, "sample")
		Expect(e).NotTo(HaveOccurred())
		Expect(after.State).To(Equal(updates.UpToDate))
		Expect(after.CheckedAt.IsZero()).To(BeFalse())
	})
})
