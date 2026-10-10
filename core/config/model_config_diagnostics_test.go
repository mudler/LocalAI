// SPDX-License-Identifier: MIT
package config

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mudler/LocalAI/pkg/diagnostics"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Diagnostics config loader", func() {
	It("applies caller options under the lock after metadata, never on enumeration failure", func() {
		b := NewModelConfigLoader("")
		enumerated := false
		calls := 0
		b.diagnosticsReadDir = func(string) ([]os.DirEntry, error) { enumerated = true; return nil, nil }
		option := func(*LoadOptions) {
			calls++
			unlocked := b.TryLock()
			if unlocked {
				b.Unlock()
			}
			Expect(unlocked).To(BeFalse())
			Expect(enumerated).To(BeTrue())
		}
		Expect(b.LoadModelConfigsFromPath("", option)).To(Succeed())
		Expect(calls).To(Equal(1))
		b.diagnosticsReadDir = func(string) ([]os.DirEntry, error) { return nil, errors.New("enumeration") }
		Expect(b.LoadModelConfigsFromPath("", option)).To(HaveOccurred())
		Expect(calls).To(Equal(1))
	})

	It("preserves filtering and panic propagation and emits only after unlock", func() {
		b := NewModelConfigLoader("")
		b.configs["a"] = ModelConfig{Name: "a"}
		var events []diagnostics.Event
		r := diagnostics.NewRecorder(func(e diagnostics.Event) {
			Expect(b.TryLock()).To(BeTrue())
			b.Unlock()
			events = append(events, e)
		})
		ctx, cancel := context.WithCancel(r.Request(context.Background()))
		cancel()
		calls := 0
		got := b.GetModelConfigsByFilterContext(ctx, func(_ string, _ *ModelConfig) bool { calls++; return true })
		Expect(got).To(Equal(b.GetModelConfigsByFilter(nil)))
		Expect(calls).To(Equal(1))
		Expect(events).To(HaveLen(3))
		for _, e := range events {
			Expect(e.Kind).To(Equal(diagnostics.KindRequest))
			Expect(e.Count).To(Equal(1))
		}
		Expect(func() { b.GetModelConfigsByFilterContext(ctx, func(string, *ModelConfig) bool { panic("sentinel") }) }).To(PanicWith("sentinel"))
		Expect(b.TryLock()).To(BeTrue())
		b.Unlock()
		Expect(events[len(events)-1].Outcome).To(Equal(diagnostics.OutcomeError))
	})
	It("separates a real gated mutex wait from held work without time thresholds", func() {
		b := NewModelConfigLoader("")
		b.configs["a"] = ModelConfig{Name: "a"}
		held, release, waiting := make(chan struct{}), make(chan struct{}), make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.GetModelConfigsByFilter(func(string, *ModelConfig) bool { close(held); <-release; return true })
		}()
		<-held
		tick := 0
		b.diagnosticsNow = func() time.Time {
			tick++
			if tick == 1 {
				close(waiting)
			}
			return time.Unix(0, int64(tick))
		}
		var events []diagnostics.Event
		r := diagnostics.NewRecorder(func(e diagnostics.Event) { events = append(events, e) })
		wg.Add(1)
		go func() { defer wg.Done(); b.GetModelConfigsByFilterContext(r.Request(context.Background()), nil) }()
		<-waiting
		Expect(b.TryLock()).To(BeFalse())
		close(release)
		wg.Wait()
		Expect(events).To(HaveLen(3))
		Expect(events[0].Elapsed).To(Equal(time.Nanosecond))
		Expect(events[2].Elapsed).To(BeNumerically(">", events[0].Elapsed))
	})
	It("propagates startup options and aggregates actual reads including gallery classification", func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "a.yaml"), []byte("name: a\nbackend: llama-cpp\nparameters:\n  model: a.bin\n"), 0600)).To(Succeed())
		gallery := filepath.Join(dir, "gallery.yaml")
		Expect(os.WriteFile(gallery, []byte("- name: g\n  url: https://example.invalid/g\n"), 0600)).To(Succeed())
		b := NewModelConfigLoader(dir)
		var events []diagnostics.Event
		r := diagnostics.NewRecorder(func(e diagnostics.Event) { Expect(b.TryLock()).To(BeTrue()); b.Unlock(); events = append(events, e) })
		opts := []ConfigLoaderOption{LoadOptionDiagnostics(r), LoadOptionGalleryFiles(Gallery{URL: "file://" + gallery})}
		Expect(b.LoadModelConfigsFromPathStrict(dir, opts...)).To(Succeed())
		Expect(events).To(HaveLen(6))
		counts := map[diagnostics.Phase]int{}
		for _, e := range events {
			counts[e.Phase] = e.Count
			Expect(e.Kind).To(Equal(diagnostics.KindConfigReload))
		}
		Expect(counts[diagnostics.PhaseReloadMetadata]).To(Equal(2))
		Expect(counts[diagnostics.PhaseReloadYAMLRead]).To(Equal(2))
		Expect(counts[diagnostics.PhaseReloadParseDefaults]).To(Equal(2))
		first := events[0].ID
		Expect(b.LoadModelConfigsFromPathStrict(dir, opts...)).To(Succeed())
		Expect(events[6].ID).NotTo(Equal(first))
		off := NewModelConfigLoader(dir)
		Expect(off.LoadModelConfigsFromPathStrict(dir, opts[1:]...)).To(Succeed())
		Expect(off.GetModelConfigsByFilter(nil)).To(Equal(b.GetModelConfigsByFilter(nil)))
		app := &ApplicationConfig{DiagnosticsRecorder: r, SystemState: &system.SystemState{}}
		lo := &LoadOptions{}
		lo.Apply(app.ToConfigLoaderOptions()...)
		Expect(lo.diagnosticsRecorder).To(BeIdenticalTo(r))
	})
	It("retains strict and non-strict parse/read failures and records failed internal phases", func() {
		for _, readFailure := range []bool{false, true} {
			for _, strict := range []bool{false, true} {
				dir := GinkgoT().TempDir()
				if readFailure {
					Expect(os.Mkdir(filepath.Join(dir, "bad.yaml"), 0700)).To(Succeed())
				} else {
					Expect(os.WriteFile(filepath.Join(dir, "bad.yaml"), []byte("[invalid"), 0600)).To(Succeed())
				}
				b := NewModelConfigLoader(dir)
				var events []diagnostics.Event
				r := diagnostics.NewRecorder(func(e diagnostics.Event) { Expect(b.TryLock()).To(BeTrue()); b.Unlock(); events = append(events, e) })
				err := b.loadModelConfigsFromPath(dir, strict, LoadOptionDiagnostics(r))
				off := NewModelConfigLoader(dir)
				off.diagnosticsNow = func() time.Time { Fail("disabled clock read"); return time.Time{} }
				offErr := off.loadModelConfigsFromPath(dir, strict)
				if strict {
					Expect(err).To(HaveOccurred())
					Expect(offErr.Error()).To(Equal(err.Error()))
				} else {
					Expect(err).NotTo(HaveOccurred())
					Expect(offErr).NotTo(HaveOccurred())
				}
				phase := diagnostics.PhaseReloadParseDefaults
				if readFailure {
					phase = diagnostics.PhaseReloadYAMLRead
				}
				found := false
				for _, e := range events {
					if e.Phase == phase {
						found = true
						Expect(e.Outcome).To(Equal(diagnostics.OutcomeError))
						Expect(e.Count).To(Equal(1))
					}
				}
				Expect(found).To(BeTrue())
				Expect(off.GetModelConfigsByFilter(nil)).To(Equal(b.GetModelConfigsByFilter(nil)))
			}
		}
	})
	It("records enumeration and metadata failures without changing filesystem operations", func() {
		dir := GinkgoT().TempDir()
		for _, metadata := range []bool{false, true} {
			b := NewModelConfigLoader(dir)
			calls := 0
			if metadata {
				b.diagnosticsReadDir = func(string) ([]os.DirEntry, error) { calls++; return []os.DirEntry{diagnosticsBadEntry{}}, nil }
			} else {
				dir = filepath.Join(dir, "missing")
			}
			var events []diagnostics.Event
			r := diagnostics.NewRecorder(func(e diagnostics.Event) { Expect(b.TryLock()).To(BeTrue()); b.Unlock(); events = append(events, e) })
			Expect(b.LoadModelConfigsFromPath(dir, LoadOptionDiagnostics(r))).To(HaveOccurred())
			phase := diagnostics.PhaseReloadEnumeration
			if metadata {
				phase = diagnostics.PhaseReloadMetadata
				Expect(calls).To(Equal(1))
			}
			found := false
			for _, e := range events {
				if e.Phase == phase {
					found = true
					Expect(e.Outcome).To(Equal(diagnostics.OutcomeError))
					Expect(e.Count).To(Equal(1))
				}
			}
			Expect(found).To(BeTrue())
		}
	})
	It("keeps concurrent reload and request identities separate and direct callers disabled", func() {
		dir := GinkgoT().TempDir()
		b := NewModelConfigLoader(dir)
		var mu sync.Mutex
		var events []diagnostics.Event
		r := diagnostics.NewRecorder(func(e diagnostics.Event) { mu.Lock(); defer mu.Unlock(); events = append(events, e) })
		var wg sync.WaitGroup
		for range 2 {
			wg.Add(1)
			go func() {
				defer GinkgoRecover()
				defer wg.Done()
				Expect(b.LoadModelConfigsFromPath(dir, LoadOptionDiagnostics(r))).To(Succeed())
			}()
		}
		wg.Wait()
		b.GetModelConfigsByFilterContext(r.Request(context.Background()), nil)
		ids := map[string]diagnostics.Kind{}
		for _, e := range events {
			ids[e.ID] = e.Kind
		}
		Expect(ids).To(HaveLen(3))
		n := len(events)
		b.diagnosticsNow = func() time.Time { Fail("disabled clock read"); return time.Time{} }
		Expect(b.LoadModelConfigsFromPath(dir)).To(Succeed())
		b.GetModelConfigsByFilter(nil)
		Expect(events).To(HaveLen(n))
	})

})

type diagnosticsBadEntry struct{}

func (diagnosticsBadEntry) Name() string               { return "bad.yaml" }
func (diagnosticsBadEntry) IsDir() bool                { return false }
func (diagnosticsBadEntry) Type() fs.FileMode          { return 0 }
func (diagnosticsBadEntry) Info() (fs.FileInfo, error) { return nil, errors.New("metadata failed") }
