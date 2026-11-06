package config_test

import (
	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("removed runtime settings", func() {
	It("names a stored localai_assistant_enabled, and nothing else", func() {
		Expect(config.RemovedRuntimeSettings([]byte(`{"localai_assistant_enabled": false, "watchdog_enabled": true}`))).To(Equal([]string{"localai_assistant_enabled"}))
		Expect(config.RemovedRuntimeSettings([]byte(`{"watchdog_enabled": true}`))).To(BeEmpty())
		Expect(config.RemovedRuntimeSettings([]byte(`not json`))).To(BeEmpty())
	})

	It("is ignored: the value switches nothing", func() {
		o := &config.ApplicationConfig{}
		Expect(config.WarnRemovedRuntimeSettings([]byte(`{"localai_assistant_enabled": false}`))).NotTo(BeEmpty())
		Expect(o.DisableChatsLocalAIAdmin).To(BeFalse())
	})
})
