package nodes

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ParseSchedulingSeed", func() {
	It("parses inline JSON with static min/max replicas", func() {
		configs, err := ParseSchedulingSeed(`[{"model_name":"m","node_selector":{"tier":"gpu"},"min_replicas":1,"max_replicas":4}]`, "")
		Expect(err).ToNot(HaveOccurred())
		Expect(configs).To(HaveLen(1))
		Expect(configs[0].ModelName).To(Equal("m"))
		Expect(configs[0].MinReplicas).To(Equal(1))
		Expect(configs[0].MaxReplicas).To(Equal(4))
		Expect(configs[0].SpreadAll).To(BeFalse())
		Expect(configs[0].NodeSelector).To(Equal(`{"tier":"gpu"}`))
	})

	It("maps replicas: all to SpreadAll", func() {
		configs, err := ParseSchedulingSeed(`[{"model_name":"m","replicas":"all"}]`, "")
		Expect(err).ToNot(HaveOccurred())
		Expect(configs[0].SpreadAll).To(BeTrue())
	})

	It("maps replicas: true to SpreadAll", func() {
		configs, err := ParseSchedulingSeed(`[{"model_name":"m","replicas":true}]`, "")
		Expect(err).ToNot(HaveOccurred())
		Expect(configs[0].SpreadAll).To(BeTrue())
	})

	It("accepts the spread_all field directly", func() {
		configs, err := ParseSchedulingSeed(`[{"model_name":"m","spread_all":true}]`, "")
		Expect(err).ToNot(HaveOccurred())
		Expect(configs[0].SpreadAll).To(BeTrue())
	})

	It("rejects spread_all combined with min/max replicas", func() {
		_, err := ParseSchedulingSeed(`[{"model_name":"m","replicas":"all","min_replicas":2}]`, "")
		Expect(err).To(MatchError(ContainSubstring("mutually exclusive")))
	})

	It("rejects a missing model_name", func() {
		_, err := ParseSchedulingSeed(`[{"min_replicas":1}]`, "")
		Expect(err).To(MatchError(ContainSubstring("model_name is required")))
	})

	It("rejects a numeric replicas value pointing the user at min/max", func() {
		_, err := ParseSchedulingSeed(`[{"model_name":"m","replicas":3}]`, "")
		Expect(err).To(MatchError(ContainSubstring("min_replicas")))
	})

	It("returns no configs for empty input", func() {
		configs, err := ParseSchedulingSeed("", "")
		Expect(err).ToNot(HaveOccurred())
		Expect(configs).To(BeEmpty())
	})

	It("parses a YAML file with replicas: all and a node_selector", func() {
		dir := GinkgoT().TempDir()
		path := filepath.Join(dir, "scheduling.yaml")
		yaml := "- model_name: m\n  replicas: all\n  node_selector:\n    tier: gpu\n"
		Expect(os.WriteFile(path, []byte(yaml), 0o600)).To(Succeed())

		configs, err := ParseSchedulingSeed("", path)
		Expect(err).ToNot(HaveOccurred())
		Expect(configs).To(HaveLen(1))
		Expect(configs[0].ModelName).To(Equal("m"))
		Expect(configs[0].SpreadAll).To(BeTrue())
		Expect(configs[0].NodeSelector).To(Equal(`{"tier":"gpu"}`))
	})
})
