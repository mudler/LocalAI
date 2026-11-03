// SPDX-License-Identifier: MIT
package egress_test

import (
	"github.com/mudler/LocalAI/core/services/studio/egress"
	"github.com/mudler/LocalAI/pkg/egresspolicy"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("TestAddonSettingsAuthorityWildcards", func() {
	for _, row := range []struct {
		pattern, host string
		port          int
		match         bool
	}{
		{"*.example.com", "a.example.com", 443, true},
		{"*.example.com", "a.b.example.com", 443, true},
		{"*.example.com", "example.com", 443, false},
		{"*.example.com", "evilexample.com", 443, false},
		{"*.example.com", "a.example.com.evil", 443, false},
		{"*.EXAMPLE.COM.", "A.Example.COM.", 443, true},
		{"*.bücher.example", "a.XN--BCHER-KVA.example.", 443, true},
		{"*.example.com:8443", "a.example.com", 8443, true},
		{"*.example.com:8443", "a.example.com", 443, false},
		{"*", "example.net", 443, true},
		{"*:8443", "example.net", 8443, true},
		{"*:8443", "example.net", 443, false},
		{"*", "93.184.216.34", 443, true},
	} {
		It("matches "+row.pattern+" against "+row.host+" on port", func() {
			entries, err := egresspolicy.ParseEgressAllowedHosts(row.pattern, false)
			Expect(err).NotTo(HaveOccurred())
			s := egress.Settings{AllowedHosts: []egress.AllowedHost{{Host: entries[0].Host, Port: entries[0].Port}}}
			Expect(s.AllowsHost(row.host, row.port)).To(Equal(row.match))
		})
	}
	for _, pattern := range []string{"*foo.example", "foo*.example", "foo.*.example", "**.example", "*.*.example", "https://*.example", "*.example/path", "*.example:*", "*.example:443:80", "*.127.0.0.1", "*.example:0", "*.example:22", "*.example:25"} {
		It("rejects ambiguous pattern "+pattern, func() { _, err := egresspolicy.ParseEgressAllowedHosts(pattern, true); Expect(err).To(HaveOccurred()) })
	}
	for _, ceiling := range []egress.Preset{egress.PresetOff, egress.PresetRegistries, egress.PresetAll} {
		for _, host := range []string{"example.com", "pypi.org", "10.0.0.1", "169.254.169.254", "127.0.0.1"} {
			for _, private := range []bool{false, true} {
				It("bounds wildcard grant "+string(ceiling)+" "+host+" private switch", func() {
					s := egress.Settings{Ceiling: ceiling, AllowPrivate: private, AllowedHosts: []egress.AllowedHost{{Host: "*"}}}
					v := egress.Decide(s, egress.Chat{Preset: egress.PresetRegistries, Rules: []egress.Rule{{Host: host, Port: 443, Allow: false}}}, tlsReq(host), reg)
					allow := ceiling != egress.PresetOff && !egress.PrivateLiteral(host) && (ceiling == egress.PresetAll || host == "pypi.org")
					Expect(v.Action == egress.Allow).To(Equal(allow))
					Expect(v.AllowPrivate).To(BeFalse())
				})
			}
		}
	}
	It("keeps network off, configured ports and resolved-private checks ahead of wildcard grants", func() {
		s := egress.Settings{Ceiling: egress.PresetAll, AllowPrivate: true, AllowedHosts: []egress.AllowedHost{{Host: "*"}}}
		Expect(egress.Decide(s, egress.Chat{Preset: egress.PresetOff}, tlsReq("example.com"), reg).Action).NotTo(Equal(egress.Allow))
		r := tlsReq("example.com")
		r.Port = 8443
		Expect(egress.Decide(s, egress.Chat{Preset: egress.PresetAll}, r, reg).Reason).To(Equal(egress.ReasonPort))
		r = tlsReq("example.com")
		r.Private = true
		Expect(egress.Decide(s, egress.Chat{Preset: egress.PresetAll}, r, reg).Action).NotTo(Equal(egress.Allow))
	})
	It("requires TLS registry admission under the registries ceiling", func() {
		s := egress.Settings{Ceiling: egress.PresetRegistries, AllowedHosts: []egress.AllowedHost{{Host: "*"}}}
		Expect(egress.Decide(s, egress.Chat{Preset: egress.PresetRegistries}, httpReq("pypi.org"), reg).Action).To(Equal(egress.Block))
	})
	It("preserves exact-host compatibility under the registries ceiling", func() {
		s := egress.Settings{Ceiling: egress.PresetRegistries, AllowedHosts: []egress.AllowedHost{{Host: "example.com"}}}
		v := egress.Decide(s, egress.Chat{Preset: egress.PresetRegistries}, httpReq("example.com"), reg)
		Expect(v.Action).To(Equal(egress.Allow))
		Expect(v.Reason).To(Equal(egress.ReasonAllowedHost))
	})
	It("preserves private administrator-rule handling without a wildcard private grant", func() {
		s := egress.Settings{Ceiling: egress.PresetAll, AllowedHosts: []egress.AllowedHost{{Host: "*"}}}
		c := egress.Chat{Preset: egress.PresetAll, Rules: []egress.Rule{{Host: "10.0.0.1", Port: 443, Allow: true, Admin: true}}}
		Expect(egress.Decide(s, c, tlsReq("10.0.0.1"), reg).Action).To(Equal(egress.Block))
		s.AllowPrivate = true
		v := egress.Decide(s, c, tlsReq("10.0.0.1"), reg)
		Expect(v.Action).To(Equal(egress.Allow))
		Expect(v.Reason).To(Equal(egress.ReasonRule))
		Expect(v.AllowPrivate).To(BeTrue())
		c.Rules[0].Admin = false
		Expect(egress.Decide(s, c, tlsReq("10.0.0.1"), reg).Action).To(Equal(egress.Block))
	})
	It("admits wildcard grants for extra registries only over TLS", func() {
		s := egress.Settings{Ceiling: egress.PresetRegistries, ExtraRegistries: []string{"mirror.example.com"}, AllowedHosts: []egress.AllowedHost{{Host: "*.example.com"}}}
		c := egress.Chat{Preset: egress.PresetRegistries}
		Expect(egress.Decide(s, c, tlsReq("mirror.example.com"), reg).Reason).To(Equal(egress.ReasonAllowedHost))
		Expect(egress.Decide(s, c, httpReq("mirror.example.com"), reg).Action).To(Equal(egress.Block))
	})

	It("keeps extra registries exact", func() {
		for _, p := range []string{"*", "*.example.com"} {
			_, err := egresspolicy.ParseEgressHosts(p)
			Expect(err).To(HaveOccurred())
		}
	})
})
