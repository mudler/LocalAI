package config_test

import (
	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("coordinator limits of the chats", func() {
	set := func(o *config.ApplicationConfig, a, b, c, d, e int) {
		o.ChatsCoordinatorHelpersCeiling, o.ChatsCoordinatorDepthCeiling, o.ChatsCoordinatorRunsPerDay = a, b, c
		o.ChatsCoordinatorMaxRunning, o.ChatsCoordinatorAuditDays = d, e
	}
	got := func(o *config.ApplicationConfig) []int {
		return []int{
			o.EffectiveChatsCoordinatorHelpersCeiling(), o.EffectiveChatsCoordinatorDepthCeiling(),
			o.EffectiveChatsCoordinatorRunsPerDay(), o.EffectiveChatsCoordinatorMaxRunning(),
			o.EffectiveChatsCoordinatorAuditDays(),
		}
	}

	It("takes the defaults when unset or out of range", func() {
		o := &config.ApplicationConfig{}
		Expect(got(o)).To(Equal([]int{16, 3, 48, 2, 90}))
		for _, bad := range [][]int{{0, 0, 0, 0, 0}, {-1, -1, -1, -1, -1}, {65, 5, 1001, 33, 366}} {
			set(o, bad[0], bad[1], bad[2], bad[3], bad[4])
			Expect(got(o)).To(Equal([]int{
				config.DefaultChatsCoordinatorHelpersCeiling, config.DefaultChatsCoordinatorDepthCeiling,
				config.DefaultChatsCoordinatorRunsPerDay, config.DefaultChatsCoordinatorMaxRunning,
				config.DefaultChatsCoordinatorAuditDays,
			}), "%v", bad)
		}
	})

	It("takes a value inside the bounds, edges included", func() {
		o := &config.ApplicationConfig{}
		set(o, 1, 1, 1, 1, 1)
		Expect(got(o)).To(Equal([]int{1, 1, 1, 1, 1}))
		set(o, config.MaxChatsCoordinatorHelpersCeiling, config.MaxChatsCoordinatorDepthCeiling,
			config.MaxChatsCoordinatorRunsPerDay, config.MaxChatsCoordinatorMaxRunning, config.MaxChatsCoordinatorAuditDays)
		Expect(got(o)).To(Equal([]int{64, 4, 1000, 32, 365}))
	})

	It("applies runtime settings, rejects bad ones and reports the effective values", func() {
		o := &config.ApplicationConfig{}
		a, b, c, d, e := 8, 2, 10, 4, 30
		o.ApplyRuntimeSettings(&config.RuntimeSettings{
			ChatsCoordinatorHelpersCeiling: &a, ChatsCoordinatorDepthCeiling: &b, ChatsCoordinatorRunsPerDay: &c,
			ChatsCoordinatorMaxRunning: &d, ChatsCoordinatorAuditDays: &e,
		})
		Expect(got(o)).To(Equal([]int{8, 2, 10, 4, 30}))

		zero, big := 0, 5
		o.ApplyRuntimeSettings(&config.RuntimeSettings{ChatsCoordinatorHelpersCeiling: &zero, ChatsCoordinatorDepthCeiling: &big})
		Expect(got(o)).To(Equal([]int{8, 2, 10, 4, 30}), "a bad value changes nothing")

		rs := o.ToRuntimeSettings()
		Expect(*rs.ChatsCoordinatorHelpersCeiling).To(Equal(8))
		Expect(*rs.ChatsCoordinatorDepthCeiling).To(Equal(2))
		Expect(*rs.ChatsCoordinatorRunsPerDay).To(Equal(10))
		Expect(*rs.ChatsCoordinatorMaxRunning).To(Equal(4))
		Expect(*rs.ChatsCoordinatorAuditDays).To(Equal(30))
	})
})
