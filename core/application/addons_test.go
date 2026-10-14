// SPDX-License-Identifier: MIT
package application

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/policy"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/xlog"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

var _ = Describe("addon bootstrap", func() {
	setup := func() *Application {
		return &Application{applicationConfig: &config.ApplicationConfig{Context: context.Background()}}
	}
	cfg := func() config.AddonConfig {
		return config.AddonConfig{Enabled: true, InstanceID: "owner", OwnerID: "owner", OwnerAddress: "https://owner.example", StorageID: "local", Root: filepath.Join(GinkgoT().TempDir(), "addons"), AdminCheck: func(context.Context, contract.Actor) error { return nil }}
	}
	It("installs from a persisted source through application bootstrap", func() {
		c := cfg()
		DeferCleanup(func() {
			_ = filepath.Walk(c.Root, func(p string, i os.FileInfo, e error) error {
				if e == nil && i.IsDir() {
					_ = os.Chmod(p, 0700)
				}
				return nil
			})
		})
		c.Host = contract.HostCompatibility{Host: 1, API: 1, Protocol: 1, SDK: 1, DataFormat: 1, OS: "linux", Arch: "amd64"}
		manifest, e := os.ReadFile("../../tests/addons/fixtures/packages/manifest.json")
		Expect(e).NotTo(HaveOccurred())
		var b bytes.Buffer
		tw := tar.NewWriter(&b)
		for name, data := range map[string][]byte{"manifest.json": manifest, "bin/sample": []byte("binary")} {
			Expect(tw.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: int64(len(data))})).To(Succeed())
			_, e = tw.Write(data)
			Expect(e).NotTo(HaveOccurred())
		}
		Expect(tw.Close()).To(Succeed())
		var url string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/package.tar" {
				_, _ = w.Write(b.Bytes())
				return
			}
			_ = json.NewEncoder(w).Encode([]catalog.Release{{AddonID: "sample", Name: "Sample", Version: "1.2.3", OS: "linux", Arch: "amd64", Artifact: url + "/package.tar", Digest: fmt.Sprintf("sha256:%x", sha256.Sum256(b.Bytes()))}})
		}))
		defer server.Close()
		url = server.URL
		Expect(os.MkdirAll(c.Root, 0700)).To(Succeed())
		sources, e := catalog.NewSources(filepath.Join(c.Root, "sources.json"), c.AdminCheck, catalog.CredentialResolver{})
		Expect(e).NotTo(HaveOccurred())
		Expect(sources.Save(context.Background(), contract.Actor{}, catalog.Source{ID: "test", Name: "Test", URL: url, Enabled: true, AllowLoopbackHTTP: true})).To(Succeed())
		a := setup()
		a.applicationConfig.Addons = c
		Expect(a.startAddons()).To(Succeed())
		DeferCleanup(a.Shutdown)
		_, e = a.AddonManager().Submit(context.Background(), contract.Actor{}, addons.LifecycleCommand{OperationID: "install", AddonID: "sample", Action: "install", SourceID: "test", Version: "1.2.3"})
		Expect(e).NotTo(HaveOccurred())
	})

	It("bounds offline refresh and ignores disabled sources and forwarders", func() {
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); <-r.Context().Done() }))
		defer server.Close()
		c := cfg()
		Expect(os.MkdirAll(c.Root, 0700)).To(Succeed())
		sources, e := catalog.NewSources(filepath.Join(c.Root, "sources.json"), c.AdminCheck, catalog.CredentialResolver{})
		Expect(e).NotTo(HaveOccurred())
		src := catalog.Source{ID: "offline", Name: "Offline", URL: server.URL, Enabled: false}
		Expect(sources.Save(context.Background(), contract.Actor{}, src)).To(Succeed())
		a := setup()
		a.applicationConfig.Addons = c
		Expect(a.startAddons()).To(Succeed())
		Expect(calls.Load()).To(BeZero())
		Expect(a.AddonSources()).NotTo(BeNil())
		_, e = a.AddonSources().Refresh(context.Background(), src.ID)
		Expect(e).To(MatchError(catalog.ErrUnavailable))
		Expect(a.Shutdown()).To(Succeed())
		src.Enabled = true
		Expect(sources.Save(context.Background(), contract.Actor{}, src)).To(Succeed())
		a = setup()
		a.applicationConfig.Addons = c
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		a.applicationConfig.Context = ctx
		Expect(a.startAddons()).NotTo(Succeed())
		Expect(a.AddonManager()).To(BeNil())
		Expect(a.AddonSources()).To(BeNil())
		Expect(calls.Load()).To(Equal(int32(1)))
		c.InstanceID = "frontend"
		a = setup()
		a.applicationConfig.Addons = c
		Expect(a.startAddons()).To(Succeed())
		Expect(a.AddonSources()).To(BeNil())
		Expect(calls.Load()).To(Equal(int32(1)))
		Expect(a.Shutdown()).To(Succeed())
		src.Enabled = false
		Expect(sources.Save(context.Background(), contract.Actor{}, src)).To(Succeed())
		c.InstanceID = c.OwnerID
		a = setup()
		a.applicationConfig.Addons = c
		Expect(a.startAddons()).To(Succeed())
		Expect(a.Shutdown()).To(Succeed())
	})
	It("rejects package-root links during bootstrap without external mutation", func() {
		c := cfg()
		Expect(os.MkdirAll(c.Root, 0700)).To(Succeed())
		external := GinkgoT().TempDir()
		stage := filepath.Join(external, "stage-keep")
		Expect(os.Mkdir(stage, 0700)).To(Succeed())
		DeferCleanup(func() { _ = os.Chmod(stage, 0700) })
		sentinel := filepath.Join(stage, "keep")
		Expect(os.WriteFile(sentinel, []byte("keep"), 0400)).To(Succeed())
		Expect(os.Chmod(stage, 0500)).To(Succeed())
		Expect(os.Symlink(external, filepath.Join(c.Root, "packages"))).To(Succeed())
		a := setup()
		a.applicationConfig.Addons = c
		Expect(a.startAddons()).NotTo(Succeed())
		Expect(a.AddonManager()).To(BeNil())
		b, e := os.ReadFile(sentinel)
		Expect(e).NotTo(HaveOccurred())
		Expect(string(b)).To(Equal("keep"))
		info, e := os.Stat(stage)
		Expect(e).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0500)))
	})

	It("leaves addons disabled by default without choosing an owner", func() { a := setup(); Expect(a.startAddons()).To(Succeed()); Expect(a.AddonManager()).To(BeNil()) })
	It("keeps forwarding bootstrap away from storage and rejects absent owners", func() {
		a := setup()
		c := cfg()
		c.InstanceID = "frontend"
		c.AdminCheck = nil
		a.applicationConfig.Addons = c
		Expect(a.startAddons()).To(Succeed())
		DeferCleanup(a.Shutdown)
		Expect(c.Root).NotTo(BeADirectory())
		Expect(a.AddonManager().Reconcile(context.Background())).To(MatchError(addons.ErrNotOwner))
		c.OwnerID = ""
		a = setup()
		a.applicationConfig.Addons = c
		Expect(a.startAddons()).NotTo(Succeed())
	})
	It("locks explicit owner storage and releases it on application shutdown", func() {
		c := cfg()
		a := setup()
		a.applicationConfig.Addons = c
		Expect(a.startAddons()).To(Succeed())
		b := setup()
		b.applicationConfig.Addons = c
		Expect(b.startAddons()).NotTo(Succeed())
		Expect(a.Shutdown()).To(Succeed())
		Expect(b.startAddons()).To(Succeed())
		Expect(b.Shutdown()).To(Succeed())
		c.StorageID = "conflict"
		a = setup()
		a.applicationConfig.Addons = c
		Expect(a.startAddons()).NotTo(Succeed())
	})
	It("reconciles interrupted intent before exposing the manager and cleans failed bootstrap", func() {
		c := cfg()
		a := setup()
		a.applicationConfig.Addons = c
		Expect(a.startAddons()).To(Succeed())
		Expect(a.Shutdown()).To(Succeed())
		p := filepath.Join(c.Root, "journal.json")
		Expect(os.WriteFile(p, []byte(`{"Version":1,"Addons":{},"Operations":{"op":{"Command":{"OperationID":"op","AddonID":"sample","Action":"install"},"Status":{"OperationID":"op","AddonID":"sample","Phase":"intent"}}}}`), 0600)).To(Succeed())
		a = setup()
		a.applicationConfig.Addons = c
		Expect(a.startAddons()).To(Succeed())
		Expect(a.Shutdown()).To(Succeed())
		b, e := os.ReadFile(p)
		Expect(e).NotTo(HaveOccurred())
		Expect(string(b)).To(ContainSubstring(`"Phase":"failed"`))
		Expect(os.WriteFile(p, []byte(`{`), 0600)).To(Succeed())
		a = setup()
		a.applicationConfig.Addons = c
		Expect(a.startAddons()).NotTo(Succeed())
		Expect(a.AddonManager()).To(BeNil())
		Expect(os.Remove(p)).To(Succeed())
		Expect(a.startAddons()).To(Succeed())
		Expect(a.Shutdown()).To(Succeed())
	})
	It("rejects shared storage and missing authoritative administration", func() {
		for _, shared := range []bool{true, false} {
			a := setup()
			c := cfg()
			c.SharedStorage = shared
			if !shared {
				c.AdminCheck = nil
			}
			a.applicationConfig.Addons = c
			Expect(a.startAddons()).NotTo(Succeed())
		}
	})
})

