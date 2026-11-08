package config_test

import (
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The prune and loop goroutines read these limits while a settings POST
// rewrites them. Under -race this fails when a getter or a setter skips the lock.
var _ = Describe("chats limits read while settings are applied", func() {
	It("does not race", func() {
		o := &config.ApplicationConfig{}
		days, runs, perChat, perUser, daily := 10, 5, 2, 3, 4
		interval, runLimit := "2m", "20m"
		perUserTrig, maxCap := 7, 100
		rs := &config.RuntimeSettings{
			ChatsEgressAuditDays: &days, ChatsLoopMaxRuns: &runs, ChatsLoopMaxPerChat: &perChat,
			ChatsLoopMaxPerUser: &perUser, ChatsLoopDailyRuns: &daily, ChatsLoopMinInterval: &interval,
			ChatsTriggerRunLimit: &runLimit, ChatsTriggerMaxPerUser: &perUserTrig, ChatsTriggerMaxDailyCap: &maxCap,
		}
		var wg sync.WaitGroup
		stop := make(chan struct{})
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-stop:
						return
					default:
					}
					_ = o.EffectiveChatsEgressAuditDays()
					_ = o.EffectiveChatsLoopMinInterval()
					_ = o.EffectiveChatsLoopMaxRuns()
					_ = o.EffectiveChatsLoopMaxPerChat()
					_ = o.EffectiveChatsLoopMaxPerUser()
					_ = o.EffectiveChatsLoopDailyRuns()
					_ = o.EffectiveChatsTriggerRunLimit()
					_ = o.EffectiveChatsTriggerMaxPerUser()
					_ = o.EffectiveChatsTriggerMaxDailyCap()
				}
			}()
		}
		for range 200 {
			o.ApplyRuntimeSettings(rs)
		}
		close(stop)
		wg.Wait()
		Expect(o.EffectiveChatsEgressAuditDays()).To(Equal(10))
		Expect(o.EffectiveChatsLoopMaxRuns()).To(Equal(5))
		Expect(o.EffectiveChatsTriggerRunLimit()).To(Equal(20 * time.Minute))
		Expect(o.EffectiveChatsTriggerMaxPerUser()).To(Equal(7))
		Expect(o.EffectiveChatsTriggerMaxDailyCap()).To(Equal(100))
	})
})
