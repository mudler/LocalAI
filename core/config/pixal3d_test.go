package config

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Pixal3D discovery", func() {
	It("advertises four views and explicit mesh scale", func() {
		c := &ModelConfig{Backend: "pixal3dcpp"}
		Expect(c.HasUsecases(FLAG_3D)).To(BeTrue())
		ops := c.ThreeDOperations()
		Expect(ops).To(HaveLen(1))
		Expect(ops[0].Inputs).To(HaveLen(4))
		Expect(ops[0].Parameters[0].Name).To(Equal("mesh_scale"))
	})
})