var _ = Describe("addon gateway bootstrap", func() {
	It("keeps public gateway unavailable without explicit transport bootstrap", func() {
		a := &Application{applicationConfig: &config.ApplicationConfig{Context: context.Background()}}
		Expect(a.AddonGateway()).To(BeNil())
	})
})

// ownerTransport builds the smallest real transport identity of an owner: one
// pinned certificate, one signing key and a loopback listen address.
func ownerTransport() *gateway.CarrierConfig {
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	Expect(e).NotTo(HaveOccurred())
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, e := x509.CreateCertificate(rand.Reader, ca, ca, pub, key)
	Expect(e).NotTo(HaveOccurred())
	ca, e = x509.ParseCertificate(der)
	Expect(e).NotTo(HaveOccurred())
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	lp, lk, e := ed25519.GenerateKey(rand.Reader)
	Expect(e).NotTo(HaveOccurred())
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "owner"}, NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
	d, e := x509.CreateCertificate(rand.Reader, leaf, ca, lp, key)
	Expect(e).NotTo(HaveOccurred())
	kd, e := x509.MarshalPKCS8PrivateKey(lk)
	Expect(e).NotTo(HaveOccurred())
	pair, e := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: d}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kd}))
	Expect(e).NotTo(HaveOccurred())
	sp, sk, e := ed25519.GenerateKey(rand.Reader)
	Expect(e).NotTo(HaveOccurred())
	return &gateway.CarrierConfig{InstanceID: "owner", OwnerID: "owner", OwnerAddress: "https://owner.example", Certificate: pair, Roots: pool,
		Pins: map[string][32]byte{"owner": sha256.Sum256(d)}, SigningKey: sk, VerificationKeys: map[string]ed25519.PublicKey{"owner": sp}}
}

