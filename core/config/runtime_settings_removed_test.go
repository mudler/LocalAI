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

	It("maps a stored localai_assistant_enabled=false to chats_localai_admin=false", func() {
		var st config.RuntimeSettings
		Expect(config.MapRemovedRuntimeSettings([]byte(`{"localai_assistant_enabled": false}`), &st)).NotTo(BeEmpty())
		Expect(st.ChatsLocalAIAdmin).NotTo(BeNil())
		Expect(*st.ChatsLocalAIAdmin).To(BeFalse())
	})

	It("does not map a true value, and an explicit chats_localai_admin wins", func() {
		var st config.RuntimeSettings
		config.MapRemovedRuntimeSettings([]byte(`{"localai_assistant_enabled": true}`), &st)
		Expect(st.ChatsLocalAIAdmin).To(BeNil())

		on := true
		st = config.RuntimeSettings{ChatsLocalAIAdmin: &on}
		config.MapRemovedRuntimeSettings([]byte(`{"localai_assistant_enabled": false, "chats_localai_admin": true}`), &st)
		Expect(*st.ChatsLocalAIAdmin).To(BeTrue())
	})
})
