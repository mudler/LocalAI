package config

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("PipelineVoiceRecognition", func() {
	Describe("Normalize", func() {
		It("fills defaults for empty fields", func() {
			v := PipelineVoiceRecognition{Model: "spk"}
			v.Normalize()
			Expect(v.Mode).To(Equal(VoiceGateModeIdentify))
			Expect(v.When).To(Equal(VoiceGateWhenEvery))
			Expect(v.OnReject).To(Equal(VoiceGateRejectEvent))
			Expect(v.Threshold).To(BeNumerically("~", defaultVoiceGateThreshold, 1e-6))
		})
		It("keeps explicit values", func() {
			v := PipelineVoiceRecognition{Model: "spk", Mode: VoiceGateModeVerify, When: VoiceGateWhenFirst, OnReject: VoiceGateRejectSilent, Threshold: 0.4}
			v.Normalize()
			Expect(v.Mode).To(Equal(VoiceGateModeVerify))
			Expect(v.When).To(Equal(VoiceGateWhenFirst))
			Expect(v.OnReject).To(Equal(VoiceGateRejectSilent))
			Expect(v.Threshold).To(BeNumerically("~", 0.4, 1e-6))
		})
	})

	Describe("Validate", func() {
		It("requires a registry for identify mode", func() {
			v := PipelineVoiceRecognition{Model: "spk", Mode: VoiceGateModeIdentify}
			Expect(v.Validate(false)).To(HaveOccurred())
			Expect(v.Validate(true)).ToNot(HaveOccurred())
		})
		It("requires references for verify mode", func() {
			v := PipelineVoiceRecognition{Model: "spk", Mode: VoiceGateModeVerify}
			Expect(v.Validate(false)).To(HaveOccurred())
			v.References = []VoiceReference{{Name: "a", Audio: "/a.wav"}}
			Expect(v.Validate(false)).ToNot(HaveOccurred())
		})
		It("rejects a reference with no audio path", func() {
			v := PipelineVoiceRecognition{Model: "spk", Mode: VoiceGateModeVerify, References: []VoiceReference{{Name: "a"}}}
			Expect(v.Validate(false)).To(HaveOccurred())
		})
		It("rejects unknown enum values", func() {
			Expect((PipelineVoiceRecognition{Model: "spk", Mode: "bogus"}).Validate(true)).To(HaveOccurred())
			Expect((PipelineVoiceRecognition{Model: "spk", Mode: VoiceGateModeIdentify, When: "bogus"}).Validate(true)).To(HaveOccurred())
			Expect((PipelineVoiceRecognition{Model: "spk", Mode: VoiceGateModeIdentify, OnReject: "bogus"}).Validate(true)).To(HaveOccurred())
		})
	})

	Describe("VoiceGateEnabled", func() {
		It("is false when block absent", func() {
			Expect((Pipeline{}).VoiceGateEnabled()).To(BeFalse())
		})
		It("is true when a model is set", func() {
			Expect((Pipeline{VoiceRecognition: &PipelineVoiceRecognition{Model: "spk"}}).VoiceGateEnabled()).To(BeTrue())
		})
	})
})
