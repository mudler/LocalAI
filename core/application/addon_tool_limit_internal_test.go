package application

import (
	"context"

	"github.com/mudler/LocalAI/core/config"
	addontools "github.com/mudler/LocalAI/core/services/addons/tools"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the cap on add-on tools of a chat", func() {
	It("follows chats_addon_tool_limit at each discovery", func() {
		app := &Application{applicationConfig: &config.ApplicationConfig{Context: context.Background()}}
		Expect(app.addonToolLimit()).To(Equal(addontools.MaxTools), "the adapter default and the setting default agree")
		v := 120
		rs := config.RuntimeSettings{ChatsAddonToolLimit: &v}
		app.applicationConfig.ApplyRuntimeSettings(&rs)
		Expect(app.addonToolLimit()).To(Equal(120))
	})
})

var _ = Describe("the late add-on broker without a gateway", func() {
	It("offers no tools and no cap report, and does not panic", func() {
		app := &Application{applicationConfig: &config.ApplicationConfig{Context: context.Background()}}
		tools, report, err := lateStudioAddons{a: app}.ToolsScoped(context.Background(), "alice", func(string) bool { return false })
		Expect(err).NotTo(HaveOccurred())
		Expect(tools).To(BeEmpty())
		Expect(report).To(BeNil())
	})
})
