// SPDX-License-Identifier: MIT
package galleryop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/diagnostics"
	"github.com/mudler/LocalAI/pkg/model"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type diagnosticDiscovery struct {
	ml            *model.ModelLoader
	lists, exists int
}

func (d *diagnosticDiscovery) ListFilesInModelPathContext(ctx context.Context) ([]string, error) {
	d.lists++
	return d.ml.ListFilesInModelPathContext(ctx)
}
func (d *diagnosticDiscovery) ExistsInModelPath(name string) bool {
	d.exists++
	return d.ml.ExistsInModelPath(name)
}

var _ = Describe("Diagnostics discovery helpers", func() {
	var bcl *config.ModelConfigLoader
	var ml *model.ModelLoader
	var ctx context.Context
	var events []diagnostics.Event
	BeforeEach(func() {
		dir := GinkgoT().TempDir()
		ml = &model.ModelLoader{ModelPath: dir}
		bcl = config.NewModelConfigLoader(dir)
		for _, name := range []string{"secret-candidate", "z-loose", "secret-weight.gguf"} {
			Expect(os.WriteFile(filepath.Join(dir, name), nil, 0600)).To(Succeed())
		}
		c := config.ModelConfig{Name: "secret-candidate"}
		c.Model = "secret-candidate"
		bcl.ReplaceModelConfigs([]config.ModelConfig{c})
		events = nil
		ctx = diagnostics.NewRecorder(func(e diagnostics.Event) { events = append(events, e) }).Request(context.Background())
	})
	It("preserves every policy, nil filters, duplicates, ordering and operation counts", func() {
		expected := [][]string{{"z-loose"}, {"secret-candidate", "z-loose"}, {"secret-candidate"}, {"secret-candidate", "secret-candidate", "z-loose"}}
		for policy := LOOSE_ONLY; policy <= ALWAYS_INCLUDE; policy++ {
			events = nil
			plainD, observedD := &diagnosticDiscovery{ml: ml}, &diagnosticDiscovery{ml: ml}
			plain, err := listModelsContext(context.Background(), bcl, plainD, nil, policy)
			Expect(err).NotTo(HaveOccurred())
			got, err := listModelsContext(ctx, bcl, observedD, nil, policy)
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(plain))
			Expect(got).To(Equal(expected[policy]))
			Expect(observedD.lists).To(Equal(plainD.lists))
			Expect(observedD.exists).To(BeZero())
			if policy == SKIP_ALWAYS {
				Expect(observedD.lists).To(BeZero())
			} else {
				Expect(observedD.lists).To(Equal(1))
			}
			var looseCounts []int
			fsCount := 0
			configCount := 0
			for _, e := range events {
				if e.State == diagnostics.StateEnd {
					switch e.Phase {
					case diagnostics.PhaseLooseFilter:
						looseCounts = append(looseCounts, e.Count)
					case diagnostics.PhaseFSEnumeration:
						fsCount += e.Count
					case diagnostics.PhaseConfigFilter:
						configCount++
					}
				}
				Expect(e.ID).To(Equal(events[0].ID))
				Expect(e.Kind).To(Equal(diagnostics.KindRequest))
			}
			Expect(configCount).To(Equal(1))
			if policy == SKIP_ALWAYS {
				Expect(looseCounts).To(Equal([]int{1}))
				Expect(fsCount).To(BeZero())
			} else {
				Expect(looseCounts).To(Equal([]int{1, 3, 2}))
				Expect(fsCount).To(Equal(1))
			}
			Expect(fmt.Sprint(events)).NotTo(ContainSubstring("secret"))
			Expect(fmt.Sprint(events)).NotTo(ContainSubstring(ml.ModelPath))
			n := len(events)
			wrapped, err := ListModels(bcl, ml, nil, policy)
			Expect(err).NotTo(HaveOccurred())
			Expect(wrapped).To(Equal(got))
			Expect(events).To(HaveLen(n))
			companion, err := ListModelsContext(ctx, bcl, ml, nil, policy)
			Expect(err).NotTo(HaveOccurred())
			Expect(companion).To(Equal(got))
		}
	})
	It("preserves invalid regex, early matches, weight fallback and negative fallback for all policies", func() {
		for policy := LOOSE_ONLY; policy <= ALWAYS_INCLUDE; policy++ {
			for _, name := range []string{"[secret-invalid", "z-loose", "secret-weight.gguf", "secret-absent"} {
				events = nil
				a, b := &diagnosticDiscovery{ml: ml}, &diagnosticDiscovery{ml: ml}
				plain, plainErr := checkIfModelExistsContext(context.Background(), bcl, a, name, policy)
				got, err := checkIfModelExistsContext(ctx, bcl, b, name, policy)
				Expect(got).To(Equal(plain))
				if plainErr != nil {
					Expect(err).To(Equal(plainErr))
				} else {
					Expect(err).NotTo(HaveOccurred())
				}
				Expect(b.lists).To(Equal(a.lists))
				Expect(b.exists).To(Equal(a.exists))
				if name == "[secret-invalid" {
					Expect(err).To(HaveOccurred())
					Expect(b.lists).To(BeZero())
					Expect(b.exists).To(BeZero())
					Expect(events).To(BeEmpty())
					continue
				}
				Expect(err).NotTo(HaveOccurred())
				Expect(got).To(Equal(name != "secret-absent"))
				if name == "secret-weight.gguf" || name == "secret-absent" || policy == SKIP_ALWAYS {
					Expect(b.exists).To(Equal(1))
				} else {
					Expect(b.exists).To(BeZero())
				}
				fallbackEnds := 0
				for _, e := range events {
					Expect(e.ID).To(Equal(events[0].ID))
					if e.Phase == diagnostics.PhaseExistenceFallback && e.State == diagnostics.StateEnd {
						fallbackEnds++
						Expect(e.Count).To(Equal(1))
						Expect(e.Outcome).To(Equal(diagnostics.OutcomeOK))
					}
				}
				Expect(fallbackEnds).To(Equal(b.exists))
				Expect(fmt.Sprint(events)).NotTo(ContainSubstring("secret"))
				n := len(events)
				wrapped, err := CheckIfModelExists(bcl, ml, name, policy)
				Expect(err).NotTo(HaveOccurred())
				Expect(wrapped).To(Equal(got))
				Expect(events).To(HaveLen(n))
				companion, err := CheckIfModelExistsContext(ctx, bcl, ml, name, policy)
				Expect(err).NotTo(HaveOccurred())
				Expect(companion).To(Equal(got))
			}
		}
	})
	It("propagates enumeration errors without fallback and skips enumeration only when requested", func() {
		ml.ModelPath = filepath.Join(ml.ModelPath, "secret-missing")
		d := &diagnosticDiscovery{ml: ml}
		found, err := checkIfModelExistsContext(ctx, bcl, d, "secret-absent", ALWAYS_INCLUDE)
		Expect(found).To(BeFalse())
		Expect(os.IsNotExist(err)).To(BeTrue())
		Expect(d.lists).To(Equal(1))
		Expect(d.exists).To(BeZero())
		Expect(events[len(events)-1].Outcome).To(Equal(diagnostics.OutcomeError))
		got, err := ListModelsContext(ctx, bcl, ml, nil, SKIP_ALWAYS)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal([]string{"secret-candidate"}))
	})
	It("retains canceled-context behavior and filter invocation counts", func() {
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		calls := 0
		filter := func(_ string, _ *config.ModelConfig) bool { calls++; return true }
		plain, err := ListModels(bcl, ml, filter, ALWAYS_INCLUDE)
		Expect(err).NotTo(HaveOccurred())
		before := calls
		got, err := ListModelsContext(canceled, bcl, ml, filter, ALWAYS_INCLUDE)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(plain))
		Expect(calls).To(Equal(before * 2))
	})
})
