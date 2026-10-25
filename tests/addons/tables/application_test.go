// SPDX-License-Identifier: MIT
package tables_test

import (
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
	"fmt"
	"io/fs"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	goruntime "runtime"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	httpapi "github.com/mudler/LocalAI/core/http"
	"github.com/mudler/LocalAI/core/http/auth"
	addonsvc "github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/policy"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/system"
	"github.com/mudler/LocalAI/tests/addons/foundation/cluster"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const appSecret = "tables-application-secret"

// ownerCarrier builds the smallest real transport identity of an owner: one
// pinned certificate, one signing key and a loopback listen address.
func ownerCarrier() *gateway.CarrierConfig {
	GinkgoHelper()
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	Expect(e).NotTo(HaveOccurred())
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
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

// host is one owner process-in-process: the real application bootstrap over
// real account storage, the real supervisor and the real Tables executable.
type host struct {
	dir    string
	owner  config.AddonConfig
	app    *application.Application
	web    *echo.Echo
	cancel context.CancelFunc
	// Credentials of the accounts, created once and kept across restarts.
	tokens   map[string]string
	sessions map[string]string
	keys     map[string]string
}

func newHost() *host {
	GinkgoHelper()
	dir := GinkgoT().TempDir()
	h := &host{dir: dir, tokens: map[string]string{}, sessions: map[string]string{}, keys: map[string]string{}}
	root := filepath.Join(dir, "addons")
	// Package directories are read-only; restore permissions before removal.
	DeferCleanup(func() {
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, e error) error {
			if e == nil && d.IsDir() {
				_ = os.Chmod(p, 0o700)
			}
			return nil
		})
	})
	h.owner = config.AddonConfig{
		Enabled: true, InstanceID: "owner", OwnerID: "owner", OwnerAddress: "https://owner.example", StorageID: "local",
		Root: root, ListenAddress: "127.0.0.1:0", Transport: ownerCarrier(),
		Host: contract.HostCompatibility{Host: 1, API: 1, Protocol: 1, SDK: 1, DataFormat: 1, OS: goruntime.GOOS, Arch: goruntime.GOARCH},
	}
	return h
}

// start runs the application bootstrap, which reconciles reservations and
// recovers proposals before it serves.
func (h *host) start() {
	GinkgoHelper()
	models, backends := filepath.Join(h.dir, "models"), filepath.Join(h.dir, "backends")
	Expect(os.MkdirAll(models, 0o750)).To(Succeed())
	Expect(os.MkdirAll(backends, 0o750)).To(Succeed())
	state, e := system.GetSystemState(system.WithBackendPath(backends), system.WithModelPath(models))
	Expect(e).NotTo(HaveOccurred())
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	app, e := application.New(
		config.WithContext(ctx), config.WithSystemState(state), config.WithDataPath(h.dir),
		config.WithAuthEnabled(true), config.WithAuthDatabaseURL(filepath.Join(h.dir, "auth.db")), config.WithAuthAPIKeyHMACSecret(appSecret),
		config.WithAddons(h.owner),
	)
	Expect(e).NotTo(HaveOccurred())
	h.app = app
	h.web, e = httpapi.API(app)
	Expect(e).NotTo(HaveOccurred())
}

// stop ends the application as a restart does and waits until the owner
// released its durable state.
func (h *host) stop() {
	GinkgoHelper()
	if h.cancel == nil {
		return
	}
	Expect(h.app.Shutdown()).To(Succeed())
	h.cancel()
	h.cancel = nil
	Eventually(func() error {
		s, e := policy.OpenStore(filepath.Join(h.owner.Root, "policy"), policy.ModeNamedUser)
		if e == nil {
			_ = s.Close()
		}
		return e
	}, 10*time.Second, 50*time.Millisecond).Should(Succeed())
}

func (h *host) actor(user string) contract.Actor {
	return contract.Actor{UserID: user, CredentialID: "key:" + h.keys[user], Mode: auth.AddonModeNamedUser}
}

