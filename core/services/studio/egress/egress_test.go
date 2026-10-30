package egress_test

import (
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/services/studio/egress"
	"github.com/mudler/LocalAI/pkg/web/safehttp"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var reg = egress.BuiltinRegistries()

func tlsReq(host string) egress.Request {
	return egress.Request{Host: host, Port: 443, Kind: egress.KindConnect, TLS: true}
}

func httpReq(host string) egress.Request {
	return egress.Request{Host: host, Port: 80, Kind: egress.KindHTTP}
}

var _ = Describe("Decide", func() {
	settings := func(ceiling egress.Preset) egress.Settings { return egress.Settings{Ceiling: ceiling} }

	type row struct {
		ceiling, preset egress.Preset
		req             egress.Request
		attended        bool
		action, reason  string
	}
	DescribeTable("ceiling x preset x host class",
		func(r row) {
			v := egress.Decide(settings(r.ceiling), egress.Chat{Preset: r.preset, Attended: r.attended}, r.req, reg)
			Expect(v.Action).To(Equal(r.action))
			Expect(v.Reason).To(Equal(r.reason))
			Expect(v.AllowPrivate).To(BeFalse(), "a preset never reaches private addresses")
		},
		// Ceiling off: nothing, and never a card.
		Entry("off/all registry", row{egress.PresetOff, egress.PresetAll, tlsReq("pypi.org"), true, egress.Block, egress.ReasonCeiling}),
		Entry("off/all public", row{egress.PresetOff, egress.PresetAll, tlsReq("example.com"), true, egress.Block, egress.ReasonCeiling}),
		// Ceiling registries.
		Entry("reg/reg registry", row{egress.PresetRegistries, egress.PresetRegistries, tlsReq("pypi.org"), true, egress.Allow, egress.ReasonPreset}),
		Entry("reg/all registry is capped to registries", row{egress.PresetRegistries, egress.PresetAll, tlsReq("files.pythonhosted.org"), true, egress.Allow, egress.ReasonPreset}),
		Entry("reg/all public is capped: no card above the ceiling", row{egress.PresetRegistries, egress.PresetAll, tlsReq("example.com"), true, egress.Block, egress.ReasonCeiling}),
		Entry("reg/off registry asks", row{egress.PresetRegistries, egress.PresetOff, tlsReq("pypi.org"), true, egress.Ask, egress.ReasonCard}),
		Entry("reg/off registry unattended", row{egress.PresetRegistries, egress.PresetOff, tlsReq("pypi.org"), false, egress.Block, egress.ReasonUnattended}),
		Entry("reg/reg plain HTTP to a registry", row{egress.PresetRegistries, egress.PresetRegistries, egress.Request{Host: "pypi.org", Port: 80, Kind: egress.KindHTTP}, true, egress.Block, egress.ReasonRegistriesTLS}),
		Entry("reg/reg GitHub release host", row{egress.PresetRegistries, egress.PresetRegistries, tlsReq("objects.githubusercontent.com"), true, egress.Allow, egress.ReasonPreset}),
		Entry("reg/reg GitHub release assets host", row{egress.PresetRegistries, egress.PresetRegistries, tlsReq("release-assets.githubusercontent.com"), true, egress.Allow, egress.ReasonPreset}),
		Entry("reg/reg raw user content is not a registry", row{egress.PresetRegistries, egress.PresetRegistries, tlsReq("raw.githubusercontent.com"), true, egress.Block, egress.ReasonCeiling}),
		// Ceiling all.
		Entry("all/all public", row{egress.PresetAll, egress.PresetAll, tlsReq("example.com"), true, egress.Allow, egress.ReasonPreset}),
		Entry("all/all plain http", row{egress.PresetAll, egress.PresetAll, httpReq("example.com"), true, egress.Allow, egress.ReasonPreset}),
		Entry("all/reg public asks", row{egress.PresetAll, egress.PresetRegistries, tlsReq("example.com"), true, egress.Ask, egress.ReasonCard}),
		Entry("all/off public asks", row{egress.PresetAll, egress.PresetOff, tlsReq("example.com"), true, egress.Ask, egress.ReasonCard}),
		Entry("all/off public unattended", row{egress.PresetAll, egress.PresetOff, tlsReq("example.com"), false, egress.Block, egress.ReasonUnattended}),
		Entry("all/all private literal", row{egress.PresetAll, egress.PresetAll, tlsReq("10.0.0.5"), true, egress.Block, egress.ReasonPrivate}),
		Entry("all/all metadata literal", row{egress.PresetAll, egress.PresetAll, httpReq("169.254.169.254"), true, egress.Block, egress.ReasonPrivate}),
		Entry("unknown preset counts as off", row{egress.PresetAll, egress.Preset("wide"), tlsReq("example.com"), false, egress.Block, egress.ReasonUnattended}),
		Entry("unknown ceiling counts as off", row{egress.Preset("wide"), egress.PresetAll, tlsReq("pypi.org"), true, egress.Block, egress.ReasonCeiling}),
	)

	It("blocks a port outside the settings first", func() {
		v := egress.Decide(egress.Settings{Ceiling: egress.PresetAll}, egress.Chat{Preset: egress.PresetAll}, egress.Request{Host: "example.com", Port: 22, Kind: egress.KindConnect}, reg)
		Expect(v).To(Equal(egress.Verdict{Action: egress.Block, Reason: egress.ReasonPort}))
		v = egress.Decide(egress.Settings{Ceiling: egress.PresetAll, Ports: []int{443, 8443}}, egress.Chat{Preset: egress.PresetAll}, egress.Request{Host: "example.com", Port: 8443, TLS: true}, reg)
		Expect(v.Action).To(Equal(egress.Allow))
	})

	It("a deny rule wins over the preset and asks no more", func() {
		c := egress.Chat{Preset: egress.PresetAll, Attended: true, Rules: []egress.Rule{{Host: "example.com", Port: 443}}}
		v := egress.Decide(settings(egress.PresetAll), c, tlsReq("example.com"), reg)
		Expect(v).To(Equal(egress.Verdict{Action: egress.Block, Reason: egress.ReasonDenied}))
	})

	It("an allow rule applies only while the ceiling admits the host", func() {
		c := egress.Chat{Preset: egress.PresetOff, Rules: []egress.Rule{{Host: "example.com", Port: 443, Allow: true}, {Host: "pypi.org", Port: 443, Allow: true}}}
		Expect(egress.Decide(settings(egress.PresetAll), c, tlsReq("example.com"), reg).Reason).To(Equal(egress.ReasonRule))
		Expect(egress.Decide(settings(egress.PresetRegistries), c, tlsReq("example.com"), reg).Reason).To(Equal(egress.ReasonCeiling), "a lowered ceiling voids the rule")
		Expect(egress.Decide(settings(egress.PresetRegistries), c, tlsReq("pypi.org"), reg).Reason).To(Equal(egress.ReasonRule))
		Expect(egress.Decide(settings(egress.PresetOff), c, tlsReq("pypi.org"), reg).Reason).To(Equal(egress.ReasonCeiling))
	})

	It("a once grant allows the host for the call", func() {
		c := egress.Chat{Preset: egress.PresetOff, Once: []egress.Grant{{Host: "example.com", Port: 443}}}
		Expect(egress.Decide(settings(egress.PresetAll), c, tlsReq("example.com"), reg).Reason).To(Equal(egress.ReasonOnce))
	})

	It("reaches a private host only with the switch and an administrator's rule", func() {
		user := egress.Chat{Rules: []egress.Rule{{Host: "10.0.0.5", Port: 443, Allow: true}}}
		admin := egress.Chat{Rules: []egress.Rule{{Host: "10.0.0.5", Port: 443, Allow: true, Admin: true}}}
		off := egress.Settings{Ceiling: egress.PresetAll}
		on := egress.Settings{Ceiling: egress.PresetAll, AllowPrivate: true}
		Expect(egress.Decide(off, admin, tlsReq("10.0.0.5"), reg).Reason).To(Equal(egress.ReasonPrivate))
		Expect(egress.Decide(on, user, tlsReq("10.0.0.5"), reg).Action).NotTo(Equal(egress.Allow))
		v := egress.Decide(on, admin, tlsReq("10.0.0.5"), reg)
		Expect(v).To(Equal(egress.Verdict{Action: egress.Allow, Reason: egress.ReasonRule, AllowPrivate: true}))
		// A name found to be private needs the same.
		r := tlsReq("mirror.internal.example")
		r.Private = true
		Expect(egress.Decide(on, egress.Chat{Preset: egress.PresetAll, Attended: true}, r, reg).Action).To(Equal(egress.Ask))
		Expect(egress.Decide(off, egress.Chat{Preset: egress.PresetAll, Attended: true}, r, reg).Reason).To(Equal(egress.ReasonPrivate))
	})

	It("offers at most three pending cards", func() {
		c := egress.Chat{Attended: true, Pending: make([]egress.Card, egress.MaxPendingCards)}
		Expect(egress.Decide(settings(egress.PresetAll), c, tlsReq("example.com"), reg).Reason).To(Equal(egress.ReasonCardsFull))
	})

	It("treats an extra registry as a registry", func() {
		s := egress.Settings{Ceiling: egress.PresetRegistries, ExtraRegistries: []string{"pypi.mirror.example"}}
		Expect(egress.Decide(s, egress.Chat{Preset: egress.PresetRegistries}, tlsReq("pypi.mirror.example"), reg).Action).To(Equal(egress.Allow))
	})

	It("caps the default preset by the ceiling", func() {
		Expect(egress.Settings{Ceiling: egress.PresetRegistries, Default: egress.PresetAll}.DefaultPreset()).To(Equal(egress.PresetRegistries))
		Expect(egress.Settings{Ceiling: egress.PresetAll}.DefaultPreset()).To(Equal(egress.PresetOff))
	})
})

var _ = Describe("ChatState", func() {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

	It("replaces and removes rules by host and port", func() {
		var s egress.ChatState
		s.PutRule(egress.Rule{Host: "a.example", Port: 443, Allow: true})
		s.PutRule(egress.Rule{Host: "a.example", Port: 443, Allow: false})
		s.PutRule(egress.Rule{Host: "a.example", Port: 80, Allow: true})
		Expect(s.Rules).To(HaveLen(2))
		r, ok := s.Rule("a.example", 443)
		Expect(ok).To(BeTrue())
		Expect(r.Allow).To(BeFalse())
		Expect(s.DeleteRule("a.example", 443)).To(BeTrue())
		Expect(s.DeleteRule("a.example", 443)).To(BeFalse())
	})

	It("keeps the last 50 cards, dropping answered ones first", func() {
		var s egress.ChatState
		s.PutCard(egress.Card{ID: "p", State: egress.CardPending})
		for i := 0; i < 60; i++ {
			s.PutCard(egress.Card{ID: strings.Repeat("x", i+1), State: egress.CardDenied})
		}
		Expect(s.Cards).To(HaveLen(egress.MaxCards))
		_, ok := s.Card("p")
		Expect(ok).To(BeTrue(), "a pending card is never dropped")
	})

	It("gives the grants of a call and a late grant, never a lapsed one", func() {
		var s egress.ChatState
		s.AddGrant(egress.Grant{Host: "a", Port: 443, Op: "op1", Expires: now.Add(time.Hour)}, now)
		s.AddGrant(egress.Grant{Host: "b", Port: 443, Expires: now.Add(egress.LateOnce)}, now)
		s.AddGrant(egress.Grant{Host: "c", Port: 443, Op: "op1", Expires: now.Add(-time.Second)}, now)
		Expect(s.Grants("op1", now)).To(HaveLen(2))
		Expect(s.Grants("op2", now)).To(HaveLen(1))
		Expect(s.Grants("op2", now.Add(time.Hour))).To(BeEmpty())
	})
})

var _ = Describe("the registries list", func() {
	g := safehttp.NewGuard(safehttp.GuardPolicy{}, nil)

	It("has exact HTTPS host names only, no upload host and no raw user content", func() {
		Expect(reg.List).NotTo(BeEmpty())
		for _, e := range reg.List {
			for _, h := range e.Hosts {
				canon, err := g.CheckHost(h, 443)
				Expect(err).NotTo(HaveOccurred(), h)
				Expect(canon).To(Equal(h))
				Expect(h).NotTo(ContainSubstring("*"))
				Expect(h).NotTo(HavePrefix("upload."))
				Expect(h).NotTo(Equal("raw.githubusercontent.com"))
			}
		}
		Expect(reg.Has("github.com", nil)).To(BeTrue())
		Expect(reg.Has("upload.pypi.org", nil)).To(BeFalse())
	})

	It("refuses a wildcard or a host with a port", func() {
		_, err := egress.ParseRegistries([]byte(`{"version":1,"registries":[{"id":"x","title":"X","hosts":["*.example.com"]}]}`))
		Expect(err).To(HaveOccurred())
		_, err = egress.ParseRegistries([]byte(`{"version":1,"registries":[{"id":"x","title":"X","hosts":["example.com:443"]}]}`))
		Expect(err).To(HaveOccurred())
	})

	It("describes the presets to the model", func() {
		Expect(egress.Describe(egress.PresetRegistries, reg)).To(ContainSubstring("Python (PyPI)"))
		Expect(egress.Describe(egress.PresetOff, reg)).To(HavePrefix("Network for commands: off."))
	})
})
