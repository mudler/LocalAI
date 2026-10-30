package config_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
)

// chats_suggestions is the server's default for offers from the assistant in
// the chats. The chats read it at each turn.
var _ = Describe("chats suggestions setting", func() {
	It("is on by default", func() {
		o := config.NewApplicationConfig()
		Expect(o.DisableChatSuggestions).To(BeFalse())
		Expect(*o.ToRuntimeSettings().ChatsSuggestions).To(BeTrue())
	})

	It("switches off and on again at runtime", func() {
		o := config.NewApplicationConfig()
		off, on := false, true
		o.ApplyRuntimeSettings(&config.RuntimeSettings{ChatsSuggestions: &off})
		Expect(o.DisableChatSuggestions).To(BeTrue())
		Expect(*o.ToRuntimeSettings().ChatsSuggestions).To(BeFalse())
		o.ApplyRuntimeSettings(&config.RuntimeSettings{ChatsSuggestions: &on})
		Expect(o.DisableChatSuggestions).To(BeFalse())
	})
})