// seed creates the accounts, installs the real Tables package from a local
// catalog and waits until it runs.
func (h *host) seed(lib *cluster.Library) {
	GinkgoHelper()
	ctx := context.Background()
	db := h.app.AuthDB()
	Expect(db).NotTo(BeNil())
	for _, u := range []struct {
		id, role string
		feature  bool
	}{{"alice", auth.RoleAdmin, false}, {"bob", auth.RoleUser, true}} {
		Expect(db.Create(&auth.User{ID: u.id, Email: u.id + "@tables.test", Name: u.id, Provider: auth.ProviderLocal, Role: u.role, Status: auth.StatusActive}).Error).To(Succeed())
		if u.feature {
			Expect(db.Create(&auth.UserPermission{ID: uuid.NewString(), UserID: u.id, Permissions: auth.PermissionMap{auth.FeatureAddons: true}}).Error).To(Succeed())
		}
		token, key, e := auth.CreateAPIKey(db, u.id, "agent", u.role, appSecret, nil)
		Expect(e).NotTo(HaveOccurred())
		session, e := auth.CreateSession(db, u.id, appSecret)
		Expect(e).NotTo(HaveOccurred())
		h.tokens[u.id], h.sessions[u.id], h.keys[u.id] = token, session, key.ID
	}
	admin := h.actor("alice")
	Expect(h.app.AddonSources().Save(ctx, admin, catalog.Source{ID: "local", Name: "Local", URL: lib.URL(), Enabled: true, AllowLoopbackHTTP: true})).To(Succeed())
	_, e := h.app.AddonSources().Refresh(ctx, "local")
	Expect(e).NotTo(HaveOccurred())
	m := h.app.AddonManager()
	_, e = m.Submit(ctx, admin, addonsvc.LifecycleCommand{OperationID: "install-tables", AddonID: "tables", Action: "install", SourceID: "local", Version: "0.1.0"})
	Expect(e).NotTo(HaveOccurred())
	_, e = m.Submit(ctx, admin, addonsvc.LifecycleCommand{OperationID: "enable-tables", AddonID: "tables", Action: "enable", Generation: 1})
	Expect(e).NotTo(HaveOccurred())
	h.running()
}

// running waits until the Tables process is observed running.
func (h *host) running() {
	GinkgoHelper()
	Eventually(func() string {
		list, e := h.app.AddonManager().Apps(context.Background(), h.actor("alice"))
		if e != nil {
			return e.Error()
		}
		for _, a := range list {
			if a.ID == "tables" {
				return a.State
			}
		}
		return "absent"
	}, 30*time.Second, 100*time.Millisecond).Should(Equal("running"))
}

type webReply struct {
	code int
	body map[string]any
}

func (h *host) send(method, path, body string, headers func(*http.Request)) webReply {
	GinkgoHelper()
	r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	if headers != nil {
		headers(r)
	}
	w := httptest.NewRecorder()
	h.web.ServeHTTP(w, r)
	out := webReply{code: w.Code, body: map[string]any{}}
	_ = json.Unmarshal(w.Body.Bytes(), &out.body)
	return out
}

func (h *host) bearer(user string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+h.tokens[user]) }
}

// humanTab is what the host confirmation surface sends: the user's own browser
// session, a same-origin fetch and the proposal named in the header.
func (h *host) humanTab(user, id string) func(*http.Request) {
	return func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: "session", Value: h.sessions[user]})
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		r.Header.Set("Sec-Fetch-Mode", "cors")
		r.Header.Set("Sec-Fetch-Dest", "empty")
		r.Header.Set("X-LocalAI-Confirm", id)
	}
}

func (h *host) chatCreate(user, op, name string) webReply {
	GinkgoHelper()
	body := fmt.Sprintf(`{"addonId":"tables","operationId":%q,"generation":1,"action":"create","arguments":{"tool":"databases-create","name":%q}}`, op, name)
	return h.send("POST", "/api/addons/proposals", body, h.bearer(user))
}

func (h *host) confirm(user, id, digest string) webReply {
	GinkgoHelper()
	return h.send("POST", "/api/addons/proposals/"+id+"/confirm", fmt.Sprintf(`{"digest":%q}`, digest), h.humanTab(user, id))
}

// databaseName reads the name of a database through the owner's gateway, as
// the person who may read it.
func (h *host) databaseName(user, resource string) (string, error) {
	res, e := h.app.AddonGateway().Dispatch(context.Background(), h.actor(user), contract.Request{
		AddonID: "tables", ResourceID: resource, OperationID: "inspect-" + uuid.NewString()[:8], Generation: 1, Action: "read",
		Arguments: json.RawMessage(`{"tool":"databases-inspect"}`),
	})
	if e != nil {
		return "", e
	}
	var out struct {
		Name string `json:"name"`
	}
	Expect(json.Unmarshal(res.Payload, &out)).To(Succeed())
	return out.Name, nil
}

func (h *host) library() *cluster.Library {
	GinkgoHelper()
	lib, e := cluster.NewLibrary(binary)
	Expect(e).NotTo(HaveOccurred())
	DeferCleanup(lib.Close)
	raw, e := os.ReadFile("../../../addons/tables/manifest.json")
	Expect(e).NotTo(HaveOccurred())
	var m struct {
		Capabilities map[string]any `json:"capabilities"`
	}
	Expect(json.Unmarshal(raw, &m)).To(Succeed())
	// These specs run the tools, not the page: the package carries no UI
	// files, so the declaration of them is left out. The UI suite installs
	// the whole package.
	delete(m.Capabilities, "ui")
	exe, e := os.ReadFile(binary)
	Expect(e).NotTo(HaveOccurred())
	Expect(lib.Add(cluster.Package{ID: "tables", Version: "0.1.0", Capabilities: m.Capabilities, Executable: "bin/tables", Binary: exe}, GinkgoT().TempDir())).To(Succeed())
	return lib
}

func accessibleTo(h *host, user string) []string {
	GinkgoHelper()
	keys, e := h.app.AddonPolicy().Accessible(context.Background(), h.actor(user), "tables")
	Expect(e).NotTo(HaveOccurred())
	var ids []string
	for _, k := range keys {
		ids = append(ids, k.ResourceID)
	}
	return ids
}

