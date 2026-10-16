// SPDX-License-Identifier: MIT
package recovery_test

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
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	httpapi "github.com/mudler/LocalAI/core/http"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/http/routes"
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

func TestRecovery(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Add-on data recovery")
}

var binary string

var _ = BeforeSuite(func() {
	binary = filepath.Join(GinkgoT().TempDir(), "tables")
	cmd := exec.Command("go", "build", "-o", binary, "../../../addons/tables/cmd/tables")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=1")
	out, e := cmd.CombinedOutput()
	Expect(e).NotTo(HaveOccurred(), string(out))
})

const appSecret = "tables-admin-secret"

// transports builds the transport identities of one owner and one forwarding
// frontend: a CA, two pinned leaves and two signing keys.
func transports(port int) (owner, front *gateway.CarrierConfig) {
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
	pins := map[string][32]byte{}
	verify := map[string]ed25519.PublicKey{}
	pairs := map[string]tls.Certificate{}
	signing := map[string]ed25519.PrivateKey{}
	for i, id := range []string{"owner", "front-a"} {
		lp, lk, e := ed25519.GenerateKey(rand.Reader)
		Expect(e).NotTo(HaveOccurred())
		leaf := &x509.Certificate{SerialNumber: big.NewInt(int64(i + 2)), Subject: pkix.Name{CommonName: id}, NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
		d, e := x509.CreateCertificate(rand.Reader, leaf, ca, lp, key)
		Expect(e).NotTo(HaveOccurred())
		kd, e := x509.MarshalPKCS8PrivateKey(lk)
		Expect(e).NotTo(HaveOccurred())
		pair, e := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: d}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kd}))
		Expect(e).NotTo(HaveOccurred())
		sp, sk, e := ed25519.GenerateKey(rand.Reader)
		Expect(e).NotTo(HaveOccurred())
		pins[id], verify[id], pairs[id], signing[id] = sha256.Sum256(d), sp, pair, sk
	}
	address := fmt.Sprintf("https://127.0.0.1:%d", port)
	mk := func(id string) *gateway.CarrierConfig {
		return &gateway.CarrierConfig{InstanceID: id, OwnerID: "owner", OwnerAddress: address, Certificate: pairs[id], Roots: pool, Pins: pins, SigningKey: signing[id], VerificationKeys: verify}
	}
	return mk("owner"), mk("front-a")
}

func freePort() int {
	GinkgoHelper()
	l, e := net.Listen("tcp", "127.0.0.1:0")
	Expect(e).NotTo(HaveOccurred())
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

// person is an account of the fixture.
type person struct {
	id, role string
	feature  bool
	status   string
}

var people = []person{
	{"alice", auth.RoleAdmin, false, auth.StatusActive},
	{"bob", auth.RoleUser, true, auth.StatusActive},
	{"carol", auth.RoleUser, true, auth.StatusActive},
	{"dave", auth.RoleUser, true, auth.StatusActive},
	{"erin", auth.RoleUser, false, auth.StatusActive},
	{"frank", auth.RoleUser, true, auth.StatusPending},
}

// host is one owner in process: the real application bootstrap over real
// account storage, the real supervisor and the real Tables executable.
type host struct {
	dir        string
	libraryURL string
	owner      config.AddonConfig
	front      *gateway.CarrierConfig
	app        *application.Application
	web        *echo.Echo
	cancel     context.CancelFunc
	local      bool
	tokens     map[string]string
	sessions   map[string]string
	keys       map[string]string
}

func newHost(local bool) *host {
	GinkgoHelper()
	dir := GinkgoT().TempDir()
	h := &host{dir: dir, local: local, tokens: map[string]string{}, sessions: map[string]string{}, keys: map[string]string{}}
	DeferCleanup(func() {
		_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, e error) error {
			if e == nil && d.IsDir() {
				_ = os.Chmod(p, 0o700)
			}
			return nil
		})
	})
	port := freePort()
	var owner *gateway.CarrierConfig
	owner, h.front = transports(port)
	h.owner = config.AddonConfig{
		Enabled: true, InstanceID: "owner", OwnerID: "owner", OwnerAddress: owner.OwnerAddress, StorageID: "local",
		Root: filepath.Join(dir, "addons"), ListenAddress: fmt.Sprintf("127.0.0.1:%d", port), Transport: owner,
		Host: contract.HostCompatibility{Host: 1, API: 1, Protocol: 1, SDK: 1, DataFormat: 1, OS: goruntime.GOOS, Arch: goruntime.GOARCH},
	}
	if local {
		h.owner.OwnershipMode = auth.AddonModeLocalOwner
	}
	return h
}

func (h *host) mode() string {
	if h.local {
		return auth.AddonModeLocalOwner
	}
	return auth.AddonModeNamedUser
}

