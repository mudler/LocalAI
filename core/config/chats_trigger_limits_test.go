package config_test

import (
	"time"

	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("trigger limits of the chats", func() {
	It("takes the defaults when unset or out of range", func() {
		o := &config.ApplicationConfig{}
		Expect(o.EffectiveChatsTriggerRunLimit()).To(Equal(15 * time.Minute))
		Expect(o.EffectiveChatsTriggerMaxPerUser()).To(Equal(50))
		Expect(o.EffectiveChatsTriggerMaxDailyCap()).To(Equal(1000))

		o.ChatsTriggerRunLimit, o.ChatsTriggerMaxPerUser, o.ChatsTriggerMaxDailyCap = 30*time.Second, 1001, -1
		Expect(o.EffectiveChatsTriggerRunLimit()).To(Equal(config.DefaultChatsTriggerRunLimit))
		Expect(o.EffectiveChatsTriggerMaxPerUser()).To(Equal(config.DefaultChatsTriggerMaxPerUser))
		Expect(o.EffectiveChatsTriggerMaxDailyCap()).To(Equal(config.DefaultChatsTriggerMaxDailyCap))
		o.ChatsTriggerRunLimit = 25 * time.Hour
		Expect(o.EffectiveChatsTriggerRunLimit()).To(Equal(config.DefaultChatsTriggerRunLimit))
	})

	It("takes a value inside the bounds, edges included", func() {
		o := &config.ApplicationConfig{}
		o.ChatsTriggerRunLimit, o.ChatsTriggerMaxPerUser, o.ChatsTriggerMaxDailyCap = time.Minute, 1, 1
		Expect(o.EffectiveChatsTriggerRunLimit()).To(Equal(time.Minute))
		Expect(o.EffectiveChatsTriggerMaxPerUser()).To(Equal(1))
		Expect(o.EffectiveChatsTriggerMaxDailyCap()).To(Equal(1))
		o.ChatsTriggerRunLimit, o.ChatsTriggerMaxPerUser, o.ChatsTriggerMaxDailyCap = 24*time.Hour, 1000, 1000
		Expect(o.EffectiveChatsTriggerRunLimit()).To(Equal(24 * time.Hour))
		Expect(o.EffectiveChatsTriggerMaxPerUser()).To(Equal(1000))
	})

	It("applies runtime settings, rejects bad ones and reports the effective values", func() {
		o := &config.ApplicationConfig{}
		d, n, c := "2h", 12, 30
		o.ApplyRuntimeSettings(&config.RuntimeSettings{ChatsTriggerRunLimit: &d, ChatsTriggerMaxPerUser: &n, ChatsTriggerMaxDailyCap: &c})
		Expect(o.EffectiveChatsTriggerRunLimit()).To(Equal(2 * time.Hour))
		Expect(o.EffectiveChatsTriggerMaxPerUser()).To(Equal(12))
		Expect(o.EffectiveChatsTriggerMaxDailyCap()).To(Equal(30))

		bad, zero := "10s", 0
		o.ApplyRuntimeSettings(&config.RuntimeSettings{ChatsTriggerRunLimit: &bad, ChatsTriggerMaxPerUser: &zero})
		Expect(o.EffectiveChatsTriggerRunLimit()).To(Equal(2*time.Hour), "a bad value changes nothing")
		Expect(o.EffectiveChatsTriggerMaxPerUser()).To(Equal(12))

		rs := o.ToRuntimeSettings()
		Expect(*rs.ChatsTriggerRunLimit).To(Equal("2h0m0s"))
		Expect(*rs.ChatsTriggerMaxPerUser).To(Equal(12))
		Expect(*rs.ChatsTriggerMaxDailyCap).To(Equal(30))
	})
})
