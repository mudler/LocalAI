// SPDX-License-Identifier: MIT
package galleryop

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/model"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("WarmFastPath request existence", func() {
	var bcl *config.ModelConfigLoader
	var ml *model.ModelLoader
	BeforeEach(func() {
		dir := GinkgoT().TempDir()
		bcl = config.NewModelConfigLoader(dir)
		ml = &model.ModelLoader{ModelPath: dir}
		bcl.ReplaceModelConfigs([]config.ModelConfig{{Name: "target"}, {Name: "default", Alias: "target"}, {Name: "literal["}})
		for i := 0; i < 128; i++ {
			Expect(os.WriteFile(filepath.Join(dir, fmt.Sprintf("unrelated-%03d", i)), nil, 0600)).To(Succeed())
		}
		Expect(os.WriteFile(filepath.Join(dir, "raw.gguf"), nil, 0600)).To(Succeed())
	})
	It("does no discovery for exact alias, concrete or literal regex names", func() {
		for _, name := range []string{"default", "target", "literal["} {
			d := &diagnosticDiscovery{ml: ml}
			exists, err := checkRequestModelExistsContext(context.Background(), bcl, d, name)
			Expect(err).NotTo(HaveOccurred())
			Expect(exists).To(BeTrue())
			Expect(d.lists).To(BeZero(), name)
			Expect(d.exists).To(BeZero(), name)
		}
	})
	It("keeps missing, regex, loose-file, raw-file and malformed regex behavior", func() {
		for _, name := range []string{"missing", "tar.*", "unrelated-001", "raw.gguf", "absent["} {
			old, next := &diagnosticDiscovery{ml: ml}, &diagnosticDiscovery{ml: ml}
			want, wantErr := checkIfModelExistsContext(context.Background(), bcl, old, name, ALWAYS_INCLUDE)
			got, err := checkRequestModelExistsContext(context.Background(), bcl, next, name)
			Expect(got).To(Equal(want))
			Expect(fmt.Sprint(err)).To(Equal(fmt.Sprint(wantErr)))
			Expect(next.lists).To(Equal(old.lists))
			Expect(next.exists).To(Equal(old.exists))
		}
	})
	It("ignores unrelated directory failure only for exact configured requests", func() {
		ml.ModelPath = filepath.Join(ml.ModelPath, "missing-directory")
		d := &diagnosticDiscovery{ml: ml}
		exists, err := checkRequestModelExistsContext(context.Background(), bcl, d, "target")
		Expect(err).NotTo(HaveOccurred())
		Expect(exists).To(BeTrue())
		Expect(d.lists).To(BeZero())
		for _, name := range []string{"missing", "tar.*", "raw.gguf"} {
			exists, err = checkRequestModelExistsContext(context.Background(), bcl, d, name)
			Expect(err).To(HaveOccurred())
			Expect(exists).To(BeFalse())
		}
		Expect(d.lists).To(Equal(3))
		Expect(d.exists).To(BeZero())
	})
	It("leaves general regex existence and full listing unchanged", func() {
		d := &diagnosticDiscovery{ml: ml}
		names, err := listModelsContext(context.Background(), bcl, d, nil, ALWAYS_INCLUDE)
		Expect(err).NotTo(HaveOccurred())
		Expect(names).To(HaveLen(131))
		Expect(names).To(ContainElements("target", "default", "unrelated-001"))
		exists, err := checkIfModelExistsContext(context.Background(), bcl, d, "tar.*", ALWAYS_INCLUDE)
		Expect(err).NotTo(HaveOccurred())
		Expect(exists).To(BeTrue())
		Expect(d.lists).To(Equal(2))
	})
	It("observes replacement and deletion without a stale successful hit", func() {
		d := &diagnosticDiscovery{ml: ml}
		exists, err := checkRequestModelExistsContext(context.Background(), bcl, d, "target")
		Expect(err).NotTo(HaveOccurred())
		Expect(exists).To(BeTrue())
		Expect(d.lists).To(BeZero())
		bcl.ReplaceModelConfigs([]config.ModelConfig{{Name: "replacement"}})
		exists, err = checkRequestModelExistsContext(context.Background(), bcl, d, "target")
		Expect(err).NotTo(HaveOccurred())
		Expect(exists).To(BeFalse())
		Expect(d.lists).To(Equal(1))
		exists, err = checkRequestModelExistsContext(context.Background(), bcl, d, "replacement")
		Expect(err).NotTo(HaveOccurred())
		Expect(exists).To(BeTrue())
		Expect(d.lists).To(Equal(1))
		bcl.RemoveModelConfig("replacement")
		exists, err = checkRequestModelExistsContext(context.Background(), bcl, d, "replacement")
		Expect(err).NotTo(HaveOccurred())
		Expect(exists).To(BeFalse())
		Expect(d.lists).To(Equal(2))
	})
})
