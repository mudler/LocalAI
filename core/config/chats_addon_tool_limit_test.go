package config_test

import (
	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("chats_addon_tool_limit", func() {
	It("is 64 while unset, and the snapshot shows the limit that applies", func() {
		o := &config.ApplicationConfig{}
		Expect(o.EffectiveChatsAddonToolLimit()).To(Equal(64))
		rs := o.ToRuntimeSettings()
		Expect(rs.ChatsAddonToolLimit).NotTo(BeNil())
		Expect(*rs.ChatsAddonToolLimit).To(Equal(config.DefaultChatsAddonToolLimit))
	})

	It("applies a value in bounds and keeps the limit in force for one out of bounds", func() {
		o := &config.ApplicationConfig{}
		for _, v := range []int{config.MinChatsAddonToolLimit, 100, config.MaxChatsAddonToolLimit} {
			rs := config.RuntimeSettings{ChatsAddonToolLimit: &v}
			o.ApplyRuntimeSettings(&rs)
			Expect(o.EffectiveChatsAddonToolLimit()).To(Equal(v))
		}
		for _, v := range []int{-1, 0, config.MinChatsAddonToolLimit - 1, config.MaxChatsAddonToolLimit + 1} {
			rs := config.RuntimeSettings{ChatsAddonToolLimit: &v}
			o.ApplyRuntimeSettings(&rs)
			Expect(o.EffectiveChatsAddonToolLimit()).To(Equal(config.MaxChatsAddonToolLimit), "a bad value in the file changes nothing")
		}
	})

	It("falls back to the default for a stored value out of bounds", func() {
		for _, v := range []int{1, 15, 257, 10000} {
			o := &config.ApplicationConfig{ChatsAddonToolLimit: v}
			Expect(o.EffectiveChatsAddonToolLimit()).To(Equal(config.DefaultChatsAddonToolLimit))
		}
	})
})
