package config_test

import (
	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// chats_host_exec says who gets the chats' built-in bash on this machine, and
// chats_tool_providers which add-on serves a tool that several replace. Both
// apply at the next turn.
var _ = Describe("chats_host_exec and chats_tool_providers", func() {
	It("round-trips the mode and the pins", func() {
		o := config.NewApplicationConfig()
		Expect(o.ChatsHostExec).To(BeEmpty(), "empty is the default: off with accounts, ask without")
		mode := "ask"
		pins := map[string]string{"bash": "sandbox"}
		o.ApplyRuntimeSettings(&config.RuntimeSettings{ChatsHostExec: &mode, ChatsToolProviders: &pins})
		Expect(o.ChatsHostExec).To(Equal("ask"))
		Expect(o.ChatsToolProviders).To(Equal(map[string]string{"bash": "sandbox"}))
		rs := o.ToRuntimeSettings()
		Expect(*rs.ChatsHostExec).To(Equal("ask"))
		Expect(*rs.ChatsToolProviders).To(Equal(map[string]string{"bash": "sandbox"}))
		pins["bash"] = "other"
		Expect(o.ChatsToolProviders["bash"]).To(Equal("sandbox"), "the setting keeps its own copy")
	})
})
