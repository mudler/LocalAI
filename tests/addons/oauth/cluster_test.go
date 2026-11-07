// SPDX-License-Identifier: MIT
//go:build auth

package oauth_test

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	localhttp "github.com/mudler/LocalAI/core/http"
	"github.com/mudler/LocalAI/core/http/auth"
	addonsvc "github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/oauthtoken/oauthtest"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gorm.io/gorm"
)

const (
	hmacSecret  = "oauth-conformance-hmac"
	externalURL = "https://localai.example.test"
	callbackURL = externalURL + "/api/chats/connectors/oauth/callback"
	returnTo    = "/app/chats/connectors"
)

// service is one OAuth service and the add-on that logs in to it. Nothing
// about it is known to the host: the suite turns it into a manifest and a
// fake service, and the host sees only those.
type service struct {
	// AddonID and Kind name the add-on and its connector kind.
	AddonID, Kind, Title string
	// Hosts are the host patterns of the descriptor; Authorize and Token are
	// the hosts of the two endpoints (the revocation endpoint shares the
	// token host).
	Hosts                    []string
	AuthorizeHost, TokenHost string
	// MovedTokenHost is where a later release of the add-on moves the token
	// endpoint.
	MovedTokenHost string
	ClientAuth     string
	ClientID       string
	ClientSecret   string
	Required       []string
	Optional       []string
	Params         []contract.OAuthParam
	// AuthorizePath is the path of the authorize endpoint; empty is
	// /authorize.
	AuthorizePath string
	// Manifest, when set, makes the manifest of the add-on at a version in
	// place of the generic one, and Executable names the file of the package
	// that holds the executable and its content. A shipped add-on sets them.
	Manifest   func(version string, moved bool) []byte
	Executable func() (name string, data []byte)
}

// services are the services every spec runs against. They differ in every
// field the descriptor has, and in the client authentication.
var services = []service{
	{
		AddonID: "provider-a", Kind: "providera", Title: "Provider A",
		Hosts: []string{"login.provider-a.example.test", "moved.provider-a.example.test"}, AuthorizeHost: "login.provider-a.example.test", TokenHost: "login.provider-a.example.test",
		MovedTokenHost: "moved.provider-a.example.test",
		ClientAuth:     contract.OAuthClientAuthBasic, ClientID: "client-a", ClientSecret: "secret-a-sentinel",
		Required: []string{"profile.read"}, Optional: []string{"files.write"},
		Params: []contract.OAuthParam{{Name: "prompt", Value: "consent"}, {Name: "access_type", Value: "offline"}},
	},
	{
		AddonID: "provider-b", Kind: "providerb", Title: "Provider B",
		Hosts: []string{"*.provider-b.example.test"}, AuthorizeHost: "id.provider-b.example.test", TokenHost: "api.provider-b.example.test",
		MovedTokenHost: "elsewhere.provider-b.example.test",
		ClientAuth:     contract.OAuthClientAuthPost, ClientID: "client-b@provider-b", ClientSecret: "secret b/with+marks-sentinel",
		Required: []string{"openid", "items:read"}, Optional: []string{"items:write", "offline"},
		Params: []contract.OAuthParam{{Name: "audience", Value: "https://api.provider-b.example.test/items"}},
	},
	{
		AddonID: "provider-c", Kind: "providerc", Title: "Provider C",
		Hosts: []string{"auth.provider-c.example.test", "tokens.provider-c.example.test"}, AuthorizeHost: "auth.provider-c.example.test", TokenHost: "auth.provider-c.example.test",
		MovedTokenHost: "tokens.provider-c.example.test",
		ClientAuth:     contract.OAuthClientAuthNone, ClientID: "public-client-c",
		Required: []string{"read"},
		Params:   []contract.OAuthParam{{Name: "resource", Value: "urn:provider-c:api"}, {Name: "include_granted_scopes", Value: "true"}},
	},
}

// descriptor is the OAuth descriptor of the service; moved says whether the
// token endpoint is at its later address.
func (s service) descriptor(moved bool) contract.OAuthDescriptor {
	token := s.TokenHost
	if moved {
		token = s.MovedTokenHost
	}
	return contract.OAuthDescriptor{
		AuthorizeURL: "https://" + s.AuthorizeHost + "/authorize", TokenURL: "https://" + token + "/token", RevokeURL: "https://" + token + "/revoke",
		Hosts: s.Hosts, Scopes: contract.OAuthScopes{Required: s.Required, Optional: s.Optional},
		PKCE: contract.OAuthPKCES256, AuthorizeParams: s.Params, ClientAuth: s.ClientAuth,
	}
}

func (s service) scopes() []string { return append(append([]string{}, s.Required...), s.Optional...) }

