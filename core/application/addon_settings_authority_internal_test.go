// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"math/big"
	"net"
	"net/http/httptest"
	"path/filepath"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/hostsettings"
	"github.com/mudler/LocalAI/core/services/addons/toolargs"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/egress"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

func runTestAddonSettingsAuthorityConsumers() {
	ctx := context.Background()
	path := filepath.Join(GinkgoT().TempDir(), "policy.json")
	owner, err := hostsettings.Open(path, &hostsettings.Migration{Mode: hostsettings.Standalone, Source: "test", Version: "1", Candidates: []hostsettings.Policy{{Ceiling: "all", Default: "registries", Ports: "443", ExtraRegistries: "packages.example", AllowedHosts: "docs.example"}}})
	Expect(err).NotTo(HaveOccurred())
	defer func() { _ = owner.Close() }()
	st, err := store.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	// Each frontend uses a separate pinned carrier, not a local policy replica.
	var services []*studio.Service
	var checks []string
	var cards []string
	ownerGateway := &gateway.Gateway{HostSettings: owner}
	for _, local := range []string{"off", "all", "registries"} {
		_, carrier := consumerSettingsCarrier(ownerGateway)
		a := &Application{applicationConfig: &config.ApplicationConfig{Addons: config.AddonConfig{Enabled: true}, ChatsEgressCeiling: local, ChatsEgressDefault: local}, addonGateway: &gateway.Gateway{Forward: carrier}}
		svc, err := studio.New(studio.Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Harness: studiotest.NewGateHarness(), EgressSettings: a.studioEgressSettings})
		if err != nil {
			Fail(fmt.Sprint(err))
		}
		DeferCleanup(func() { _ = svc.Close(ctx) })
		services = append(services, svc)
	}
	for i, svc := range services {
		rec, err := svc.Create(ctx, "alice", studio.CreateInput{Model: "test"})
		if err != nil {
			Fail(fmt.Sprint(err))
		}
		if rec.Egress.Preset != egress.PresetRegistries {
			Fail(fmt.Sprintf("consumer %d default = %s, want owner registries", i, rec.Egress.Preset))
		}
		admin, err := svc.AdminEgress(ctx, "alice")
		if err != nil {
			Fail(fmt.Sprint(err))
		}
		if admin.Settings.Ceiling != egress.PresetAll || len(admin.Settings.ExtraRegistries) != 1 || len(admin.Settings.AllowedHosts) != 1 || len(admin.Settings.Ports) != 1 {
			Fail(fmt.Sprintf("consumer %d settings: %+v", i, admin.Settings))
		}
		// The real card callback must cap a saved grant against the current owner.
		if err := st.UpdateEgress(ctx, "alice", rec.ID, func(s *egress.ChatState) error {
			s.PutCard(egress.Card{ID: "card", Host: "docs.example", Port: 443, Kind: egress.KindConnect, State: egress.CardPending, AskedAt: time.Now()})
			return nil
		}); err != nil {
			Fail(fmt.Sprint(err))
		}
		if err := svc.EgressDecide(ctx, "alice", rec.ID, "card", studio.EgressChat); err != nil {
			Fail(fmt.Sprint(err))
		}
		if err := st.UpdateEgress(ctx, "alice", rec.ID, func(s *egress.ChatState) error {
			s.PutCard(egress.Card{ID: "pending", Host: "other.example", Port: 443, Kind: egress.KindConnect, State: egress.CardPending, AskedAt: time.Now()})
			return nil
		}); err != nil {
			Fail(fmt.Sprint(err))
		}
		cards = append(cards, rec.ID)
		// A saved chat with an all preset exercises enforcement without an approval wait.
		if err := st.CreateChat(ctx, session.ChatRecord{ID: "check" + rec.ID, Principal: "alice", Model: "test", Egress: egress.ChatState{Preset: egress.PresetAll}}); err != nil {
			Fail(fmt.Sprint(err))
		}
		checks = append(checks, "check"+rec.ID)
		v, err := svc.EgressCheck(ctx, "alice", toolargs.ActionResourceID("check"+rec.ID), "", egress.Request{Host: "docs.example", Port: 443, TLS: true, Kind: egress.KindConnect})
		if err != nil || v.Action != egress.Allow {
			Fail(fmt.Sprintf("owner allowed host: %+v %v", v, err))
		}
	}
	snap, err := owner.Snapshot(ctx)
	Expect(err).NotTo(HaveOccurred())
	off := "off"
	if _, err = owner.Update(ctx, hostsettings.Patch{Ceiling: &off, Default: &off}, snap.Revision, "revoke"); err != nil {
		Fail(fmt.Sprint(err))
	}
	// Tightening must affect the next request of already-running frontends.
	for i, svc := range services {
		rec, err := svc.Create(ctx, "alice", studio.CreateInput{Model: "test"})
		if err != nil || rec.Egress.Preset != egress.PresetOff {
			Fail(fmt.Sprintf("existing frontend default: %+v %v", rec, err))
		}
		admin, err := svc.AdminEgress(ctx, "alice")
		if err != nil || admin.Settings.Ceiling != egress.PresetOff {
			Fail(fmt.Sprintf("existing frontend admin: %+v %v", admin, err))
		}
		v, err := svc.EgressCheck(ctx, "alice", toolargs.ActionResourceID(checks[i]), "", egress.Request{Host: "docs.example", Port: 443, TLS: true})
		if err != nil || v.Action != egress.Block {
			Fail(fmt.Sprintf("existing frontend enforcement: %+v %v", v, err))
		}
		if err := svc.EgressDecide(ctx, "alice", cards[i], "pending", studio.EgressChat); !errors.Is(err, studio.ErrEgressHostAboveCeiling) {
			Fail(fmt.Sprintf("existing pending card after tightening: %v", err))
		}
	}
	if err = owner.Close(); err != nil {
		Fail(fmt.Sprint(err))
	}
	owner, err = hostsettings.Open(path, nil)
	Expect(err).NotTo(HaveOccurred())

	// Restart is verified through a new consumer, not a frontend config import.
	a := &Application{applicationConfig: &config.ApplicationConfig{Addons: config.AddonConfig{Enabled: true}, ChatsEgressCeiling: "all", ChatsEgressDefault: "all"}, addonGateway: &gateway.Gateway{HostSettings: owner}}
	svc, err := studio.New(studio.Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Harness: studiotest.NewGateHarness(), EgressSettings: a.studioEgressSettings})
	Expect(err).NotTo(HaveOccurred())
	defer func() { Expect(svc.Close(ctx)).To(Succeed()) }()
	rec, err := svc.Create(ctx, "alice", studio.CreateInput{Model: "test"})
	if err != nil || rec.Egress.Preset != egress.PresetOff {
		Fail(fmt.Sprintf("restart default %+v %v", rec, err))
	}
	_ = owner.Close()
	if _, err = svc.Create(ctx, "alice", studio.CreateInput{Model: "test"}); err == nil {
		Fail(fmt.Sprint("owner outage silently used frontend default"))
	}
	if _, err = svc.AdminEgress(ctx, "alice"); err == nil {
		Fail(fmt.Sprint("admin card masked owner outage"))
	}
	if _, err = svc.EgressCheck(ctx, "alice", toolargs.ActionResourceID(rec.ID), "", egress.Request{Host: "docs.example", Port: 443, TLS: true}); err == nil {
		Fail(fmt.Sprint("enforcement masked owner outage"))
	}
}

