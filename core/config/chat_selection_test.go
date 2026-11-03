// SPDX-License-Identifier: MIT
package config_test

import (
	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("current chat selection", func() {
	It("clones pins and applies current pin and host mode settings", func() {
		o := config.NewApplicationConfig()
		a := map[string]string{"bash": "alpha"}
		o.ApplyRuntimeSettings(&config.RuntimeSettings{ChatsToolProviders: &a})
		first := o.ChatSelectionSnapshot()
		a["bash"] = "caller"
		first.ToolProviders["bash"] = "consumer"
		Expect(o.ChatSelectionSnapshot().ToolProviders["bash"]).To(Equal("alpha"))
		b := map[string]string{"bash": "beta"}
		o.ApplyRuntimeSettings(&config.RuntimeSettings{ChatsToolProviders: &b})
		Expect(o.ChatSelectionSnapshot().ToolProviders["bash"]).To(Equal("beta"))
		a["bash"] = "alpha"
		o.ApplyRuntimeSettings(&config.RuntimeSettings{ChatsToolProviders: &a})
		o.ApplyRuntimeSettings(&config.RuntimeSettings{ChatsToolProviders: &a})
		mode := o.ChatSelectionSnapshot().HostExec
		Expect(o.ChatSelectionSnapshot().ToolProviders["bash"]).To(Equal("alpha"))
		config.WithChatsHostExec("different")(o)
		Expect(o.ChatSelectionSnapshot().HostExec).To(Equal("different"))
		config.WithChatsHostExec(mode)(o)
		Expect(o.ChatSelectionSnapshot().HostExec).To(Equal(mode))
	})
	It("captures legacy startup values and routes startup settings through mutations", func() {
		o := &config.ApplicationConfig{ChatsHostExec: "ask", ChatsToolProviders: map[string]string{"bash": "first"}}
		initial := o.ChatSelectionSnapshot()
		Expect(initial.HostExec).To(Equal("ask"))
		Expect(initial.ToolProviders["bash"]).To(Equal("first"))
		pins := map[string]string{"bash": "second"}
		// Startup preserves explicit legacy inputs over persisted settings.
		o.ApplyRuntimeSettingsAtStartup(&config.RuntimeSettings{ChatsToolProviders: &pins})
		Expect(o.ChatSelectionSnapshot().ToolProviders["bash"]).To(Equal("first"))
		o = config.NewApplicationConfig()
		o.ApplyRuntimeSettingsAtStartup(&config.RuntimeSettings{ChatsToolProviders: &pins})
		Expect(o.ChatSelectionSnapshot().ToolProviders["bash"]).To(Equal("second"))
	})

})
