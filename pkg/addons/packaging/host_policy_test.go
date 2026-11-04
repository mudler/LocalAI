// SPDX-License-Identifier: MIT
package packaging_test

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("official host policy deployment", func() {
	It("loads exactly the historical grants through the actual host loader", func() {
		root, err := filepath.Abs(filepath.Join("..", "..", ".."))
		Expect(err).NotTo(HaveOccurred())
		grants, err := config.LoadAddonHostPolicy(filepath.Join(root, "deploy", "addons", "host-policy.json"))
		Expect(err).NotTo(HaveOccurred())
		// Deployment-owned exception: historical identity grants, not core defaults.
		legacy := "fine-tuning=finetune.,quantization=quantize.,sandbox=workspace.,sandbox=egress.,office=workspace.,web=workspace.,telegram=connector.,slack=connector."
		want, err := config.ParseAddonHostOps(legacy)
		Expect(err).NotTo(HaveOccurred())
		Expect(grants).To(Equal(want))
	})
	It("explicitly selects the shipped policy in the final Docker runtime stage", func() {
		data, err := os.ReadFile(filepath.Join("..", "..", "..", "Dockerfile"))
		Expect(err).NotTo(HaveOccurred())
		start := strings.LastIndex(string(data), "\nFROM ")
		Expect(start).To(BeNumerically(">=", 0))
		runtime := string(data[start:])
		Expect(runtime).To(ContainSubstring("COPY deploy/addons/host-policy.json /etc/localai/addons/host-policy.json"))
		Expect(runtime).To(ContainSubstring("ENV LOCALAI_ADDON_HOST_POLICY=/etc/localai/addons/host-policy.json"))
	})
})
