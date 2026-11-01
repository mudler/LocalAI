package application

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/studio/egress"
)

var _ = Describe("the list settings of the Sandbox network", func() {
	It("keeps the last valid list when the stored value stops parsing, and takes a valid one again", func() {
		cfg := &config.ApplicationConfig{ChatsEgressAllowedHosts: "api.example.com, git.example.org:8443", ChatsEgressExtraRegistries: "mirror.example.org", ChatsEgressPorts: "443"}
		a := &Application{applicationConfig: cfg}
		good := a.studioEgressSettings()
		Expect(good.AllowedHosts).To(Equal([]egress.AllowedHost{{Host: "api.example.com"}, {Host: "git.example.org", Port: 8443}}))
		Expect(good.Ports).To(Equal([]int{443}))

		cfg.ChatsEgressAllowedHosts = "*.example.com"
		cfg.ChatsEgressExtraRegistries = "https://bad"
		cfg.ChatsEgressPorts = "22"
		bad := a.studioEgressSettings()
		Expect(bad.AllowedHosts).To(Equal(good.AllowedHosts), "a bad value neither empties nor widens the list")
		Expect(bad.ExtraRegistries).To(Equal([]string{"mirror.example.org"}))
		Expect(bad.Ports).To(Equal([]int{443}), "the ports do not widen to the default")

		cfg.ChatsEgressAllowedHosts = "docs.example.net"
		Expect(a.studioEgressSettings().AllowedHosts).To(Equal([]egress.AllowedHost{{Host: "docs.example.net"}}))
	})

	It("uses the default when a process never saw a valid value", func() {
		a := &Application{applicationConfig: &config.ApplicationConfig{ChatsEgressAllowedHosts: "https://x", ChatsEgressPorts: "nope"}}
		set := a.studioEgressSettings()
		Expect(set.AllowedHosts).To(BeEmpty())
		Expect(set.Ports).To(Equal([]int{80, 443}))
	})
})
