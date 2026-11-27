package model

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("withNodeEnv", func() {
	It("sets the node entries over inherited and model entries", func() {
		env := withNodeEnv([]string{
			"PATH=/bin",
			"LOCALAI_PROMPT_CACHE_ROOT=/inherited",
			"LOCALAI_PROMPT_CACHE_READ_MBPS=1",
		}, map[string]string{
			"LOCALAI_PROMPT_CACHE_ROOT":      "/data/prompt-cache",
			"LOCALAI_PROMPT_CACHE_READ_MBPS": "",
		})
		Expect(env).To(Equal([]string{"PATH=/bin", "LOCALAI_PROMPT_CACHE_ROOT=/data/prompt-cache"}))
	})

	It("removes a key whose value is empty, so the node alone turns a feature on", func() {
		env := withNodeEnv([]string{"LOCALAI_PROMPT_CACHE_ROOT=/inherited"}, map[string]string{"LOCALAI_PROMPT_CACHE_ROOT": ""})
		Expect(env).To(BeEmpty())
	})

	It("leaves the environment alone without node entries", func() {
		Expect(withNodeEnv([]string{"A=b"}, nil)).To(Equal([]string{"A=b"}))
	})

	It("reads the provider at each call", func() {
		ml := &ModelLoader{}
		Expect(ml.nodeBackendEnv()).To(BeNil())
		v := "/a"
		ml.SetBackendEnv(func() map[string]string { return map[string]string{"K": v} })
		Expect(ml.nodeBackendEnv()).To(HaveKeyWithValue("K", "/a"))
		v = "/b"
		Expect(ml.nodeBackendEnv()).To(HaveKeyWithValue("K", "/b"))
	})
})
