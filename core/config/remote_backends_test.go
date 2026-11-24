package config

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("remote backend registry", func() {
	It("describes both proxy backends", func() {
		b, ok := RemoteBackendFor("cloud-proxy")
		Expect(ok).To(BeTrue())
		Expect(b).To(Equal(RemoteBackend{Name: "cloud-proxy", PIIDefault: true}))
		b, ok = RemoteBackendFor("localai-proxy")
		Expect(ok).To(BeTrue())
		Expect(b).To(Equal(RemoteBackend{Name: "localai-proxy", PIIDefault: false}))
	})

	It("does not know local backends", func() {
		for _, name := range []string{"llama-cpp", "vllm", ""} {
			_, ok := RemoteBackendFor(name)
			Expect(ok).To(BeFalse(), name)
			Expect(IsRemoteBackend(name)).To(BeFalse(), name)
		}
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