type logCapture struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logCapture) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}
func (l *logCapture) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.buf.String() }

var _ = Describe("addon policy bootstrap", func() {
	setup := func() *Application {
		return &Application{applicationConfig: &config.ApplicationConfig{Context: context.Background()}}
	}
	base := func() config.AddonConfig {
		c := config.AddonConfig{Enabled: true, InstanceID: "owner", OwnerID: "owner", OwnerAddress: "https://owner.example", StorageID: "local", Root: filepath.Join(GinkgoT().TempDir(), "addons"), ListenAddress: "127.0.0.1:0", Transport: ownerTransport()}
		return c
	}
	authDB := func() *gorm.DB {
		db, e := auth.InitDB(filepath.Join(GinkgoT().TempDir(), "auth.db"))
		Expect(e).NotTo(HaveOccurred())
		return db
	}
	start := func(a *Application, c config.AddonConfig) error {
		a.applicationConfig.Addons = c
		return a.startAddons()
	}
	namedActor := func(db *gorm.DB) contract.Actor {
		Expect(db.Create(&auth.User{ID: "alice", Role: auth.RoleAdmin, Status: auth.StatusActive}).Error).To(Succeed())
		_, key, e := auth.CreateAPIKey(db, "alice", "test", auth.RoleAdmin, "secret", nil)
		Expect(e).NotTo(HaveOccurred())
		return contract.Actor{UserID: "alice", CredentialID: "key:" + key.ID, Mode: "named-user"}
	}
	policyDir := func(c config.AddonConfig) string { return filepath.Join(c.Root, "policy") }

	It("opens the policy on the owner and keeps frontends and hosts without it denying", func() {
		c := base()
		a := setup()
		a.authDB = authDB()
		Expect(start(a, c)).To(Succeed())
		DeferCleanup(a.Shutdown)
		Expect(a.AddonGateway()).NotTo(BeNil())
		Expect(a.AddonGateway().Admission).NotTo(BeNil())
		Expect(a.AddonGateway().Resolver).NotTo(BeNil())
		Expect(a.AddonPolicy()).NotTo(BeNil())
		// The owner holds the store: a second opener is refused.
		_, e := policy.OpenStore(policyDir(c), policy.ModeNamedUser)
		Expect(e).To(MatchError(policy.ErrStoreBusy))
		// Unknown resources deny through the installed provider.
		_, e = a.AddonGateway().Dispatch(context.Background(), namedActor(a.authDB), contract.Request{AddonID: "sample", ResourceID: "nope", OperationID: "op", Generation: 1, Action: "read", Arguments: []byte(`{}`)})
		Expect(errors.Is(e, gateway.ErrForbidden)).To(BeTrue())

		// A frontend forwards to the owner and never opens or even creates the store.
		f := base()
		f.InstanceID = "frontend"
		f.Transport.InstanceID = "frontend"
		f.Transport.Pins["frontend"] = f.Transport.Pins["owner"]
		f.Root = filepath.Join(GinkgoT().TempDir(), "frontend")
		b := setup()
		b.authDB = authDB()
		Expect(start(b, f)).To(Succeed())
		DeferCleanup(b.Shutdown)
		Expect(b.AddonGateway()).NotTo(BeNil())
		Expect(b.AddonGateway().Admission).To(BeNil())
		Expect(b.AddonGateway().Resolver).To(BeNil())
		Expect(b.AddonPolicy()).To(BeNil())
		Expect(policyDir(f)).NotTo(BeADirectory())

		// A host with no transport has no gateway and no policy.
		n := base()
		n.Transport = nil
		n.AdminCheck = func(context.Context, contract.Actor) error { return nil }
		n.Root = filepath.Join(GinkgoT().TempDir(), "plain")
		h := setup()
		Expect(start(h, n)).To(Succeed())
		DeferCleanup(h.Shutdown)
		Expect(h.AddonGateway()).To(BeNil())
		Expect(h.AddonPolicy()).To(BeNil())
		Expect(policyDir(n)).NotTo(BeADirectory())
	})

	It("requires the addon to be installed and enabled before a resource can be registered", func() {
		c := base()
		a := setup()
		a.authDB = authDB()
		Expect(start(a, c)).To(Succeed())
		DeferCleanup(a.Shutdown)
		actor := namedActor(a.authDB)
		// An addon that was never installed cannot own resources.
		_, e := a.AddonPolicy().Register(context.Background(), actor, "ghost")
		Expect(errors.Is(e, gateway.ErrForbidden)).To(BeTrue())
		// The decision follows the manager's installed and enabled state.
		status := &statusFixture{}
		authority := addonCreateAuthority(status, func(context.Context, contract.Actor) error { return nil })
		for _, row := range []struct {
			name   string
			status addons.Status
			err    error
			want   int
		}{
			{"absent", addons.Status{}, nil, 403},
			{"installed but disabled", addons.Status{Installed: true}, nil, 403},
			{"enabled", addons.Status{Installed: true, DesiredEnabled: true, StartEligible: true}, nil, 0},
			{"change in progress", addons.Status{Installed: true, DesiredEnabled: true}, nil, 403},
			{"journal needs recovery", addons.Status{}, addons.ErrRecoveryRequired, 503},
		} {
			status.s, status.err = row.status, row.err
			err := authority(context.Background(), actor, "sample")
			if row.want == 0 {
				Expect(err).NotTo(HaveOccurred(), row.name)
				continue
			}
			Expect(err).To(HaveOccurred(), row.name)
			Expect(gateway.StatusFor(err)).To(Equal(row.want), row.name)
		}
		Expect(addonCreateAuthority(nil, func(context.Context, contract.Actor) error { return nil })(context.Background(), actor, "sample")).NotTo(Succeed())
	})

	It("reconciles registrations left pending before the owner starts serving", func() {
		c := base()
		Expect(os.MkdirAll(c.Root, 0o700)).To(Succeed())
		store, e := policy.OpenStore(policyDir(c), policy.ModeNamedUser)
		Expect(e).NotTo(HaveOccurred())
		allow := func(context.Context, contract.Actor) error { return nil }
		p := policy.New(store, policy.Options{Revalidate: allow, CreateAuthority: func(context.Context, contract.Actor, string) error { return nil }})
		db := authDB()
		actor := namedActor(db)
		kept, e := p.Register(context.Background(), actor, "sample")
		Expect(e).NotTo(HaveOccurred())
		Expect(p.Commit(context.Background(), actor, kept)).To(Succeed())
		pending, e := p.Register(context.Background(), actor, "sample")
		Expect(e).NotTo(HaveOccurred())
		Expect(store.Close()).To(Succeed())

		a := setup()
		a.authDB = db
		Expect(start(a, c)).To(Succeed())
		DeferCleanup(a.Shutdown)
		// Committed work stays; the host default drops a registration whose
		// backing resource nobody can confirm.
		Expect(a.AddonPolicy().Authorize(context.Background(), actor, kept, policy.ActionRead)).To(Succeed())
		Expect(a.AddonPolicy().Commit(context.Background(), actor, pending)).NotTo(Succeed())
		Expect(a.applicationConfig.Addons.OwnershipMode).To(BeEmpty())
	})

	It("starts with a pending registration whose addon cannot answer and settles it when the addon is ready", func() {
		c := base()
		Expect(os.MkdirAll(c.Root, 0o700)).To(Succeed())
		store, e := policy.OpenStore(policyDir(c), policy.ModeNamedUser)
		Expect(e).NotTo(HaveOccurred())
		allow := func(context.Context, contract.Actor) error { return nil }
		p := policy.New(store, policy.Options{Revalidate: allow, CreateAuthority: func(context.Context, contract.Actor, string) error { return nil }})
		db := authDB()
		actor := namedActor(db)
		kept, e := p.Register(context.Background(), actor, "sample")
		Expect(e).NotTo(HaveOccurred())
		Expect(p.Commit(context.Background(), actor, kept)).To(Succeed())
		pending, e := p.Register(context.Background(), actor, "tables")
		Expect(e).NotTo(HaveOccurred())
		Expect(store.Close()).To(Succeed())

		var ready atomic.Bool
		ticks := make(chan time.Time)
		a := setup()
		a.authDB = db
		a.addonReconcileTick = ticks
		a.addonResourceExists = func(context.Context, policy.ResourceKey) (bool, error) {
			if !ready.Load() {
				return false, policy.ErrUnavailable
			}
			return true, nil
		}
		// A disabled or broken addon makes the check unavailable: the host
		// still starts, the committed resource stays and the record is kept.
		Expect(start(a, c)).To(Succeed())
		DeferCleanup(a.Shutdown)
		Expect(a.AddonPolicy().Authorize(context.Background(), actor, kept, policy.ActionRead)).To(Succeed())
		ids, _ := a.AddonPolicy().Pending()
		Expect(ids).To(Equal([]string{pending.ResourceID}))
		Expect(a.AddonPolicy().Authorize(context.Background(), actor, pending, policy.ActionRead)).NotTo(Succeed())
		// The addon becomes ready: the retry settles the kept record.
		ready.Store(true)
		ticks <- time.Time{}
		Eventually(func() error {
			return a.AddonPolicy().Authorize(context.Background(), actor, pending, policy.ActionRead)
		}).Should(Succeed())
		ids, _ = a.AddonPolicy().Pending()
		Expect(ids).To(BeEmpty())
	})

	It("keeps named-user mode by default and selects local-owner only explicitly with a warning", func() {
		logs := &logCapture{}
		xlog.SetLogger(xlog.NewLoggerWithHandler(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelWarn}), xlog.LogLevelWarn))
		DeferCleanup(func() { xlog.SetLogger(xlog.NewLogger(xlog.LogLevel("info"), "text")) })

		named := setup()
		named.authDB = authDB()
		Expect(start(named, base())).To(Succeed())
		DeferCleanup(named.Shutdown)
		Expect(named.AddonOwnershipMode()).To(Equal("named-user"))
		Expect(logs.String()).NotTo(ContainSubstring("local-owner"))
		// Named mode still refuses to run without the account database.
		noDB := setup()
		Expect(start(noDB, base())).NotTo(Succeed())

		c := base()
		c.OwnershipMode = "local-owner"
		a := setup() // no account database: local-owner mode does not need one
		Expect(start(a, c)).To(Succeed())
		DeferCleanup(a.Shutdown)
		Expect(a.AddonOwnershipMode()).To(Equal("local-owner"))
		Expect(logs.String()).To(ContainSubstring("local-owner"))
		Expect(logs.String()).To(ContainSubstring("unauthenticated callers share"))
		Expect(logs.String()).To(ContainSubstring("can reach this port can confirm addon proposals"))
		local := contract.Actor{UserID: "local-owner", CredentialID: "local-owner", Mode: "local-owner"}
		key, e := a.AddonPolicy().Register(context.Background(), local, "ghost")
		Expect(e).To(HaveOccurred(), "creation authority still needs an installed addon")
		_ = key
		// Sharing is disabled by the store mode.
		Expect(a.AddonPolicy().SetGrant(context.Background(), local, policy.ResourceKey{AddonID: "sample", ResourceID: "x"}, "bob", policy.RoleViewer)).NotTo(Succeed())

		bad := base()
		bad.OwnershipMode = "shared"
		Expect(start(setup(), bad)).NotTo(Succeed())
	})
})

