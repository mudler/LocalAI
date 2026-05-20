package config

import (
	"slices"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("BackendCapabilities", func() {
	It("every backend declares possible/default usecases and gRPC methods", func() {
		for name, cap := range BackendCapabilities {
			Expect(cap.PossibleUsecases).NotTo(BeEmpty(), "backend %q has no possible usecases", name)
			Expect(cap.DefaultUsecases).NotTo(BeEmpty(), "backend %q has no default usecases", name)
			Expect(cap.GRPCMethods).NotTo(BeEmpty(), "backend %q has no gRPC methods", name)
		}
	})

	It("default usecases are a subset of possible usecases", func() {
		for name, cap := range BackendCapabilities {
			for _, d := range cap.DefaultUsecases {
				Expect(cap.PossibleUsecases).To(ContainElement(d), "backend %q: default %q not in possible %v", name, d, cap.PossibleUsecases)
			}
		}
	})

	It("every backend's possible usecases map to a known FLAG_*", func() {
		allFlags := GetAllModelConfigUsecases()
		for name, cap := range BackendCapabilities {
			for _, u := range cap.PossibleUsecases {
				info, ok := UsecaseInfoMap[u]
				Expect(ok).To(BeTrue(), "backend %q: usecase %q not in UsecaseInfoMap", name, u)
				flagName := "FLAG_" + strings.ToUpper(u)
				if _, ok := allFlags[flagName]; ok {
					continue
				}
				// Some usecase names don't transform exactly to FLAG_<UPPER>; fall back to flag value lookup.
				found := false
				for _, flag := range allFlags {
					if flag == info.Flag {
						found = true
						break
					}
				}
				Expect(found).To(BeTrue(), "backend %q: usecase %q flag %d not in GetAllModelConfigUsecases", name, u, info.Flag)
			}
		}
	})

	It("every UsecaseInfoMap entry has a non-zero flag and a gRPC method", func() {
		for name, info := range UsecaseInfoMap {
			Expect(info.Flag).NotTo(Equal(FLAG_ANY), "usecase %q has FLAG_ANY (zero) — should have a real flag", name)
			Expect(info.GRPCMethod).NotTo(BeEmpty(), "usecase %q has no gRPC method", name)
		}
	})
})

var _ = Describe("GetBackendCapability", func() {
	It("returns the capability for a known backend", func() {
		cap := GetBackendCapability("llama-cpp")
		Expect(cap).NotTo(BeNil())
		Expect(cap.PossibleUsecases).To(ContainElement("chat"))
	})

	It("normalizes hyphenated names so llama.cpp resolves to llama-cpp", func() {
		Expect(GetBackendCapability("llama.cpp")).NotTo(BeNil())
	})

	It("returns nil for unknown backends", func() {
		Expect(GetBackendCapability("nonexistent")).To(BeNil())
	})
})

var _ = Describe("IsValidUsecaseForBackend", func() {
	It("accepts a backend's declared usecases", func() {
		Expect(IsValidUsecaseForBackend("piper", "tts")).To(BeTrue())
	})

	It("rejects usecases outside a backend's possible set", func() {
		Expect(IsValidUsecaseForBackend("piper", "chat")).To(BeFalse())
	})

	It("is permissive for unknown backends", func() {
		Expect(IsValidUsecaseForBackend("unknown", "anything")).To(BeTrue())
	})
})

var _ = Describe("AllBackendNames", func() {
	It("returns 30+ backends in sorted order", func() {
		names := AllBackendNames()
		Expect(len(names)).To(BeNumerically(">=", 30))
		Expect(slices.IsSorted(names)).To(BeTrue())
	})
})
