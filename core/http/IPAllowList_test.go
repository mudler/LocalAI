package http_test

import (
	. "github.com/mudler/LocalAI/core/http"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("IPAllowList", func() {
	It("allows all IPs when allowlist is empty", func() {
		w, err := NewIPAllowList("")
		Expect(err).ToNot(HaveOccurred())
		Expect(w.IsAllowed("192.168.1.100")).To(BeTrue())
	})

	It("respects CIDRs and explicit IPs", func() {
		allowList := "192.168.1.0/24,10.0.0.1,127.0.0.1"
		w, err := NewIPAllowList(allowList)
		Expect(err).ToNot(HaveOccurred())

		cases := []struct {
			ip       string
			expected bool
		}{
			{"192.168.1.100", true},
			{"10.0.0.1", true},
			{"127.0.0.1", true},
			{"10.0.0.2", false},
			{"172.16.0.1", false},
		}

		for _, tc := range cases {
			Expect(w.IsAllowed(tc.ip)).To(Equal(tc.expected), "IP: %s", tc.ip)
		}
	})
})
