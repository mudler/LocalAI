// SPDX-License-Identifier: MIT
package config

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("3D animation capabilities", func() {
	It("keeps animation distinct from image-conditioned mesh generation", func() {
		cfg := &ModelConfig{Backend: "kimodocpp"}
		Expect(cfg.HasUsecases(FLAG_3D_ANIMATION)).To(BeTrue())
		Expect(cfg.HasUsecases(FLAG_3D)).To(BeFalse())
		Expect(cfg.Capabilities()).To(ContainElement("3d_animation"))
		Expect(cfg.InputModalities()).To(Equal([]string{"text"}))
		Expect(cfg.OutputModalities()).To(Equal([]string{"3d"}))
		Expect((&ModelConfig{Backend: "llama-cpp"}).HasUsecases(FLAG_3D_ANIMATION)).To(BeFalse())
	})
	It("advertises effective model defaults without sharing mutable descriptors", func() {
		cfg := &ModelConfig{Backend: "kimodocpp", Options: []string{"steps:50"}}
		operations := cfg.ThreeDOperations()
		Expect(operations).To(HaveLen(1))
		Expect(operations[0].Endpoint).To(Equal("/3d/animate"))
		Expect(operations[0].Parameters[1].Default).To(Equal("50"))
		operations[0].Parameters[1].Default = "1"
		Expect(cfg.ThreeDOperations()[0].Parameters[1].Default).To(Equal("50"))
		Expect((&ModelConfig{Backend: "trellis2cpp"}).ThreeDOperations()).To(HaveLen(2))
	})
	It("rejects invalid configured sampling defaults", func() {
		cfg := &ModelConfig{Backend: "kimodocpp", Options: []string{"steps:NaN"}}
		_, err := cfg.Validate()
		Expect(err).To(MatchError(ContainSubstring("default")))
	})
})
