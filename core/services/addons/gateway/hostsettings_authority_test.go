// SPDX-License-Identifier: MIT
package gateway

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/hostsettings"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

var _ = Describe("owner host settings authority", func() {
	It("projects private policy safely", func() { testSettingsProjection(GinkgoT()) })
	It("forwards authenticated owner decisions", func() { testSettingsForwarding(GinkgoT()) })
	It("validates strict request payloads", func() { testSettingsValidation(GinkgoT()) })
	It("checks CAS and administrator authority", func() { testSettingsAuthority(GinkgoT()) })
})

func testSettingsAuthority(t GinkgoTInterface) {
	ctx := context.Background()
	store, err := hostsettings.Open(filepath.Join(t.TempDir(), "host.json"), &hostsettings.Migration{Mode: hostsettings.Standalone, Source: "owner", Version: "1", Candidates: []hostsettings.Policy{{Ceiling: "off", Default: "off"}}})
	if err != nil {
		Expect(err).NotTo(HaveOccurred())
	}
	defer func() { Expect(store.Close()).To(Succeed()) }()
	admin := contract.Actor{Mode: "local-owner"}
	g := &Gateway{HostSettings: store, Revalidate: func(context.Context, contract.Actor) error { return nil }, HostSettingsAdmin: func(_ context.Context, a contract.Actor) error {
		if a != admin {
			return ErrForbidden
		}
		return nil
	}}
	s, err := g.HostSettingsSnapshot(ctx, admin)
	if err != nil || s.Revision != 1 {
		Fail(fmt.Sprintf("snapshot: %+v %v", s, err))
	}
	yes := true
	s, err = g.UpdateHostSettings(ctx, admin, hostsettings.Patch{AutoUpgradeAddons: &yes}, 1, "update-1")
	if err != nil || s.Revision != 2 {
		Fail(fmt.Sprintf("update: %+v %v", s, err))
	}
	_, err = g.UpdateHostSettings(ctx, admin, hostsettings.Patch{}, 1, "update-2")
	if !errors.Is(err, hostsettings.ErrConflict) {
		Fail(fmt.Sprintf("conflict: %v", err))
	}
	_, err = g.UpdateHostSettings(ctx, contract.Actor{UserID: "not-admin"}, hostsettings.Patch{}, 2, "update-3")
	if !errors.Is(err, ErrForbidden) {
		Fail(fmt.Sprintf("admin: %v", err))
	}
	r, err := g.Dispatch(ctx, admin, contract.Request{Action: ActionHostSettingsUpdate, OperationID: "bad", Arguments: json.RawMessage(`{"expectedRevision":2,"patch":{},"admin":true}`)})
	if err == nil {
		_, err = DecodeHostSettingsResult(r)
	}
	if !errors.Is(err, hostsettings.ErrInvalid) {
		Fail(fmt.Sprintf("malformed: %v", err))
	}
}

