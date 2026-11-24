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
		It("accepts a zero (unset) threshold", func() {
			v := PipelineVoiceRecognition{Model: "spk", Mode: VoiceGateModeIdentify, Threshold: 0}
			Expect(v.Validate(true)).ToNot(HaveOccurred())
		})
		It("rejects an out-of-range threshold", func() {
			Expect((PipelineVoiceRecognition{Model: "spk", Mode: VoiceGateModeIdentify, Threshold: 5}).Validate(true)).To(HaveOccurred())
			Expect((PipelineVoiceRecognition{Model: "spk", Mode: VoiceGateModeIdentify, Threshold: -1}).Validate(true)).To(HaveOccurred())
		})
		It("rejects an empty model", func() {
			Expect((PipelineVoiceRecognition{Mode: VoiceGateModeIdentify}).Validate(true)).To(HaveOccurred())
		})
	})

	Describe("VoiceGateEnabled", func() {
		It("is false when block absent", func() {
			Expect((Pipeline{}).VoiceGateEnabled()).To(BeFalse())
		})
		It("is true when a model is set", func() {
			Expect((Pipeline{VoiceRecognition: &PipelineVoiceRecognition{Model: "spk"}}).VoiceGateEnabled()).To(BeTrue())
		})
		It("is true when the block is present even without a model (fails closed downstream)", func() {
			Expect((Pipeline{VoiceRecognition: &PipelineVoiceRecognition{}}).VoiceGateEnabled()).To(BeTrue())
		})
	})

	Describe("Enforce / Identity helpers", func() {
		It("treats a nil Enforce as enforcing (backward compatible)", func() {
			v := PipelineVoiceRecognition{Model: "spk"}
			Expect(v.EnforceGate()).To(BeTrue())
		})
		It("honors an explicit enforce:false", func() {
			off := false
			v := PipelineVoiceRecognition{Model: "spk", Enforce: &off}
			Expect(v.EnforceGate()).To(BeFalse())
		})
		It("reports identity disabled when no identity block is set", func() {
			v := PipelineVoiceRecognition{Model: "spk"}
			Expect(v.IdentityEnabled()).To(BeFalse())
			Expect(v.AnnounceEnabled()).To(BeFalse())
			Expect(v.PersonalizeEnabled()).To(BeFalse())
		})
		It("reports identity enabled when announce or personalize is on", func() {
			v := PipelineVoiceRecognition{Model: "spk", Identity: &VoiceIdentityConfig{Announce: true}}
			Expect(v.IdentityEnabled()).To(BeTrue())
			Expect(v.AnnounceEnabled()).To(BeTrue())
			Expect(v.PersonalizeEnabled()).To(BeFalse())

			v2 := PipelineVoiceRecognition{Model: "spk", Identity: &VoiceIdentityConfig{Personalize: true}}
			Expect(v2.IdentityEnabled()).To(BeTrue())
			Expect(v2.PersonalizeEnabled()).To(BeTrue())
		})
	})
})
