// SPDX-License-Identifier: MIT

package model

import (
	"context"
	"os"
	"path/filepath"
	"time"

	grpc "github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/LocalAI/pkg/system"
	process "github.com/mudler/go-processmanager"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type watchdogLifecycleBackend struct{ grpc.Backend }

func (*watchdogLifecycleBackend) IsBusy() bool               { return false }
func (*watchdogLifecycleBackend) Free(context.Context) error { return nil }

var _ = Describe("Watchdog backend lifecycle", func() {
	var loader *ModelLoader
	var wd *WatchDog
	BeforeEach(func() {
		loader = NewModelLoader(&system.SystemState{Model: system.Model{ModelsPath: GinkgoT().TempDir()}})
		wd = NewWatchDog(WithProcessManager(loader), WithIdleTimeout(time.Second), WithBusyTimeout(time.Second), WithLRULimit(1))
		loader.SetWatchDog(wd)
	})

	It("removes shutdown tracking and ignores late request completions", func() {
		loader.store.Set("model", NewModelWithClient("model", "old", &watchdogLifecycleBackend{}))
		wd.AddAddressModelMap("old", "model")
		finish := wd.TrackRequest("old")
		Expect(loader.ShutdownModelForce("model")).To(Succeed())
		finish()
		state := wd.GetState()
		Expect(state.AddressModelMap).To(BeEmpty())
		Expect(state.BusyTime).To(BeEmpty())
		Expect(state.IdleTime).To(BeEmpty())
		Expect(state.InFlight).To(BeEmpty())
		Expect(state.LastUsed).To(BeEmpty())
	})

	DescribeTable("does not evict a replacement backend from a stale address",
		func(evict func(*WatchDog)) {
			replacement := NewModelWithClient("model", "new", &watchdogLifecycleBackend{})
			loader.store.Set("model", replacement)
			wd.AddAddressModelMap("old", "model")
			wd.AddAddressModelMap("new", "model")
			wd.RegisterModelSize("model", 123)
			wd.lastUsed["old"] = time.Now().Add(-time.Hour)
			wd.lastUsed["new"] = time.Now()
			wd.idleTime["old"] = time.Now().Add(-time.Hour)
			evict(wd)
			resident, ok := loader.store.Get("model")
			Expect(ok).To(BeTrue())
			Expect(resident).To(BeIdenticalTo(replacement))
			Expect(wd.GetState().AddressModelMap).To(HaveKeyWithValue("new", "model"))
			Expect(wd.modelSizes).To(HaveKeyWithValue("model", int64(123)))
		},
		Entry("idle timeout", func(w *WatchDog) { w.checkIdle() }),
		Entry("busy timeout", func(w *WatchDog) { w.busyTime["old"] = time.Now().Add(-time.Hour); w.checkBusy() }),
		Entry("memory eviction", func(w *WatchDog) { w.evictLRUModel() }),
	)

	DescribeTable("does not stop a replacement after eviction selection",
		func(force bool) {
			wd.AddAddressModelMap("old", "model")
			if force {
				wd.TrackRequest("old")
			}
			targets, _ := wd.collectEvictionsLocked([]modelUsageInfo{{model: "model", address: "old"}}, 1, force)
			replacement := NewModelWithClient("model", "new", &watchdogLifecycleBackend{})
			loader.store.Set("model", replacement)
			wd.AddAddressModelMap("new", "model")
			wd.RegisterModelSize("model", 123)
			wd.shutdownEvicted(targets, "test")
			resident, ok := loader.store.Get("model")
			Expect(ok).To(BeTrue())
			Expect(resident).To(BeIdenticalTo(replacement))
			Expect(wd.modelSizes).To(HaveKeyWithValue("model", int64(123)))
		},
		Entry("graceful", false),
		Entry("forced", true),
	)

	DescribeTable("still stops the matching backend",
		func(force bool) {
			loader.store.Set("model", NewModelWithClient("model", "current", &watchdogLifecycleBackend{}))
			wd.AddAddressModelMap("current", "model")
			Expect(wd.shutdownTarget(evictionTarget{model: "model", address: "current", wasBusy: force})).To(Succeed())
			_, ok := loader.store.Get("model")
			Expect(ok).To(BeFalse())
			Expect(wd.GetState().AddressModelMap).To(BeEmpty())
		},
		Entry("graceful", false),
		Entry("forced", true),
	)

	It("keeps replacement tracking when an old process cleanup arrives late", func() {
		oldProcess := process.New()
		newProcess := process.New()
		wd.Add("same-address", oldProcess)
		wd.AddAddressModelMap("same-address", "model")
		wd.Add("same-address", newProcess)
		wd.RegisterModelSize("model", 123)
		finish := wd.TrackRequest("same-address")
		loader.cleanupProcessRuntime(oldProcess)
		Expect(wd.GetState().AddressMap).To(HaveKeyWithValue("same-address", newProcess))
		Expect(wd.modelSizes).To(HaveKeyWithValue("model", int64(123)))
		finish()
		Expect(wd.GetState().IdleTime).To(HaveKey("same-address"))
	})

	It("removes local process tracking synchronously during shutdown", func() {
		p := process.New()
		m := NewModelWithClient("model", "old", &watchdogLifecycleBackend{})
		m.process = p
		loader.store.Set("model", m)
		wd.Add("old", p)
		wd.AddAddressModelMap("old", "model")
		wd.TrackRequest("old")()
		Expect(loader.ShutdownModelForce("model")).To(Succeed())
		Expect(wd.GetState().AddressMap).To(BeEmpty())
		Expect(wd.GetState().IdleTime).To(BeEmpty())
	})

	It("cleans the current watchdog after its configuration is replaced", func() {
		p := process.New()
		wd.Add("old", p)
		wd.AddAddressModelMap("old", "model")
		replacement := NewWatchDog(WithProcessManager(loader))
		replacement.RestoreState(wd.GetState())
		loader.SetWatchDog(replacement)
		loader.cleanupProcessRuntime(p)
		Expect(replacement.GetState().AddressModelMap).To(BeEmpty())
	})

	It("untracks a backend that fails to start", func() {
		_, err := loader.startProcess(filepath.Join(GinkgoT().TempDir(), "missing"), "model", "old", nil)
		Expect(err).To(HaveOccurred())
		Expect(wd.GetState().AddressModelMap).To(BeEmpty())
		Expect(wd.GetState().AddressMap).To(BeEmpty())
	})

	It("untracks a backend after an unexpected exit", func() {
		backend := filepath.Join(GinkgoT().TempDir(), "backend")
		Expect(os.WriteFile(backend, []byte("#!/bin/sh\nexit 42\n"), 0o700)).To(Succeed())
		p, err := loader.startProcess(backend, "model", "old", nil)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { loader.cleanupProcessRuntime(p) })
		Eventually(p.Done()).Should(BeClosed())
		Eventually(func() map[string]string { return wd.GetState().AddressModelMap }).Should(BeEmpty())
	})
})
