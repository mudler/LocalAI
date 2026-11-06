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

	It("lets an explicit stored chats_localai_admin win over the deprecated flag", func() {
		cfg := &config.ApplicationConfig{DynamicConfigsDir: seedSettings(`{"chats_localai_admin": true}`)}
		config.DisableLocalAIAdminByDeprecatedOption(cfg)
		loadRuntimeSettingsFromFile(cfg)
		Expect(cfg.DisableChatsLocalAIAdmin).To(BeFalse())
	})
})