type statusFixture struct {
	s   addons.Status
	err error
}

func (f *statusFixture) Status(context.Context, string) (addons.Status, error) { return f.s, f.err }

var _ = Describe("addon owner in a full application start", func() {
	It("starts the named-user gateway when authentication is enabled", func() {
		dir := GinkgoT().TempDir()
		models, backends := filepath.Join(dir, "models"), filepath.Join(dir, "backends")
		Expect(os.Mkdir(models, 0o750)).To(Succeed())
		Expect(os.Mkdir(backends, 0o750)).To(Succeed())
		state, e := system.GetSystemState(system.WithBackendPath(backends), system.WithModelPath(models))
		Expect(e).NotTo(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		root := filepath.Join(dir, "addons")
		DeferCleanup(func() {
			_ = filepath.Walk(root, func(p string, i os.FileInfo, e error) error {
				if e == nil && i.IsDir() {
					_ = os.Chmod(p, 0700)
				}
				return nil
			})
		})
		owner := config.AddonConfig{Enabled: true, InstanceID: "owner", OwnerID: "owner", OwnerAddress: "https://owner.example", StorageID: "local", Root: root, ListenAddress: "127.0.0.1:0", Transport: ownerTransport()}
		a, e := New(
			config.WithContext(ctx), config.WithSystemState(state), config.WithDataPath(dir),
			config.WithAuthEnabled(true), config.WithAuthDatabaseURL(filepath.Join(dir, "auth.db")), config.WithAuthAPIKeyHMACSecret("secret"),
			config.WithAddons(owner),
		)
		Expect(e).NotTo(HaveOccurred(), "the account database must exist before the owner starts: named-user mode checks it")
		DeferCleanup(a.Shutdown)
		Expect(a.AuthDB()).NotTo(BeNil())
		Expect(a.AddonGateway()).NotTo(BeNil())
		Expect(a.AddonManager()).NotTo(BeNil())
	})
})
