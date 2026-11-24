package config_test

import (
	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("chats_egress_audit_days in the settings snapshot", func() {
	It("shows the retention that applies, so saving the snapshot back stays valid", func() {
		o := &config.ApplicationConfig{}
		rs := o.ToRuntimeSettings()
		Expect(rs.ChatsEgressAuditDays).NotTo(BeNil())
		Expect(*rs.ChatsEgressAuditDays).To(Equal(config.DefaultChatsEgressAuditDays))

		target := &config.ApplicationConfig{}
		target.ApplyRuntimeSettings(&rs)
		Expect(target.ChatsEgressAuditDays).To(Equal(config.DefaultChatsEgressAuditDays))
	})

	It("keeps a value the administrator set", func() {
		o := &config.ApplicationConfig{ChatsEgressAuditDays: 7}
		rs := o.ToRuntimeSettings()
		Expect(*rs.ChatsEgressAuditDays).To(Equal(7))
	})
})
