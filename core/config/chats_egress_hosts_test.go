package config_test

import (
	"strings"

	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ParseEgressAllowedHosts", func() {
	entries := func(s string, private bool) []string {
		hosts, err := config.ParseEgressAllowedHosts(s, private)
		Expect(err).NotTo(HaveOccurred(), s)
		var out []string
		for _, h := range hosts {
			out = append(out, h.String())
		}
		return out
	}

	It("reads exact hosts and pinned ports, lower-cased and without duplicates", func() {
		Expect(entries(" API.Example.COM , git.example.org:8443, api.example.com., ,git.example.org:8443", false)).
			To(Equal([]string{"api.example.com", "git.example.org:8443"}))
		Expect(entries("", false)).To(BeEmpty())
	})

	It("writes an international name in punycode", func() {
		Expect(entries("bücher.example", false)).To(Equal([]string{"xn--bcher-kva.example"}))
		Expect(entries("XN--BCHER-KVA.example:8443", false)).To(Equal([]string{"xn--bcher-kva.example:8443"}))
	})

	It("keeps the same host on two ports as two entries", func() {
		Expect(entries("a.example:80,a.example:443,a.example", false)).To(Equal([]string{"a.example:80", "a.example:443", "a.example"}))
	})

	It("refuses what is not an exact host name", func() {
		for _, bad := range []string{
			"*.example.com", "https://example.com", "example.com/path", "user@example.com", "example.com?x=1",
			"0x7f.1", "2130706433", "example.com:0", "example.com:70000", "example.com:ssh",
			"example.com:22", "example.com:25", "bad host.example",
		} {
			_, err := config.ParseEgressAllowedHosts(bad, true)
			Expect(err).To(HaveOccurred(), bad)
		}
	})

	It("refuses an IP literal outside the public internet unless the private switch is on", func() {
		for _, lit := range []string{"localhost", "foo.localhost", "10.0.0.5", "127.0.0.1", "169.254.169.254", "192.168.1.9:8080", "[::1]", "fd00::1"} {
			_, err := config.ParseEgressAllowedHosts(lit, false)
			Expect(err).To(HaveOccurred(), lit)
		}
		Expect(entries("10.0.0.5, [fd00::1]:8080", true)).To(Equal([]string{"10.0.0.5", "[fd00::1]:8080"}))
		Expect(entries("93.184.216.34", false)).To(Equal([]string{"93.184.216.34"}))
	})

	It("accepts at most 64 hosts", func() {
		var many []string
		for i := range 65 {
			many = append(many, "h"+strings.Repeat("a", i%5)+string(rune('a'+i%26))+string(rune('a'+i/26))+".example")
		}
		_, err := config.ParseEgressAllowedHosts(strings.Join(many[:64], ","), false)
		Expect(err).NotTo(HaveOccurred())
		_, err = config.ParseEgressAllowedHosts(strings.Join(many, ","), false)
		Expect(err).To(MatchError(ContainSubstring("at most 64")))
	})
})
