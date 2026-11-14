package config

import (
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("remote backend registry", func() {
	It("describes cloud-proxy", func() {
		b, ok := RemoteBackendFor("cloud-proxy")
		Expect(ok).To(BeTrue())
		Expect(b).To(Equal(RemoteBackend{
			Name:                  "cloud-proxy",
			Label:                 "Cloud proxy",
			PIIDefault:            true,
			KnownUsecasesRequired: false,
			UpstreamURLShape:      UpstreamURLFullEndpoint,
			WarmApplies:           false,
		}))
	})

	It("describes localai-proxy", func() {
		b, ok := RemoteBackendFor("localai-proxy")
		Expect(ok).To(BeTrue())
		Expect(b).To(Equal(RemoteBackend{
			Name:                  "localai-proxy",
			Label:                 "LocalAI proxy",
			PIIDefault:            false,
			KnownUsecasesRequired: true,
			UpstreamURLShape:      UpstreamURLServerRoot,
			WarmApplies:           false,
		}))
	})

	It("does not know local backends", func() {
		for _, name := range []string{"llama-cpp", "vllm", ""} {
			_, ok := RemoteBackendFor(name)
			Expect(ok).To(BeFalse(), name)
			Expect(IsRemoteBackend(name)).To(BeFalse(), name)
		}
	})

	It("lists entries sorted by name", func() {
		var names []string
		for _, b := range RemoteBackends() {
			names = append(names, b.Name)
		}
		Expect(names).To(Equal([]string{"cloud-proxy", "localai-proxy"}))
		Expect(slices.IsSorted(names)).To(BeTrue())
	})

	It("returns a copy of the registry", func() {
		list := RemoteBackends()
		list[0].Label = "changed"
		list[0].PIIDefault = false
		list = append(list[:0], list[1:]...)
		_ = list

		b, _ := RemoteBackendFor("cloud-proxy")
		Expect(b.Label).To(Equal("Cloud proxy"))
		Expect(b.PIIDefault).To(BeTrue())
		Expect(RemoteBackends()).To(HaveLen(2))
	})

	It("reports IsRemoteProxy for both remote backends only", func() {
		Expect(ModelConfig{Backend: "cloud-proxy"}.IsRemoteProxy()).To(BeTrue())
		Expect(ModelConfig{Backend: "localai-proxy"}.IsRemoteProxy()).To(BeTrue())
		Expect(ModelConfig{Backend: "llama-cpp"}.IsRemoteProxy()).To(BeFalse())
		Expect(ModelConfig{}.IsRemoteProxy()).To(BeFalse())
	})

	It("lists cloud-proxy as the only PII default backend", func() {
		Expect(PIIDefaultBackends()).To(Equal([]string{"cloud-proxy"}))
		Expect(PIIDefaultFor("cloud-proxy")).To(BeTrue())
		Expect(PIIDefaultFor("localai-proxy")).To(BeFalse())
		Expect(PIIDefaultFor("llama-cpp")).To(BeFalse())
	})

	It("keeps the PII default of a model on the registry", func() {
		cloud := ModelConfig{Backend: "cloud-proxy"}
		Expect(cloud.PIIIsEnabled()).To(BeTrue())
		Expect(cloud.PIIFilterApplies()).To(BeTrue())
		peer := ModelConfig{Backend: "localai-proxy"}
		Expect(peer.PIIIsEnabled()).To(BeFalse())
		Expect((&ModelConfig{Backend: "llama-cpp"}).PIIIsEnabled()).To(BeFalse())
	})

	// Only localai-proxy registers a defaults hook today; cloud-proxy needs
	// none. This pins that fact so a new registry entry makes a choice.
	It("registers a defaults hook for localai-proxy", func() {
		Expect(backendHooks).To(HaveKey("localai-proxy"))
		Expect(backendHooks).NotTo(HaveKey("cloud-proxy"))
	})
})