var _ = Describe("Tables in the application bootstrap", Label("application"), func() {
	It("creates a database from a chat proposal that only the proposer's session confirms", func() {
		h := newHost()
		lib := h.library()
		h.start()
		DeferCleanup(h.stop)
		h.seed(lib)

		p := h.chatCreate("alice", "chat-create-1", "Sales")
		Expect(p.code).To(Equal(200), "%v", p.body)
		Expect(p.body["state"]).To(Equal("pending"))
		Expect(p.body["action"]).To(Equal("create"))
		id, digest, resource := p.body["id"].(string), p.body["digest"].(string), p.body["resourceId"].(string)
		Expect(resource).NotTo(BeEmpty())
		// The reservation is reachable by nobody, not even by its proposer.
		Expect(accessibleTo(h, "alice")).To(BeEmpty())
		_, e := h.databaseName("alice", resource)
		Expect(e).To(HaveOccurred())
		// An API key and another user's session cannot approve it.
		Expect(h.send("POST", "/api/addons/proposals/"+id+"/confirm", fmt.Sprintf(`{"digest":%q}`, digest), h.bearer("alice")).code).To(Equal(403))
		Expect(h.confirm("bob", id, digest).code).To(Equal(403))
		Expect(accessibleTo(h, "alice")).To(BeEmpty())

		done := h.confirm("alice", id, digest)
		Expect(done.code).To(Equal(200), "%v", done.body)
		Expect(done.body["state"]).To(Equal("applied"))
		Expect(accessibleTo(h, "alice")).To(Equal([]string{resource}))
		Expect(accessibleTo(h, "bob")).To(BeEmpty())
		name, e := h.databaseName("alice", resource)
		Expect(e).NotTo(HaveOccurred())
		Expect(name).To(Equal("Sales"))
		_, e = h.databaseName("bob", resource)
		Expect(e).To(HaveOccurred())
		// A withdrawn proposal leaves nothing behind.
		q := h.chatCreate("alice", "chat-create-2", "Scratch")
		Expect(q.code).To(Equal(200))
		Expect(h.send("POST", "/api/addons/proposals/chat-create-2/cancel", "", h.bearer("alice")).code).To(Equal(200))
		pending, e := h.app.AddonPolicy().Pending()
		Expect(e).NotTo(HaveOccurred())
		Expect(pending).To(BeEmpty())
	})

	It("settles reservations at bootstrap by asking the real addon and keeps those of pending proposals", func() {
		h := newHost()
		lib := h.library()
		h.start()
		DeferCleanup(h.stop)
		h.seed(lib)
		ctx := context.Background()
		alice := h.actor("alice")
		p := h.app.AddonPolicy()
		m := h.app.AddonManager()

		// A crash after the addon created the database and before the commit.
		made, e := p.Register(ctx, alice, "tables")
		Expect(e).NotTo(HaveOccurred())
		create := policy.RuntimeCreate(m, m, contract.DefaultLimits(), alice, json.RawMessage(`{"tool":"databases-create","name":"Recovered"}`))
		Expect(create(ctx, made)).To(Succeed())
		// A crash after the reservation and before any database.
		lost, e := p.Register(ctx, alice, "tables")
		Expect(e).NotTo(HaveOccurred())
		// A chat proposal that nobody confirmed yet.
		live := h.chatCreate("alice", "chat-create-live", "Later")
		Expect(live.code).To(Equal(200), "%v", live.body)
		held := live.body["resourceId"].(string)
		before, e := p.Pending()
		Expect(e).NotTo(HaveOccurred())
		Expect(before).To(ConsistOf(made.ResourceID, lost.ResourceID, held))

		h.stop()
		h.start()
		h.running()

		p = h.app.AddonPolicy()
		// The real addon said the database exists: committed for its owner.
		Expect(accessibleTo(h, "alice")).To(Equal([]string{made.ResourceID}))
		name, e := h.databaseName("alice", made.ResourceID)
		Expect(e).NotTo(HaveOccurred())
		Expect(name).To(Equal("Recovered"))
		// It said nothing exists: dropped. The live proposal keeps its own.
		pending, e := p.Pending()
		Expect(e).NotTo(HaveOccurred())
		Expect(pending).To(Equal([]string{held}))
		// The proposal is still confirmable after the restart.
		got := h.send("GET", "/api/addons/proposals/chat-create-live", "", h.bearer("alice"))
		Expect(got.code).To(Equal(200), "%v", got.body)
		Expect(got.body["state"]).To(Equal("pending"))
		done := h.confirm("alice", "chat-create-live", got.body["digest"].(string))
		Expect(done.code).To(Equal(200), "%v", done.body)
		Expect(accessibleTo(h, "alice")).To(ConsistOf(made.ResourceID, held))
		name, e = h.databaseName("alice", held)
		Expect(e).NotTo(HaveOccurred())
		Expect(name).To(Equal("Later"))
	})
})
