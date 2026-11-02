// SPDX-License-Identifier: MIT
package egresspolicy_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/pkg/egresspolicy"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestEgressPolicy(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Egress policy parser") }

var _ = Describe("Wildcard parser", func() {
	It("canonicalizes and deduplicates exact and pattern entries independently", func() {
		list, err := egresspolicy.ParseEgressAllowedHosts("*.BÜCHER.Example., *.xn--bcher-kva.example, BÜCHER.example, *, *:8443", false)
		Expect(err).NotTo(HaveOccurred())
		var entries []string
		for _, h := range list {
			entries = append(entries, h.String())
		}
		Expect(entries).To(Equal([]string{"*.xn--bcher-kva.example", "xn--bcher-kva.example", "*", "*:8443"}))
	})
	It("counts wildcard entries against the same bound", func() {
		var entries []string
		for i := range 65 {
			entries = append(entries, fmt.Sprintf("*.h%d.example", i))
		}
		_, err := egresspolicy.ParseEgressAllowedHosts(strings.Join(entries[:64], ","), false)
		Expect(err).NotTo(HaveOccurred())
		_, err = egresspolicy.ParseEgressAllowedHosts(strings.Join(entries, ","), false)
		Expect(err).To(HaveOccurred())
	})
	It("keeps wildcard matching public-only regardless of parser private switch", func() {
		list, err := egresspolicy.ParseEgressAllowedHosts("*", true)
		Expect(err).NotTo(HaveOccurred())
		for _, host := range []string{"10.0.0.1", "127.0.0.1", "169.254.169.254", "::1", "fd00::1", "localhost", "a.localhost"} {
			Expect(list[0].Matches(host, 443)).To(BeFalse(), host)
		}
	})
	It("keeps the apex separate from descendants", func() {
		list, err := egresspolicy.ParseEgressAllowedHosts("*.example.com,example.com", false)
		Expect(err).NotTo(HaveOccurred())
		Expect(list[0].Matches("example.com", 443)).To(BeFalse())
		Expect(list[1].Matches("example.com", 443)).To(BeTrue())
	})
})
