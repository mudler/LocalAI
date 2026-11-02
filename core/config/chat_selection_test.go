// SPDX-License-Identifier: MIT
package config_test

import (
	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sync"
)

var _ = Describe("chat selection epochs", func() {
	It("clones pins and detects pin and host mode ABA only on mutation", func() {
		o := config.NewApplicationConfig()
		a := map[string]string{"bash": "alpha"}
		o.ApplyRuntimeSettings(&config.RuntimeSettings{ChatsToolProviders: &a})
		first := o.ChatSelectionSnapshot()
		a["bash"] = "caller"
		first.ToolProviders["bash"] = "consumer"
		Expect(o.ChatSelectionSnapshot().ToolProviders["bash"]).To(Equal("alpha"))
		epoch := o.ChatSelectionSnapshot().Epoch
		b := map[string]string{"bash": "beta"}
		o.ApplyRuntimeSettings(&config.RuntimeSettings{ChatsToolProviders: &b})
		a["bash"] = "alpha"
		o.ApplyRuntimeSettings(&config.RuntimeSettings{ChatsToolProviders: &a})
		Expect(o.ChatSelectionSnapshot().Epoch).To(Equal(epoch + 2))
		o.ApplyRuntimeSettings(&config.RuntimeSettings{ChatsToolProviders: &a})
		Expect(o.ChatSelectionSnapshot().Epoch).To(Equal(epoch + 2))
		mode := o.ChatSelectionSnapshot().HostExec
		config.WithChatsHostExec("different")(o)
		config.WithChatsHostExec(mode)(o)
		Expect(o.ChatSelectionSnapshot().Epoch).To(Equal(epoch + 4))
	})
	It("shares synchronized selection ownership across config value copies", func() {
		o := config.NewApplicationConfig()
		_ = o.ChatSelectionSnapshot()
		copied := *o
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			for range 100 {
				config.WithChatsHostExec("on")(&copied)
				config.WithChatsHostExec("off")(&copied)
			}
		}()
		go func() {
			defer wg.Done()
			for range 100 {
				_ = o.ChatSelectionSnapshot()
			}
		}()
		wg.Wait()
		Expect(o.ChatSelectionSnapshot().HostExec).To(Equal("off"))
		Expect(o.ChatSelectionSnapshot().Epoch).To(Equal(copied.ChatSelectionSnapshot().Epoch))
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
		Expect(o.ChatSelectionSnapshot().Epoch).To(Equal(initial.Epoch))
		o = config.NewApplicationConfig()
		initial = o.ChatSelectionSnapshot()
		o.ApplyRuntimeSettingsAtStartup(&config.RuntimeSettings{ChatsToolProviders: &pins})
		Expect(o.ChatSelectionSnapshot().ToolProviders["bash"]).To(Equal("second"))
		Expect(o.ChatSelectionSnapshot().Epoch).To(Equal(initial.Epoch + 1))
	})

})