func (h *host) start() {
	GinkgoHelper()
	models, backends := filepath.Join(h.dir, "models"), filepath.Join(h.dir, "backends")
	Expect(os.MkdirAll(models, 0o750)).To(Succeed())
	Expect(os.MkdirAll(backends, 0o750)).To(Succeed())
	state, e := system.GetSystemState(system.WithBackendPath(backends), system.WithModelPath(models))
	Expect(e).NotTo(HaveOccurred())
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	opts := []config.AppOption{config.WithContext(ctx), config.WithSystemState(state), config.WithDataPath(h.dir), config.WithAddons(h.owner)}
	if !h.local {
		opts = append(opts, config.WithAuthEnabled(true), config.WithAuthDatabaseURL(filepath.Join(h.dir, "auth.db")), config.WithAuthAPIKeyHMACSecret(appSecret))
	}
	app, e := application.New(opts...)
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
	mode := policy.ModeNamedUser
	if h.local {
		mode = policy.ModeLocalOwner
	}
	Eventually(func() error {
		s, e := policy.OpenStore(filepath.Join(h.owner.Root, "policy"), mode)
		if e == nil {
			_ = s.Close()
		}
		return e
	}, 10*time.Second, 50*time.Millisecond).Should(Succeed())
}

func (h *host) actor(user string) contract.Actor {
	if h.local {
		return contract.Actor{UserID: auth.AddonLocalOwnerID, CredentialID: auth.AddonLocalOwnerID, Mode: auth.AddonModeLocalOwner}
	}
	return contract.Actor{UserID: user, CredentialID: "key:" + h.keys[user], Mode: auth.AddonModeNamedUser}
}

// seed creates the accounts, installs the real Tables package from a local
// catalog and waits until it runs.
func (h *host) seed(lib *cluster.Library) {
	GinkgoHelper()
	ctx := context.Background()
	if !h.local {
		db := h.app.AuthDB()
		Expect(db).NotTo(BeNil())
		for _, u := range people {
			Expect(db.Create(&auth.User{ID: u.id, Email: u.id + "@tables.test", Name: "Name of " + u.id, Provider: auth.ProviderLocal, Role: u.role, Status: u.status}).Error).To(Succeed())
			if u.feature {
				Expect(db.Create(&auth.UserPermission{ID: uuid.NewString(), UserID: u.id, Permissions: auth.PermissionMap{auth.FeatureAddons: true}}).Error).To(Succeed())
			}
			token, key, e := auth.CreateAPIKey(db, u.id, "agent", u.role, appSecret, nil)
			Expect(e).NotTo(HaveOccurred())
			session, e := auth.CreateSession(db, u.id, appSecret)
			Expect(e).NotTo(HaveOccurred())
			h.tokens[u.id], h.sessions[u.id], h.keys[u.id] = token, session, key.ID
		}
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

func (h *host) library() *cluster.Library {
	GinkgoHelper()
	lib, e := cluster.NewLibrary(binary)
	Expect(e).NotTo(HaveOccurred())
	DeferCleanup(lib.Close)
	h.libraryURL = lib.URL()
	raw, e := os.ReadFile("../../../addons/tables/manifest.json")
	Expect(e).NotTo(HaveOccurred())
	var m struct {
		Capabilities map[string]any `json:"capabilities"`
	}
	Expect(json.Unmarshal(raw, &m)).To(Succeed())
	// These specs run the tools, not the page: the package carries no UI
	// files, so the declaration of them is left out.
	delete(m.Capabilities, "ui")
	exe, e := os.ReadFile(binary)
	Expect(e).NotTo(HaveOccurred())
	for _, v := range []string{"0.1.0", "0.1.1"} {
		Expect(lib.Add(cluster.Package{ID: "tables", Version: v, Capabilities: m.Capabilities, Executable: "bin/tables", Binary: exe}, GinkgoT().TempDir())).To(Succeed())
	}
	// A release whose process fails at start: its upgrade must be undone.
	Expect(lib.Add(cluster.Package{ID: "tables", Version: "0.2.0", Capabilities: m.Capabilities, Executable: "bin/tables", Binary: []byte("#!/bin/sh\nexit 9\n")}, GinkgoT().TempDir())).To(Succeed())
	return lib
}

// ready is the usual start of a spec: a running host with the accounts and
// the package.
func ready(local bool) *host {
	GinkgoHelper()
	h := newHost(local)
	lib := h.library()
	h.start()
	DeferCleanup(h.stop)
	h.seed(lib)
	return h
}

// reply is one HTTP answer.
type reply struct {
	code int
	body map[string]any
	raw  string
}

func (r reply) errType() string {
	e, _ := r.body["error"].(map[string]any)
	s, _ := e["type"].(string)
	return s
}

func serve(e *echo.Echo, method, path, body string, headers ...func(*http.Request)) reply {
	GinkgoHelper()
	r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	for _, h := range headers {
		h(r)
	}
	w := httptest.NewRecorder()
	e.ServeHTTP(w, r)
	out := reply{code: w.Code, body: map[string]any{}, raw: w.Body.String()}
	_ = json.Unmarshal(w.Body.Bytes(), &out.body)
	return out
}

func (h *host) send(method, path, body string, headers ...func(*http.Request)) reply {
	GinkgoHelper()
	return serve(h.web, method, path, body, headers...)
}

func (h *host) bearer(user string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+h.tokens[user]) }
}

