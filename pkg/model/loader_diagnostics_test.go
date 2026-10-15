// SPDX-License-Identifier: MIT
package model

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/mudler/LocalAI/pkg/diagnostics"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Diagnostics model directory enumeration", func() {
	It("counts only the existing ReadDir and separates filename work without leaking paths", func() {
		dir := GinkgoT().TempDir()
		for _, name := range []string{"secret-candidate", "z-model", "weights.GGUF", "config.yaml", "CACHEDIR.TAG", "x.bak-save"} {
			Expect(os.WriteFile(filepath.Join(dir, name), nil, 0600)).To(Succeed())
		}
		Expect(os.Mkdir(filepath.Join(dir, "secret-directory"), 0700)).To(Succeed())
		ml := &ModelLoader{ModelPath: dir}
		var events []diagnostics.Event
		ctx := diagnostics.NewRecorder(func(e diagnostics.Event) { events = append(events, e) }).Request(context.Background())
		calls := 0
		readDir := func(path string) ([]os.DirEntry, error) {
			calls++
			Expect(events).To(BeEmpty()) // Emission happens after ReadDir, outside the measured operation.
			return os.ReadDir(path)
		}
		plain, err := ml.listFilesInModelPathContext(context.Background(), readDir)
		Expect(err).NotTo(HaveOccurred())
		got, err := ml.listFilesInModelPathContext(ctx, readDir)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(plain))
		Expect(got).To(Equal([]string{"secret-candidate", "z-model"}))
		Expect(calls).To(Equal(2))
		Expect(events).To(HaveLen(3))
		Expect(events[0].Count).To(Equal(1))
		Expect(events[2].Count).To(Equal(7))
		Expect(events[2].Phase).To(Equal(diagnostics.PhaseLooseFilter))
		for _, e := range events {
			Expect(e.ID).To(Equal(events[0].ID))
			Expect(e.Outcome).To(Equal(diagnostics.OutcomeOK))
		}
		Expect(fmt.Sprint(events)).NotTo(ContainSubstring("secret"))
		Expect(fmt.Sprint(events)).NotTo(ContainSubstring(dir))
		wrapped, err := ml.ListFilesInModelPath()
		Expect(err).NotTo(HaveOccurred())
		Expect(wrapped).To(Equal(got))
		Expect(events).To(HaveLen(3))
		observed, err := ml.ListFilesInModelPathContext(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(observed).To(Equal(got))
	})
	It("preserves missing-directory errors and does not run filename filtering", func() {
		ml := &ModelLoader{ModelPath: filepath.Join(GinkgoT().TempDir(), "secret-missing")}
		var events []diagnostics.Event
		ctx := diagnostics.NewRecorder(func(e diagnostics.Event) { events = append(events, e) }).Request(context.Background())
		plain, plainErr := ml.ListFilesInModelPath()
		got, err := ml.ListFilesInModelPathContext(ctx)
		Expect(os.IsNotExist(err)).To(BeTrue())
		Expect(err.Error()).To(Equal(plainErr.Error()))
		Expect(got).To(Equal(plain))
		Expect(events).To(HaveLen(1))
		Expect(events[0].Outcome).To(Equal(diagnostics.OutcomeError))
		Expect(events[0].Count).To(Equal(1))
	})
	It("adds no allocations for disabled timing on empty enumeration", func() {
		ml := &ModelLoader{}
		readDir := func(string) ([]os.DirEntry, error) { return nil, nil }
		Expect(testing.AllocsPerRun(100, func() { _, _ = ml.listFilesInModelPathContext(context.Background(), readDir) })).To(BeZero())
	})
})