func runTestAddonSettingsAuthorityFallback() {
	ctx := context.Background()
	a := &Application{applicationConfig: &config.ApplicationConfig{ChatsEgressCeiling: "all", ChatsEgressDefault: "registries", ChatsEgressPorts: "443"}}
	set, err := a.studioEgressSettings(ctx)
	if err != nil || set.Ceiling != egress.PresetAll || set.Default != egress.PresetRegistries {
		Fail(fmt.Sprintf("addons disabled fallback: %+v %v", set, err))
	}
	a.applicationConfig.Addons.Enabled = true
	if _, err := a.studioEgressSettings(ctx); !errors.Is(err, gateway.ErrUnavailable) {
		Fail(fmt.Sprintf("dedicated unavailable owner: %v", err))
	}
}

func runTestAddonSettingsAuthorityPendingOutage() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	owner, err := hostsettings.Open(filepath.Join(GinkgoT().TempDir(), "policy.json"), &hostsettings.Migration{Mode: hostsettings.Standalone, Source: "test", Version: "1", Candidates: []hostsettings.Policy{{Ceiling: "all", Default: "off"}}})
	Expect(err).NotTo(HaveOccurred())
	defer func() { Expect(owner.Close()).To(Succeed()) }()
	_, carrier := consumerSettingsCarrier(&gateway.Gateway{HostSettings: owner})
	a := &Application{applicationConfig: &config.ApplicationConfig{Addons: config.AddonConfig{Enabled: true}, ChatsEgressCeiling: "all"}, addonGateway: &gateway.Gateway{Forward: carrier}}
	st, err := store.NewJSON("")
	Expect(err).NotTo(HaveOccurred())
	svc, err := studio.New(studio.Deps{Store: st, Provider: provider.New(), StateDir: GinkgoT().TempDir(), Harness: studiotest.NewGateHarness(), EgressSettings: a.studioEgressSettings, EgressCardWait: 8 * time.Second})
	Expect(err).NotTo(HaveOccurred())
	defer func() { Expect(svc.Close(context.Background())).To(Succeed()) }()
	rec, err := svc.Create(ctx, "alice", studio.CreateInput{Model: "test"})
	Expect(err).NotTo(HaveOccurred())
	type result struct {
		v   egress.Verdict
		err error
	}
	done := make(chan result, 1)
	go func() {
		v, err := svc.EgressCheck(ctx, "alice", toolargs.ActionResourceID(rec.ID), "op", egress.Request{Host: "example.com", Port: 443, TLS: true, Kind: egress.KindConnect})
		done <- result{v, err}
	}()
	var card egress.Card
	for card.ID == "" {
		saved, err := st.GetChat(ctx, "alice", rec.ID)
		if err != nil {
			Fail(fmt.Sprint(err))
		}
		if pending := saved.Egress.Pending(); len(pending) > 0 {
			card = pending[0]
			break
		}
		select {
		case r := <-done:
			Fail(fmt.Sprintf("check ended before pending: %+v", r))
		case <-ctx.Done():
			Fail(fmt.Sprint(ctx.Err()))
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := owner.Close(); err != nil {
		Fail(fmt.Sprint(err))
	}
	if err := svc.EgressDecide(ctx, "alice", rec.ID, card.ID, studio.EgressChat); !errors.Is(err, gateway.ErrUnavailable) {
		Fail(fmt.Sprintf("pending decision outage: %v", err))
	}
	select {
	case r := <-done:
		if !errors.Is(r.err, gateway.ErrUnavailable) || r.v.Action != egress.Block || r.v.Reason != "policy_unavailable" {
			Fail(fmt.Sprintf("pending wait outage: %+v %v", r.v, r.err))
		}
	case <-ctx.Done():
		Fail(fmt.Sprint("pending wait masked outage", ctx.Err()))
	}
	saved, err := st.GetChat(ctx, "alice", rec.ID)
	Expect(err).NotTo(HaveOccurred())
	if len(saved.Egress.Rules) != 0 || len(saved.Egress.Once) != 0 {
		Fail(fmt.Sprint("outage persisted a cached allow"))
	}
}

// consumerSettingsCarrier uses real pinned mTLS for each independent frontend.
func consumerSettingsCarrier(owner *gateway.Gateway) (*httptest.Server, *gateway.Carrier) {
	owner.Revalidate = func(context.Context, contract.Actor) error { return nil }
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	Expect(err).NotTo(HaveOccurred())
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, pub, key)
	Expect(err).NotTo(HaveOccurred())
	ca, err = x509.ParseCertificate(der)
	Expect(err).NotTo(HaveOccurred())
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	certs := map[string]tls.Certificate{}
	pins := map[string][32]byte{}
	keys := map[string]ed25519.PublicKey{}
	private := map[string]ed25519.PrivateKey{}
	for i, id := range []string{"owner", "front"} {
		p, k, e := ed25519.GenerateKey(rand.Reader)
		if e != nil {
			Fail(fmt.Sprint(e))
		}
		c := &x509.Certificate{SerialNumber: big.NewInt(int64(i + 2)), NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
		d, e := x509.CreateCertificate(rand.Reader, c, ca, p, key)
		if e != nil {
			Fail(fmt.Sprint(e))
		}
		certs[id] = tls.Certificate{Certificate: [][]byte{d}, PrivateKey: k}
		pins[id] = sha256.Sum256(d)
		keys[id] = p
		private[id] = k
	}
	server := httptest.NewUnstartedServer(nil)
	carriers := map[string]*gateway.Carrier{}
	for _, id := range []string{"owner", "front"} {
		c, e := gateway.NewCarrier(gateway.CarrierConfig{InstanceID: id, OwnerID: "owner", OwnerAddress: "https://" + server.Listener.Addr().String(), Certificate: certs[id], Roots: roots, Pins: pins, SigningKey: private[id], VerificationKeys: keys})
		if e != nil {
			Fail(fmt.Sprint(e))
		}
		carriers[id] = c
	}
	h, err := carriers["owner"].OwnerHandler(owner)
	Expect(err).NotTo(HaveOccurred())
	server.Config.Handler = h
	server.TLS = carriers["owner"].ServerTLS()
	server.StartTLS()
	DeferCleanup(server.Close)
	return server, carriers["front"]
}

var _ = Describe("add-on settings authority", Label("addon-settings-authority"), func() {
	It("TestAddonSettingsAuthorityConsumers", runTestAddonSettingsAuthorityConsumers)
	It("TestAddonSettingsAuthorityFallback", runTestAddonSettingsAuthorityFallback)
	It("TestAddonSettingsAuthorityPendingOutage", runTestAddonSettingsAuthorityPendingOutage)
})
