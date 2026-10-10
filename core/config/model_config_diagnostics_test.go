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
	DescribeTable("preserves stateful caller options for each model's defaults",
		func(enabled bool) {
			dir := GinkgoT().TempDir()
			Expect(os.WriteFile(filepath.Join(dir, "models.yaml"), []byte("- name: a\n- name: b\n"), 0600)).To(Succeed())
			var events []diagnostics.Event
			var recorder *diagnostics.Recorder
			if enabled {
				recorder = diagnostics.NewRecorder(func(e diagnostics.Event) { events = append(events, e) })
			}
			b := NewModelConfigLoader(dir, WithReloadDiagnostics(recorder))
			calls := 0
			Expect(b.LoadModelConfigsFromPath(dir, func(o *LoadOptions) {
				calls++
				LoadOptionThreads(calls)(o)
			}, LoadOptionDiagnostics(recorder))).To(Succeed())
			Expect(calls).To(Equal(3))
			Expect(*b.configs["a"].Threads).To(Equal(2))
			Expect(*b.configs["b"].Threads).To(Equal(3))
			if enabled {
				Expect(events).NotTo(BeEmpty())
			} else {
				Expect(events).To(BeEmpty())
			}
		},
		Entry("with diagnostics enabled", true),
		Entry("with diagnostics disabled", false),
	)

	It("uses explicit startup timing for early failures and preserves identity across late selection", func() {
		for _, mode := range []string{"same", "nil", "different", "failure"} {
			dir := GinkgoT().TempDir()
			Expect(os.WriteFile(filepath.Join(dir, "a.yaml"), []byte("name: a\n"), 0600)).To(Succeed())
			var early, late []diagnostics.Event
			r := diagnostics.NewRecorder(func(e diagnostics.Event) { early = append(early, e) })
			other := diagnostics.NewRecorder(func(e diagnostics.Event) { late = append(late, e) })
			shared := NewModelConfigLoader(dir, WithReloadDiagnostics(r))
			b := NewModelConfigLoader(dir, shared.ReloadDiagnosticsOption())
			selected := r
			if mode == "nil" {
				selected = nil
			}
			if mode == "different" {
				selected = other
			}
			calls := 0
			if mode == "failure" {
				b.diagnosticsReadDir = func(string) ([]os.DirEntry, error) { return nil, errors.New("enumeration") }
			}
			err := b.LoadModelConfigsFromPath(dir, func(o *LoadOptions) { calls++; LoadOptionDiagnostics(selected)(o) })
			if mode == "failure" {
				Expect(err).To(HaveOccurred())
				Expect(calls).To(BeZero())
				Expect(early).To(HaveLen(3))
				Expect(early[1].Phase).To(Equal(diagnostics.PhaseReloadEnumeration))
				Expect(early[1].Outcome).To(Equal(diagnostics.OutcomeError))
			} else {
				Expect(err).NotTo(HaveOccurred())
				Expect(calls).To(Equal(2))
				if mode == "same" {
					Expect(early).To(HaveLen(6))
				} else {
					Expect(early).To(HaveLen(4))
				}
			}
			for _, e := range early {
				Expect(e.ID).To(Equal(early[0].ID))
			}
			if mode == "different" {
				Expect(late).To(HaveLen(2))
				Expect(late[0].ID).NotTo(Equal(early[0].ID))
				Expect(late[1].ID).To(Equal(late[0].ID))
			}
		}
	})

	It("honors composed diagnostics options and last writes at each original application site", func() {
		for _, enabled := range []bool{true, false} {
			dir := GinkgoT().TempDir()
			Expect(os.WriteFile(filepath.Join(dir, "a.yaml"), []byte("name: a\nbackend: llama-cpp\n"), 0600)).To(Succeed())
			b := NewModelConfigLoader(dir)
			var events []diagnostics.Event
			r := diagnostics.NewRecorder(func(e diagnostics.Event) { events = append(events, e) })
			var order []string
			b.diagnosticsReadDir = func(path string) ([]os.DirEntry, error) {
				order = append(order, "enumerate")
				entries, err := os.ReadDir(path)
				return []os.DirEntry{diagnosticsTrackedEntry{DirEntry: entries[0], visit: func() { order = append(order, "metadata") }}}, err
			}
			wrap := func(label string, option ConfigLoaderOption) ConfigLoaderOption {
				return func(o *LoadOptions) {
					unlocked := b.TryLock()
					if unlocked {
						b.Unlock()
					}
					Expect(unlocked).To(BeFalse())
					order = append(order, label)
					option(o)
				}
			}
			var last *diagnostics.Recorder
			if enabled {
				b.diagnosticsNow = func() time.Time {
					Expect(order).To(Or(
						Equal([]string{"enumerate", "metadata", "first", "last"}),
						Equal([]string{"enumerate", "metadata", "first", "last", "first", "last"}),
					))
					return time.Now()
				}
				last = r
			} else {
				b.diagnosticsNow = func() time.Time { Fail("disabled clock read"); return time.Time{} }
			}
			Expect(b.LoadModelConfigsFromPath(dir, LoadOptionDiagnostics(r), wrap("first", LoadOptionDiagnostics(nil)), wrap("last", LoadOptionDiagnostics(last)))).To(Succeed())
			Expect(order).To(Equal([]string{"enumerate", "metadata", "first", "last", "first", "last"}))
			if enabled {
				Expect(events).To(HaveLen(2))
				Expect(events[0].Phase).To(Equal(diagnostics.PhaseReloadYAMLRead))
				Expect(events[1].Phase).To(Equal(diagnostics.PhaseReloadParseDefaults))
			} else {
				Expect(events).To(BeEmpty())
			}
		}
	})

	It("records malformed configured gallery parse errors without changing strict behavior", func() {
		for _, strict := range []bool{false, true} {
			dir := GinkgoT().TempDir()
			path := filepath.Join(dir, "gallery.yaml")
			Expect(os.WriteFile(path, []byte("[invalid"), 0600)).To(Succeed())
			var events []diagnostics.Event
			r := diagnostics.NewRecorder(func(e diagnostics.Event) { events = append(events, e) })
			gallery := LoadOptionGalleryFiles(Gallery{URL: "file://" + path})
			b := NewModelConfigLoader(dir)
			err := b.loadModelConfigsFromPath(dir, strict, gallery, LoadOptionDiagnostics(r))
			off := NewModelConfigLoader(dir)
			offErr := off.loadModelConfigsFromPath(dir, strict, gallery)
			if strict {
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(Equal(offErr.Error()))
			} else {
				Expect(err).NotTo(HaveOccurred())
				Expect(offErr).NotTo(HaveOccurred())
			}
			Expect(b.GetAllModelsConfigs()).To(Equal(off.GetAllModelsConfigs()))
			var reads, parses int
			for _, e := range events {
				if e.Phase == diagnostics.PhaseReloadYAMLRead {
					reads += e.Count
				}
				if e.Phase == diagnostics.PhaseReloadParseDefaults {
					parses += e.Count
					Expect(e.Outcome).To(Equal(diagnostics.OutcomeError))
				}
			}
			Expect(reads).To(Equal(1))
			Expect(parses).To(Equal(1))
			ok, classifyErr := classifyGalleryDocument(path)
			Expect(ok).To(BeFalse())
			Expect(classifyErr).NotTo(HaveOccurred())
		}
	})

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
		WithReloadDiagnostics(r)(b)
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
			WithReloadDiagnostics(r)(b)
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
		WithReloadDiagnostics(r)(b)
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
		b = NewModelConfigLoader(dir)
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

type diagnosticsTrackedEntry struct {
	os.DirEntry
	visit func()
}

func (e diagnosticsTrackedEntry) Info() (fs.FileInfo, error) { e.visit(); return e.DirEntry.Info() }
