package application

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/studio"
)

var _ = Describe("the limits of the loop host", func() {
	It("passes every loop setting, the daily script checks too", func() {
		cfg := &config.ApplicationConfig{}
		Expect(loopLimitsFrom(cfg)).To(Equal(studio.LoopLimits{
			MinInterval: config.DefaultChatsLoopMinInterval, MaxRuns: config.DefaultChatsLoopMaxRuns,
			PerChat: config.DefaultChatsLoopMaxPerChat, PerUser: config.DefaultChatsLoopMaxPerUser,
			DailyRuns: config.DefaultChatsLoopDailyRuns, DailyScriptChecks: config.DefaultChatsLoopDailyScriptChecks,
		}))
		checks := 42
		cfg.ApplyRuntimeSettings(&config.RuntimeSettings{ChatsLoopDailyScriptChecks: &checks})
		Expect(loopLimitsFrom(cfg).DailyScriptChecks).To(Equal(42), "a change of the setting applies at the next read")
	})
})
