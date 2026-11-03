package config_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
)

// chats_date_note starts the note of every chat turn with today's date. The
// chats read it at each turn.
var _ = Describe("chats date note setting", func() {
	It("is off by default", func() {
		o := config.NewApplicationConfig()
		Expect(o.ChatsDateNote).To(BeFalse())
		Expect(*o.ToRuntimeSettings().ChatsDateNote).To(BeFalse())
	})

	It("is on with the option, and switches off and on again at runtime", func() {
		o := config.NewApplicationConfig(config.WithChatsDateNote(true))
		Expect(o.ChatsDateNote).To(BeTrue())
		off, on := false, true
		o.ApplyRuntimeSettings(&config.RuntimeSettings{ChatsDateNote: &off})
		Expect(o.ChatsDateNote).To(BeFalse())
		Expect(*o.ToRuntimeSettings().ChatsDateNote).To(BeFalse())
		o.ApplyRuntimeSettings(&config.RuntimeSettings{ChatsDateNote: &on})
		Expect(o.ChatsDateNote).To(BeTrue())
	})
})