// This fixture intentionally uses real pinned mTLS and signed forwarding rather
// than a fake Dispatch: authority must be checked on the receiving owner.
func settingsCarrier(t GinkgoTInterface, owner *Gateway) (*httptest.Server, *Carrier) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		Expect(err).NotTo(HaveOccurred())
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, pub, key)
	if err != nil {
		Expect(err).NotTo(HaveOccurred())
	}
	ca, err = x509.ParseCertificate(der)
	if err != nil {
		Expect(err).NotTo(HaveOccurred())
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	certs := map[string]tls.Certificate{}
	pins := map[string][32]byte{}
	keys := map[string]ed25519.PublicKey{}
	private := map[string]ed25519.PrivateKey{}
	for i, id := range []string{"owner", "front"} {
		p, k, e := ed25519.GenerateKey(rand.Reader)
		if e != nil {
			Expect(e).NotTo(HaveOccurred())
		}
		c := &x509.Certificate{SerialNumber: big.NewInt(int64(i + 2)), NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
		d, e := x509.CreateCertificate(rand.Reader, c, ca, p, key)
		if e != nil {
			Expect(e).NotTo(HaveOccurred())
		}
		certs[id] = tls.Certificate{Certificate: [][]byte{d}, PrivateKey: k}
		pins[id] = sha256.Sum256(d)
		keys[id] = p
		private[id] = k
	}
	server := httptest.NewUnstartedServer(nil)
	carriers := map[string]*Carrier{}
	for _, id := range []string{"owner", "front"} {
		c, e := NewCarrier(CarrierConfig{InstanceID: id, OwnerID: "owner", OwnerAddress: "https://" + server.Listener.Addr().String(), Certificate: certs[id], Roots: roots, Pins: pins, SigningKey: private[id], VerificationKeys: keys})
		if e != nil {
			Expect(e).NotTo(HaveOccurred())
		}
		carriers[id] = c
	}
	h, err := carriers["owner"].OwnerHandler(owner)
	if err != nil {
		Expect(err).NotTo(HaveOccurred())
	}
	server.Config.Handler = h
	server.TLS = carriers["owner"].ServerTLS()
	server.StartTLS()
	t.Cleanup(server.Close)
	return server, carriers["front"]
}

type unavailableSettings struct{ err error }

func (s unavailableSettings) Snapshot(context.Context) (hostsettings.Snapshot, error) {
	return hostsettings.Snapshot{}, s.err
}
func (s unavailableSettings) Update(context.Context, hostsettings.Patch, uint64, string) (hostsettings.Snapshot, error) {
	return hostsettings.Snapshot{}, s.err
}

func testSettingsForwarding(t GinkgoTInterface) {
	ctx := context.Background()
	store, err := hostsettings.Open(filepath.Join(t.TempDir(), "host.json"), &hostsettings.Migration{Mode: hostsettings.Standalone, Source: "owner", Version: "1", Candidates: []hostsettings.Policy{{Ceiling: "off", Default: "off"}}})
	if err != nil {
		Expect(err).NotTo(HaveOccurred())
	}
	defer func() { Expect(store.Close()).To(Succeed()) }()
	admin := contract.Actor{UserID: "admin", CredentialID: "key:admin", Mode: "named-user"}
	var revoked atomic.Bool
	var ownerChecks atomic.Int32
	owner := &Gateway{HostSettings: store, Revalidate: func(_ context.Context, a contract.Actor) error {
		ownerChecks.Add(1)
		if revoked.Load() {
			return ErrForbidden
		}
		return nil
	}, HostSettingsAdmin: func(_ context.Context, a contract.Actor) error {
		if a != admin {
			return ErrForbidden
		}
		return nil
	}}
	server, carrier := settingsCarrier(t, owner)
	// Even a frontend configured with a stale store must always read the owner.
	front := &Gateway{Forward: carrier, HostSettings: unavailableSettings{hostsettings.ErrUnavailable}, Revalidate: func(context.Context, contract.Actor) error { return nil }, HostSettingsAdmin: func(context.Context, contract.Actor) error { return nil }}
	yes := true
	patch := hostsettings.Patch{AutoUpgradeAddons: &yes}
	s, err := front.UpdateHostSettings(ctx, admin, patch, 1, "same-op")
	if err != nil || s.Revision != 2 {
		Fail(fmt.Sprintf("forward update: %+v %v", s, err))
	}
	replay, err := front.UpdateHostSettings(ctx, admin, patch, 1, "same-op")
	if err != nil || !reflect.DeepEqual(replay, s) {
		Fail(fmt.Sprintf("receipt replay: %+v %v", replay, err))
	}
	_, err = front.UpdateHostSettings(ctx, admin, hostsettings.Patch{}, 1, "same-op")
	if !errors.Is(err, hostsettings.ErrOperationMismatch) {
		Fail(fmt.Sprintf("receipt mismatch: %v", err))
	}
	_, err = front.UpdateHostSettings(ctx, admin, hostsettings.Patch{}, 1, "stale")
	if !errors.Is(err, hostsettings.ErrConflict) {
		Fail(fmt.Sprintf("forward CAS: %v", err))
	}
	user := contract.Actor{UserID: "user", CredentialID: "key:user", Mode: "named-user"}
	_, err = front.UpdateHostSettings(ctx, user, patch, 2, "not-admin")
	if !errors.Is(err, ErrForbidden) {
		Fail(fmt.Sprintf("owner must ignore frontend admin approval: %v", err))
	}
	s, err = front.ReadHostSettings(ctx, user)
	if err != nil || s.Revision != 2 {
		Fail(fmt.Sprintf("authenticated read: %+v %v", s, err))
	}
	if ownerChecks.Load() < 12 {
		Fail(fmt.Sprintf("owner did not revalidate each forwarded request: %d", ownerChecks.Load()))
	}
	revoked.Store(true)
	_, err = front.ReadHostSettings(ctx, admin)
	if !errors.Is(err, ErrForbidden) {
		Fail(fmt.Sprintf("revoked actor: %v", err))
	}
	// Trusted host reads do not impersonate a user, even after revocation.
	s, err = front.HostSettingsInternalSnapshot(ctx)
	if err != nil || s.Revision != 2 {
		Fail(fmt.Sprintf("internal owner read: %+v %v", s, err))
	}
	// Internal reads still require the pinned client certificate.
	carrier.client.Transport.(*http.Transport).TLSClientConfig.Certificates = nil
	carrier.client.CloseIdleConnections()
	_, err = front.HostSettingsInternalSnapshot(ctx)
	if !errors.Is(err, ErrUnavailable) {
		Fail(fmt.Sprintf("untrusted internal read: %v", err))
	}
	server.Close()
	_, err = front.ReadHostSettings(ctx, admin)
	var unknown *OutcomeUnknown
	if !errors.Is(err, ErrUnavailable) || errors.As(err, &unknown) {
		Fail(fmt.Sprintf("read outage is not uncertain: %v", err))
	}
	_, err = front.UpdateHostSettings(ctx, admin, patch, 2, "lost")
	// Audience discovery fails before any mutation is sent. Preserve that
	// definite unavailable result rather than inventing an uncertain write.
	if !errors.Is(err, ErrUnavailable) || errors.As(err, &unknown) {
		Fail(fmt.Sprintf("pre-send mutation outage: %v", err))
	}
}

func testSettingsValidation(t GinkgoTInterface) {
	ctx := context.Background()
	actor := contract.Actor{Mode: "local-owner"}
	g := &Gateway{HostSettings: unavailableSettings{hostsettings.ErrUncertain}, Revalidate: func(context.Context, contract.Actor) error { return nil }, HostSettingsAdmin: func(context.Context, contract.Actor) error { return nil }}
	for _, raw := range []string{
		`{"expectedRevision":1,"patch":{},"admin":true}`, `{"ExpectedRevision":1,"patch":{}}`,
		`{"expectedRevision":1,"expectedRevision":2,"patch":{}}`, `{"expectedRevision":null,"patch":{}}`,
		`{"expectedRevision":1}`, `{"expectedRevision":1,"patch":null}`, `{"expectedRevision":1,"patch":{}} {}`,
		`{"expectedRevision":1,"patch":{"AUTO_UPGRADE_ADDONS":true}}`,
		`{"expectedRevision":1,"patch":{"auto_upgrade_addons":true,"auto_upgrade_addons":false}}`,
		`{"expectedRevision":1,"patch":{"admin":true}}`, `{"expectedRevision":0,"patch":{}}`,
	} {
		r, err := g.Dispatch(ctx, actor, contract.Request{Action: ActionHostSettingsUpdate, OperationID: "invalid", Arguments: json.RawMessage(raw)})
		if err == nil {
			_, err = DecodeHostSettingsResult(r)
		}
		if !errors.Is(err, hostsettings.ErrInvalid) {
			Fail(fmt.Sprintf("%s: %v", raw, err))
		}
	}
	for _, raw := range []string{`null`, `{"admin":true}`, `{} {}`} {
		r, err := g.Dispatch(ctx, actor, contract.Request{Action: ActionHostSettingsRead, Arguments: json.RawMessage(raw)})
		if err == nil {
			_, err = DecodeHostSettingsResult(r)
		}
		if !errors.Is(err, hostsettings.ErrInvalid) {
			Fail(fmt.Sprintf("read %s: %v", raw, err))
		}
	}
	_, err := g.UpdateHostSettings(ctx, actor, hostsettings.Patch{}, 1, "uncertain")
	var unknown *OutcomeUnknown
	if !errors.As(err, &unknown) || unknown.OperationID != "uncertain" {
		Fail(fmt.Sprintf("store uncertainty: %v", err))
	}
	g.HostSettings = nil
	_, err = g.ReadHostSettings(ctx, actor)
	if !errors.Is(err, ErrUnavailable) || errors.As(err, &unknown) {
		Fail(fmt.Sprintf("nil store read: %v", err))
	}
	_, err = g.HostSettingsInternalSnapshot(ctx)
	if !errors.Is(err, ErrUnavailable) {
		Fail(fmt.Sprintf("nil internal read: %v", err))
	}
	g.HostSettingsAdmin = nil
	_, err = g.UpdateHostSettings(ctx, actor, hostsettings.Patch{}, 1, "no-check")
	if !errors.Is(err, ErrForbidden) {
		Fail(fmt.Sprintf("missing admin check: %v", err))
	}
	g.Revalidate = nil
	_, err = g.ReadHostSettings(ctx, actor)
	if !errors.Is(err, ErrForbidden) {
		Fail(fmt.Sprintf("missing actor check: %v", err))
	}
	if mutation(ActionHostSettingsRead) || !mutation(ActionHostSettingsUpdate) || !mutation("future-host-action") {
		Fail("unsafe mutation classification")
	}
}

// Synthetic provenance must remain durable without becoming part of host policy.
func testSettingsProjection(t GinkgoTInterface) {
	for _, forwarded := range []bool{false, true} {
		func() {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "host.json")
			provenance := hostsettings.Provenance{Mode: string(hostsettings.Standalone), Source: "/synthetic-private/owner-config.json", Version: "synthetic-internal-release"}
			store, err := hostsettings.Open(path, &hostsettings.Migration{Mode: hostsettings.Mode(provenance.Mode), Source: provenance.Source, Version: provenance.Version, Candidates: []hostsettings.Policy{{Ceiling: "off", Default: "off"}}})
			if err != nil {
				Expect(err).NotTo(HaveOccurred())
			}
			defer func() { Expect(store.Close()).To(Succeed()) }()
			admin := contract.Actor{UserID: "admin", CredentialID: "key:admin", Mode: "named-user"}
			user := contract.Actor{UserID: "ordinary-user", CredentialID: "key:ordinary", Mode: "named-user"}
			owner := &Gateway{HostSettings: store, Revalidate: func(context.Context, contract.Actor) error { return nil }, HostSettingsAdmin: func(_ context.Context, a contract.Actor) error {
				if a != admin {
					return ErrForbidden
				}
				return nil
			}}
			g := owner
			if forwarded {
				server, carrier := settingsCarrier(t, owner)
				defer server.Close()
				g = &Gateway{Forward: carrier, Revalidate: func(context.Context, contract.Actor) error { return nil }}
			}
			assertProjection := func(value any, revision uint64) {
				t.Helper()
				raw, e := json.Marshal(value)
				if e != nil {
					Expect(e).NotTo(HaveOccurred())
				}
				var fields map[string]json.RawMessage
				if e = json.Unmarshal(raw, &fields); e != nil {
					Expect(e).NotTo(HaveOccurred())
				}
				if len(fields) != 3 || fields["schema_version"] == nil || fields["revision"] == nil || fields["policy"] == nil {
					Fail(fmt.Sprintf("policy projection must contain only schema, revision and policy: %s", raw))
				}
				var gotRevision uint64
				if json.Unmarshal(fields["revision"], &gotRevision) != nil || gotRevision != revision {
					Fail(fmt.Sprintf("revision: %s", raw))
				}
				if bytes.Contains(raw, []byte(provenance.Source)) || bytes.Contains(raw, []byte(provenance.Version)) || fields["migration"] != nil {
					Fail(fmt.Sprintf("projection exposes migration provenance: %s", raw))
				}
			}
			s, err := g.ReadHostSettings(ctx, user)
			if err != nil {
				Expect(err).NotTo(HaveOccurred())
			}
			assertProjection(s, 1)
			alias, err := g.HostSettingsSnapshot(ctx, user)
			if err != nil {
				Expect(err).NotTo(HaveOccurred())
			}
			assertProjection(alias, 1)
			// Inspect the actual action payload, not only the helper's decoded result.
			r, err := g.Dispatch(ctx, user, contract.Request{Action: ActionHostSettingsRead, OperationID: "projection-read", Arguments: json.RawMessage(`{}`)})
			if err != nil {
				Expect(err).NotTo(HaveOccurred())
			}
			var envelope map[string]json.RawMessage
			if err = json.Unmarshal(r.Payload, &envelope); err != nil {
				Expect(err).NotTo(HaveOccurred())
			}
			assertProjection(envelope["data"], 1)
			internal, err := g.HostSettingsInternalSnapshot(ctx)
			if err != nil {
				Expect(err).NotTo(HaveOccurred())
			}
			assertProjection(internal, 1)
			if forwarded {
				resp, e := g.Forward.client.Get(g.Forward.config.OwnerAddress + "/addons/host-settings")
				if e != nil {
					Expect(e).NotTo(HaveOccurred())
				}
				e = json.NewDecoder(resp.Body).Decode(&envelope)
				Expect(resp.Body.Close()).To(Succeed())
				if e != nil || resp.StatusCode != http.StatusOK {
					Fail(fmt.Sprintf("internal wire: %v %d", e, resp.StatusCode))
				}
				assertProjection(envelope["data"], 1)
			}
			yes := true
			patch := hostsettings.Patch{AutoUpgradeAddons: &yes}
			args, err := json.Marshal(map[string]any{"expectedRevision": 1, "patch": patch})
			if err != nil {
				Expect(err).NotTo(HaveOccurred())
			}
			for i := 0; i < 2; i++ {
				updated, e := g.UpdateHostSettings(ctx, admin, patch, 1, "projection-update")
				if e != nil {
					Expect(e).NotTo(HaveOccurred())
				}
				assertProjection(updated, 2)
				r, e = g.Dispatch(ctx, admin, contract.Request{Action: ActionHostSettingsUpdate, OperationID: "projection-update", Arguments: args})
				if e != nil {
					Expect(e).NotTo(HaveOccurred())
				}
				if e = json.Unmarshal(r.Payload, &envelope); e != nil {
					Expect(e).NotTo(HaveOccurred())
				}
				assertProjection(envelope["data"], 2)
			}
			// Reads and receipt replay must not strip provenance from private storage.
			private, err := store.Snapshot(ctx)
			if err != nil || private.Migration != provenance || private.Revision != 2 || !private.Policy.AutoUpgradeAddons {
				Fail(fmt.Sprintf("private snapshot: %+v %v", private, err))
			}
			disk, err := os.ReadFile(path)
			if err != nil {
				Expect(err).NotTo(HaveOccurred())
			}
			if !bytes.Contains(disk, []byte(provenance.Source)) || !bytes.Contains(disk, []byte(provenance.Version)) {
				Fail("durable provenance lost")
			}
			_, err = g.UpdateHostSettings(ctx, admin, hostsettings.Patch{}, 1, "projection-update")
			if !errors.Is(err, hostsettings.ErrOperationMismatch) {
				Fail(fmt.Sprintf("typed mismatch: %v", err))
			}
			_, err = g.UpdateHostSettings(ctx, admin, patch, 1, "projection-stale")
			if !errors.Is(err, hostsettings.ErrConflict) {
				Fail(fmt.Sprintf("typed conflict: %v", err))
			}
			_, err = g.UpdateHostSettings(ctx, user, patch, 2, "projection-denied")
			if !errors.Is(err, ErrForbidden) {
				Fail(fmt.Sprintf("typed forbidden: %v", err))
			}
		}()
	}
}
