package application

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
)

var _ = Describe("the deprecated assistant switch at startup", func() {
	It("maps a stored localai_assistant_enabled=false to the admin group off", func() {
		cfg := &config.ApplicationConfig{DynamicConfigsDir: seedSettings(`{"localai_assistant_enabled": false}`)}
		loadRuntimeSettingsFromFile(cfg)
		Expect(cfg.DisableChatsLocalAIAdmin).To(BeTrue())
	})

	It("leaves the admin group on for a stored localai_assistant_enabled=true", func() {
		cfg := &config.ApplicationConfig{DynamicConfigsDir: seedSettings(`{"localai_assistant_enabled": true}`)}
		loadRuntimeSettingsFromFile(cfg)
		Expect(cfg.DisableChatsLocalAIAdmin).To(BeFalse())
	})

	It("lets an explicit stored chats_localai_admin win over the old stored value", func() {
		cfg := &config.ApplicationConfig{DynamicConfigsDir: seedSettings(`{"localai_assistant_enabled": false, "chats_localai_admin": true}`)}
		loadRuntimeSettingsFromFile(cfg)
		Expect(cfg.DisableChatsLocalAIAdmin).To(BeFalse())
	})

	It("keeps the admin group off for the deprecated flag or variable", func() {
		cfg := &config.ApplicationConfig{DynamicConfigsDir: seedSettings(`{"watchdog_enabled": true}`)}
		config.DisableLocalAIAdminByDeprecatedOption(cfg)
		loadRuntimeSettingsFromFile(cfg)
		Expect(cfg.DisableChatsLocalAIAdmin).To(BeTrue())
	})

	It("keeps an explicit disable by the deprecated flag over a stored chats_localai_admin=true", func() {
		cfg := &config.ApplicationConfig{DynamicConfigsDir: seedSettings(`{"chats_localai_admin": true}`)}
		config.DisableLocalAIAdminByDeprecatedOption(cfg)
		loadRuntimeSettingsFromFile(cfg)
		Expect(cfg.DisableChatsLocalAIAdmin).To(BeTrue())
	})

	It("keeps that disable after a runtime settings reload or change", func() {
		on := true
		cfg := &config.ApplicationConfig{}
		config.DisableLocalAIAdminByDeprecatedOption(cfg)
		cfg.ApplyRuntimeSettings(&config.RuntimeSettings{ChatsLocalAIAdmin: &on})
		Expect(cfg.DisableChatsLocalAIAdmin).To(BeTrue(), "a posted setting")
		cfg.ApplyRuntimeSettingsAtStartup(&config.RuntimeSettings{ChatsLocalAIAdmin: &on})
		Expect(cfg.DisableChatsLocalAIAdmin).To(BeTrue(), "a reload from the file")
	})

	It("lets chats_localai_admin change freely when the flag is not set", func() {
		on, off := true, false
		cfg := &config.ApplicationConfig{}
		cfg.ApplyRuntimeSettings(&config.RuntimeSettings{ChatsLocalAIAdmin: &off})
		Expect(cfg.DisableChatsLocalAIAdmin).To(BeTrue())
		cfg.ApplyRuntimeSettings(&config.RuntimeSettings{ChatsLocalAIAdmin: &on})
		Expect(cfg.DisableChatsLocalAIAdmin).To(BeFalse())
	})
})
