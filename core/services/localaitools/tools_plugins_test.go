package localaitools

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("plugins tool", func() {
	It("surfaces the browser-session sentence and the review page for a plugin that runs code", func() {
		fc := &fakeClient{installPlugin: func(plugin, _ string) (*PluginInstallResult, error) {
			return nil, &PluginNeedsBrowserError{Plugin: plugin, ReviewPath: "/app/addons?plugin=hook%40acme&tab=plugins", Reasons: []string{"hook-command"}}
		}}
		ctx, sess, done := connectInMemory(fc, Options{})
		DeferCleanup(done)

		res := callTool(ctx, sess, ToolPlugins, PluginsRequest{Action: PluginsActionInstall, Plugin: "hook@acme", Scope: "instance"})
		Expect(res.IsError).To(BeTrue())
		Expect(resultText(res)).To(SatisfyAll(
			ContainSubstring("hook@acme runs code (hook-command)"),
			ContainSubstring("needs the administrator's own browser session"),
			ContainSubstring("/app/addons?plugin=hook%40acme&tab=plugins"),
		))
	})

	It("keeps the read actions and refuses the changes in the read-only flavour", func() {
		fc := &fakeClient{}
		ctx, sess, done := connectInMemory(fc, Options{DisableMutating: true})
		DeferCleanup(done)

		Expect(callTool(ctx, sess, ToolPlugins, PluginsRequest{Action: PluginsActionBrowse}).IsError).To(BeFalse())
		for _, a := range []PluginsRequest{
			{Action: PluginsActionInstall, Plugin: "fmt@acme"},
			{Action: PluginsActionAddMarketplace, Source: "acme/plugins"},
			{Action: PluginsActionRefreshMarketplace, Marketplace: "acme"},
			{Action: PluginsActionEnable, ID: "i1"},
			{Action: PluginsActionDisable, ID: "i1"},
			{Action: PluginsActionRemove, ID: "i1"},
		} {
			res := callTool(ctx, sess, ToolPlugins, a)
			Expect(res.IsError).To(BeTrue(), a.Action)
			Expect(resultText(res)).To(ContainSubstring("read-only"))
		}
		Expect(fc.recorded()).To(HaveLen(1))
	})

	It("lists only the installs with an update or a block for check_updates without an id", func() {
		fc := &fakeClient{listPluginInstalls: func(bool) ([]PluginInstall, error) {
			return []PluginInstall{{ID: "a", State: "installed"}, {ID: "b", State: "update_available"}, {ID: "c", State: "blocked"}}, nil
		}}
		ctx, sess, done := connectInMemory(fc, Options{})
		DeferCleanup(done)

		res := callTool(ctx, sess, ToolPlugins, PluginsRequest{Action: PluginsActionCheckUpdates})
		Expect(res.IsError).To(BeFalse(), resultText(res))
		Expect(resultText(res)).To(SatisfyAll(ContainSubstring(`"id": "b"`), ContainSubstring(`"id": "c"`), Not(ContainSubstring(`"id": "a"`))))
	})
})