// manifest is the manifest of the add-on of the service at a version.
func (s service) manifest(version string, moved bool) []byte {
	if s.Manifest != nil {
		return s.Manifest(version, moved)
	}
	desc := s.descriptor(moved)
	m := map[string]any{
		"schemaVersion": 1, "id": s.AddonID, "version": version, "executable": "bin/oauthfixture",
		"os": goruntime.GOOS, "arch": goruntime.GOARCH,
		"host": map[string]int{"min": 1, "max": 2}, "api": map[string]int{"min": 2, "max": 2},
		"protocol": map[string]int{"min": 1, "max": 1}, "dataFormat": map[string]int{"min": 1, "max": 2},
		"capabilities": contract.Capabilities{
			Tools: []string{"whoami"},
			ToolDefinitions: []contract.ToolDefinition{{Name: "whoami", Kind: contract.ToolKindRead,
				Description: "Say who the person is at " + s.Title + ".",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"reject":{"type":"array","items":{"type":"string","maxLength":64},"maxItems":4}},"additionalProperties":false}`)}},
			Credentials: []contract.CredentialSlot{{ID: "login", Title: s.Title + " account", Kind: contract.CredentialKindOAuth2, OAuth: &desc}},
			Connector: &contract.ConnectorCapability{Kinds: []contract.ConnectorKind{{ID: s.Kind, Title: s.Title,
				Description: "Let your chats read " + s.Title + " as you.", Delivery: contract.DeliveryPull, AgentOptional: true}}},
		},
	}
	b, err := json.Marshal(m)
	Expect(err).NotTo(HaveOccurred())
	return b
}

// pkg is the package of the add-on: its manifest and the one executable.
func pkg(manifest []byte, exeName string, exe []byte) []byte {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, f := range []struct {
		name string
		mode int64
		data []byte
	}{{"manifest.json", 0o644, manifest}, {exeName, 0o755, exe}} {
		Expect(tw.WriteHeader(&tar.Header{Name: f.name, Mode: f.mode, Size: int64(len(f.data))})).To(Succeed())
		_, err := tw.Write(f.data)
		Expect(err).NotTo(HaveOccurred())
	}
	Expect(tw.Close()).To(Succeed())
	return buf.Bytes()
}

// library serves the packages on loopback, as a library source does.
type library struct {
	*httptest.Server
	mu       sync.Mutex
	releases []catalog.Release
	archives map[string][]byte
}

func newLibrary() *library {
	l := &library{archives: map[string][]byte{}}
	l.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l.mu.Lock()
		defer l.mu.Unlock()
		if data, ok := l.archives[strings.TrimPrefix(r.URL.Path, "/")]; ok {
			_, _ = w.Write(data)
			return
		}
		_ = catalog.WriteIndex(w, l.releases)
	}))
	DeferCleanup(l.Close)
	return l
}

func (l *library) publish(s service, version string, manifest []byte) {
	id := s.AddonID
	exeName, exe := "bin/oauthfixture", fixtureExecutable
	if s.Executable != nil {
		exeName, exe = s.Executable()
	}
	data := pkg(manifest, exeName, exe)
	name := id + "-" + version + ".tar"
	l.mu.Lock()
	defer l.mu.Unlock()
	l.archives[name] = data
	l.releases = append(l.releases, catalog.Release{ID: id, Name: id, Version: version, OS: goruntime.GOOS, Arch: goruntime.GOARCH,
		Artifact: l.URL + "/" + name, Digest: fmt.Sprintf("sha256:%x", sha256.Sum256(data))})
}

// clock is the clock of the connectors: specs move it to end a token.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// node is one server: the owner, which runs the add-ons, or a frontend that
// forwards to it.
type node struct {
	app      *application.Application
	e        *echo.Echo
	sessions map[string]string
}

// cluster is the owner, the fake services, the library and the accounts.
type cluster struct {
	owner     *node
	db        *gorm.DB
	dir       string
	keys      string
	ownerAddr string
	clock     *clock
	network   *oauthtest.Network
	providers map[string]*oauthtest.Provider
	library   *library
	admin     contract.Actor
	// logStart is where the log lines of this cluster begin in suiteLogs.
	logStart int
	// startingFrontend is set while a frontend is built.
	startingFrontend bool
}

// syncBuffer is a log sink several goroutines write.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func (s *syncBuffer) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Len()
}

// suiteLogs holds every log line of the servers of the suite, so a spec can
// check that none carries a secret. The global logger is set once: servers
// of an ended spec may still be logging while the next one starts.
var (
	suiteLogs       = &syncBuffer{}
	suiteLogHandler = slog.NewTextHandler(suiteLogs, &slog.HandlerOptions{Level: slog.LevelDebug})
)

// logText is what the servers logged since the cluster started.
func (c *cluster) logText() string { return suiteLogs.String()[c.logStart:] }

// realTempDir returns a new temporary directory whose path has no symbolic
// link: the owner refuses one in its root. The owner leaves directories it
// owns without write access; they are opened up again so the directory can
// be removed.
func realTempDir() string {
	dir, err := filepath.EvalSymlinks(GinkgoT().TempDir())
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
			if err == nil && info.IsDir() {
				return os.Chmod(p, 0o700)
			}
			return nil
		})
	})
	return dir
}

// newCluster is newClusterFor the services every spec of the generic suite
// runs against.
func newCluster() *cluster { return newClusterFor(services) }

// newClusterFor starts the fake services, publishes version 1.0.0 of every
// add-on and 1.0.1 (with the token endpoint moved) to the library, and starts
// the owner with the 1.0.0 add-ons installed and enabled.
func newClusterFor(services []service) *cluster {
	c := &cluster{dir: realTempDir(), clock: &clock{now: time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)}, providers: map[string]*oauthtest.Provider{}, library: newLibrary()}
	var ps []*oauthtest.Provider
	for _, s := range services {
		p := oauthtest.New(oauthtest.Config{Host: s.TokenHost, ClientID: s.ClientID, ClientSecret: s.ClientSecret, ClientAuth: s.ClientAuth, AuthorizePath: s.AuthorizePath})
		DeferCleanup(p.Close)
		p.Set(func(p *oauthtest.Provider) { p.ExpiresIn = 3600 })
		c.providers[s.AddonID] = p
		ps = append(ps, p)
		c.library.publish(s, "1.0.0", s.manifest("1.0.0", false))
		c.library.publish(s, "1.0.1", s.manifest("1.0.1", true))
	}
	c.network = oauthtest.NewNetwork(ps...)
	for _, s := range services {
		p := c.providers[s.AddonID]
		c.network.Add(s.AuthorizeHost, p)
		c.network.Add(s.MovedTokenHost, p)
	}
	c.logStart = suiteLogs.Len()
	DeferCleanup(application.UseConnectorOptionsForTests(func(o *studio.ConnectorOptions) {
		o.TokenClient = c.network.TokenClient()
		if c.startingFrontend {
			// A frontend that called a token endpoint itself would fail: only
			// the owner reaches the services.
			o.TokenClient = oauthtest.NewNetwork().TokenClient()
		}
		o.Clock = c.clock.Now
		o.Logger = slog.New(suiteLogHandler)
	}))

	c.keys = filepath.Join(c.dir, "keys")
	Expect(gateway.GenerateCarrierKeys(c.keys, []string{"127.0.0.1"})).To(Succeed())
	l, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())
	c.ownerAddr = l.Addr().String()
	Expect(l.Close()).To(Succeed())
	transport, err := gateway.LoadCarrierConfig(c.keys, "owner", "https://"+c.ownerAddr)
	Expect(err).NotTo(HaveOccurred())
	c.owner = c.start("owner", config.AddonConfig{
		Enabled: true, InstanceID: transport.InstanceID, OwnerID: transport.OwnerID, OwnerAddress: transport.OwnerAddress, StorageID: "disk",
		Root: filepath.Join(c.dir, "owner", "addons"), ListenAddress: c.ownerAddr, Transport: transport,
		OwnershipMode: auth.AddonModeNamedUser,
		Host:          contract.HostCompatibility{Host: 1, API: contract.HostAPIVersion, Protocol: 1, SDK: 1, DataFormat: 1, OS: goruntime.GOOS, Arch: goruntime.GOARCH},
	})
	c.db = c.owner.app.AuthDB()
	Expect(c.db).NotTo(BeNil())
	for _, u := range []struct{ id, role string }{{"root", auth.RoleAdmin}, {"alice", auth.RoleUser}, {"bob", auth.RoleUser}} {
		Expect(c.db.Create(&auth.User{ID: u.id, Email: u.id + "@example.test", Name: u.id, Role: u.role, Status: auth.StatusActive, Provider: auth.ProviderLocal}).Error).To(Succeed())
		if u.role != auth.RoleAdmin {
			Expect(auth.UpdateUserPermissions(c.db, u.id, auth.PermissionMap{auth.FeatureConnectors: true, auth.FeatureAddons: true})).To(Succeed())
		}
	}
	c.login(c.owner)
	_, key, err := auth.CreateAPIKey(c.db, "root", "conformance", auth.RoleAdmin, hmacSecret, nil)
	Expect(err).NotTo(HaveOccurred())
	c.admin = contract.Actor{UserID: "root", CredentialID: "key:" + key.ID, Mode: auth.AddonModeNamedUser}

	ctx := context.Background()
	sources, manager := c.owner.app.AddonSources(), c.owner.app.AddonManager()
	Expect(sources).NotTo(BeNil())
	Expect(manager).NotTo(BeNil())
	Expect(sources.Save(ctx, c.admin, catalog.Source{ID: "conformance", Name: "Conformance", URL: c.library.URL, Enabled: true, AllowLoopbackHTTP: true})).To(Succeed())
	_, err = sources.Refresh(ctx, "conformance")
	Expect(err).NotTo(HaveOccurred())
	for _, s := range services {
		_, err := manager.Submit(ctx, c.admin, addonsvc.LifecycleCommand{OperationID: "install-" + s.AddonID, AddonID: s.AddonID, Action: "install", SourceID: "conformance", Version: "1.0.0"})
		Expect(err).NotTo(HaveOccurred())
		_, err = manager.Submit(ctx, c.admin, addonsvc.LifecycleCommand{OperationID: "enable-" + s.AddonID, AddonID: s.AddonID, Action: "enable", Generation: 1})
		Expect(err).NotTo(HaveOccurred())
	}
	c.waitRunning(len(services))
	return c
}

// start builds one server on the shared account database.
func (c *cluster) start(name string, addons config.AddonConfig) *node {
	dir := filepath.Join(c.dir, name)
	models, backends := filepath.Join(dir, "models"), filepath.Join(dir, "backends")
	Expect(os.MkdirAll(models, 0o750)).To(Succeed())
	Expect(os.MkdirAll(backends, 0o750)).To(Succeed())
	state, err := system.GetSystemState(system.WithModelPath(models), system.WithBackendPath(backends))
	Expect(err).NotTo(HaveOccurred())
	ctx, cancel := context.WithCancel(context.Background())
	DeferCleanup(cancel)
	app, err := application.New(
		config.WithContext(ctx),
		config.WithSystemState(state),
		config.WithAuthEnabled(true),
		config.WithAuthDatabaseURL(filepath.Join(c.dir, "auth.db")),
		config.WithAuthAPIKeyHMACSecret(hmacSecret),
		config.WithExternalBaseURL(externalURL),
		config.WithAddons(addons),
	)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = app.Shutdown() })
	e, err := localhttp.API(app)
	Expect(err).NotTo(HaveOccurred())
	return &node{app: app, e: e, sessions: map[string]string{}}
}

// login gives the node a session of every account.
func (c *cluster) login(n *node) {
	for _, id := range []string{"root", "alice", "bob"} {
		s, err := auth.CreateSession(c.db, id, hmacSecret)
		Expect(err).NotTo(HaveOccurred())
		n.sessions[id] = s
	}
}

func (c *cluster) waitRunning(n int) {
	Eventually(func() int {
		list, err := c.owner.app.AddonManager().Apps(context.Background(), c.admin)
		if err != nil {
			return -1
		}
		running := 0
		for _, a := range list {
			if a.State == "running" {
				running++
			}
		}
		return running
	}, 60*time.Second, 100*time.Millisecond).Should(BeNumerically(">=", n))
}

// frontend starts a server that runs no add-on and forwards every add-on
// call, and every OAuth call, to the owner over the mTLS carrier.
func (c *cluster) frontend() *node {
	transport, err := gateway.LoadCarrierConfig(c.keys, "frontend", "https://"+c.ownerAddr)
	Expect(err).NotTo(HaveOccurred())
	c.startingFrontend = true
	defer func() { c.startingFrontend = false }()
	n := c.start("frontend", config.AddonConfig{
		Enabled: true, InstanceID: transport.InstanceID, OwnerID: transport.OwnerID, OwnerAddress: transport.OwnerAddress, StorageID: "disk",
		Root: filepath.Join(c.dir, "frontend", "addons"), Transport: transport, OwnershipMode: auth.AddonModeNamedUser,
		Host: contract.HostCompatibility{Host: 1, API: contract.HostAPIVersion, Protocol: 1, SDK: 1, DataFormat: 1, OS: goruntime.GOOS, Arch: goruntime.GOARCH},
	})
	c.login(n)
	return n
}

// do sends a request to a node as user ("" for none) with cookies.
func (n *node) do(user, method, path string, body any, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	var rd *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		Expect(err).NotTo(HaveOccurred())
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Host = "localai.example.test"
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if s := n.sessions[user]; user != "" && s != "" {
		req.AddCookie(&http.Cookie{Name: "session", Value: s})
		req.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	rec := httptest.NewRecorder()
	n.e.ServeHTTP(rec, req)
	return rec
}

func decode(rec *httptest.ResponseRecorder) map[string]any {
	var out map[string]any
	ExpectWithOffset(1, json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed(), rec.Body.String())
	return out
}

func hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// location is the address of a 303 answer.
func location(rec *httptest.ResponseRecorder) *url.URL {
	ExpectWithOffset(1, rec.Code).To(Equal(http.StatusSeeOther), rec.Body.String())
	u, err := url.Parse(rec.Header().Get("Location"))
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return u
}

type addonCommand = addonsvc.LifecycleCommand