// tab is what the host's own pages send: the user's browser session, a
// same-origin fetch and the identifier the action concerns in the header. In
// local-owner mode there is no session.
func (h *host) tab(user, id string) func(*http.Request) {
	return func(r *http.Request) {
		if !h.local {
			r.AddCookie(&http.Cookie{Name: "session", Value: h.sessions[user]})
		}
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		r.Header.Set("Sec-Fetch-Mode", "cors")
		r.Header.Set("Sec-Fetch-Dest", "empty")
		r.Header.Set("X-LocalAI-Confirm", id)
	}
}

// session is a plain same-origin session request with no confirmation header.
func (h *host) session(user string) func(*http.Request) {
	return func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: "session", Value: h.sessions[user]})
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		r.Header.Set("Sec-Fetch-Mode", "cors")
		r.Header.Set("Sec-Fetch-Dest", "empty")
	}
}

// frontend is a second LocalAI instance that holds no addon state and
// forwards every resource call to the owner over the real carrier.
func (h *host) frontend() *echo.Echo {
	GinkgoHelper()
	carrier, e := gateway.NewCarrier(*h.front)
	Expect(e).NotTo(HaveOccurred())
	var db = h.app.AuthDB()
	g := &gateway.Gateway{Forward: carrier, Revalidate: auth.AddonRevalidate(db, h.mode())}
	web := echo.New()
	web.Use(auth.Middleware(db, h.app.ApplicationConfig()))
	routes.RegisterAddonRoutesFor(web, g, db, h.mode())
	return web
}

// Typed calls of the management routes, as the person's own browser tab.

func (h *host) create(user, op, name string) reply {
	GinkgoHelper()
	body := fmt.Sprintf(`{"addonId":"tables","operationId":%q,"generation":1,"arguments":{"name":%q}}`, op, name)
	return h.send("POST", "/api/addons/resources", body, h.tab(user, op))
}

func (h *host) list(user string) reply {
	GinkgoHelper()
	return h.send("GET", "/api/addons/resources?addonId=tables", "", h.bearer(user))
}

// resources returns the listed resources by ID.
func resourcesOf(r reply) map[string]map[string]any {
	GinkgoHelper()
	out := map[string]map[string]any{}
	list, _ := r.body["resources"].([]any)
	for _, x := range list {
		m := x.(map[string]any)
		out[m["id"].(string)] = m
	}
	return out
}

func (h *host) grant(user, res, target, role string) reply {
	GinkgoHelper()
	return h.send("PUT", "/api/addons/resources/"+res+"/grants/"+target, fmt.Sprintf(`{"role":%q}`, role), h.tab(user, res))
}

func (h *host) revoke(user, res, target string) reply {
	GinkgoHelper()
	return h.send("DELETE", "/api/addons/resources/"+res+"/grants/"+target, "", h.tab(user, res))
}

func (h *host) grants(user, res string) reply {
	GinkgoHelper()
	return h.send("GET", "/api/addons/resources/"+res+"/grants", "", h.tab(user, res))
}

func (h *host) rename(user, res, op, name string, rev map[string]any) reply {
	GinkgoHelper()
	b, _ := json.Marshal(map[string]any{"operationId": op, "generation": 1, "revision": rev, "name": name})
	return h.send("POST", "/api/addons/resources/"+res+"/rename", string(b), h.tab(user, res))
}

// revision reads the revision the owner role is shown for a resource.
func (h *host) revision(user, res string) map[string]any {
	GinkgoHelper()
	got := resourcesOf(h.list(user))[res]
	Expect(got).NotTo(BeNil())
	rev, _ := got["revision"].(map[string]any)
	Expect(rev).NotTo(BeNil())
	return rev
}

// name reads the display name of a resource through the owner's gateway, as
// the person who may read it.
func (h *host) name(user, res string) (string, error) {
	out, e := h.app.AddonGateway().Dispatch(context.Background(), h.actor(user), contract.Request{
		AddonID: "tables", ResourceID: res, OperationID: "inspect-" + uuid.NewString()[:8], Generation: h.currentGeneration(), Action: "read",
		Arguments: json.RawMessage(`{"tool":"databases-inspect"}`),
	})
	if e != nil {
		return "", e
	}
	var v struct {
		Name string `json:"name"`
	}
	Expect(json.Unmarshal(out.Payload, &v)).To(Succeed())
	return v.Name, nil
}

// database creates a database for the user and returns its ID.
func (h *host) database(user, op, name string) string {
	GinkgoHelper()
	got := h.create(user, op, name)
	Expect(got.code).To(Equal(200), got.raw)
	id, _ := got.body["id"].(string)
	Expect(id).NotTo(BeEmpty())
	return id
}

func (h *host) dbFile(res string) string {
	return filepath.Join(h.owner.Root, "data", "tables", "tables", "databases", res+".db")
}

func (h *host) currentGeneration() uint64 {
	st, e := h.app.AddonManager().Status(context.Background(), "tables")
	Expect(e).NotTo(HaveOccurred())
	return st.Generation
}
