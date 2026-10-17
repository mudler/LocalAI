package main

// The add-ons UI fixture: with --addons-fixture the server runs with
// authentication on and a real add-on owner (lifecycle manager, supervisor,
// gateway and mTLS carrier), and installs the packages of the fixture
// directory through a real catalog served on loopback. A spec then drives the
// shell against real authorization: sessions and API keys are stored keys,
// every asset and call is decided by the owner, and enable, disable and
// upgrade are the real lifecycle routes.

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
	"fmt"
	"io/fs"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mudler/LocalAI/core/application"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	addonsvc "github.com/mudler/LocalAI/core/services/addons"
	"github.com/mudler/LocalAI/core/services/addons/catalog"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

const fixtureSecret = "addons-ui-fixture-secret" // #nosec G101 -- a fixed secret of the local test fixture, not a credential

type addonsFixture struct {
	dataDir  string
	listener net.Listener
	catalog  *http.Server
	library  string
	releases []catalog.Release
	archives map[string][]byte
	owner    config.AddonConfig
	// localOwner runs the owner without authentication, in local-owner mode.
	localOwner bool
	// mu guards what a spec moves through the control routes: the step of
	// the moving channels and whether the library answers at all.
	mu      sync.Mutex
	step    int
	offline bool
	moving  []movingRelease
}

// movingRelease is a release of a package directory that carries
// .fixture-generation: of the releases of one add-on and channel, only the
// one with the highest generation not above the current step is listed. A spec
// moves the channel by raising the step, as a publisher moves a tag.
type movingRelease struct {
	catalog.Release
	generation int
}

// Credentials is what the spec reads from --fixture-out.
type fixtureCredentials struct {
	Users map[string]fixtureUser `json:"users"`
	// Packages lists every installable release as "id@version".
	Packages []string `json:"packages"`
	// Control is the base URL of the fixture library's control routes: POST
	// /step moves every moving channel on by one generation, POST /offline and
	// /online make the library answer or not. Empty without moving channels.
	Control string `json:"control,omitempty"`
}

type fixtureUser struct {
	ID      string `json:"id"`
	Role    string `json:"role"`
	Token   string `json:"token"`
	Session string `json:"session"`
}

func newAddonsFixture(dir, executable, dataDir string, localOwner bool) (*addonsFixture, error) {
	f := &addonsFixture{dataDir: dataDir, archives: map[string][]byte{}, localOwner: localOwner, step: 1}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	exe, err := os.ReadFile(executable) // #nosec G304 -- test harness reads files it created in its own work directory
	if err != nil {
		return nil, fmt.Errorf("fixture executable: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pkg := filepath.Join(dir, entry.Name())
		raw, err := os.ReadFile(filepath.Join(pkg, "manifest.json")) // #nosec G304 -- test harness reads files it created in its own work directory
		if err != nil {
			continue
		}
		var manifest map[string]any
		if err := json.Unmarshal(raw, &manifest); err != nil {
			return nil, fmt.Errorf("%s: %w", entry.Name(), err)
		}
		// The package is for this machine: the fixture sources are portable.
		manifest["os"], manifest["arch"] = runtime.GOOS, runtime.GOARCH
		patched, _ := json.Marshal(manifest)
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		add := func(name string, data []byte) error {
			if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(data))}); err != nil {
				return err
			}
			_, err := tw.Write(data)
			return err
		}
		if err := add("manifest.json", patched); err != nil {
			return nil, err
		}
		// The executable goes where the manifest says it is. The protocol
		// fixture packages all say bin/fixture; a package that names another
		// path (the Tables package) gets the supplied executable there.
		exePath := "bin/fixture"
		if declared, _ := manifest["executable"].(string); declared != "" && !strings.HasPrefix(declared, "/") && !strings.Contains(declared, "..") {
			exePath = declared
		}
		if err := add(exePath, exe); err != nil {
			return nil, err
		}
		var files []string
		_ = filepath.WalkDir(pkg, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				rel, _ := filepath.Rel(pkg, p)
				// The .fixture-* files describe the release to the library and
				// are not part of the package.
				if rel != "manifest.json" && !strings.HasPrefix(rel, ".fixture-") {
					files = append(files, rel)
				}
			}
			return nil
		})
		sort.Strings(files)
		for _, rel := range files {
			data, err := os.ReadFile(filepath.Join(pkg, rel)) // #nosec G304 -- test harness reads files it created in its own work directory
			if err != nil {
				return nil, err
			}
			if err := add(filepath.ToSlash(rel), data); err != nil {
				return nil, err
			}
		}
		if err := tw.Close(); err != nil {
			return nil, err
		}
		id, _ := manifest["id"].(string)
		version, _ := manifest["version"].(string)
		f.archives[entry.Name()+".tar"] = buf.Bytes()
		release := catalog.Release{
			AddonID: id, Name: id, Version: version, OS: runtime.GOOS, Arch: runtime.GOARCH,
			Digest: fmt.Sprintf("sha256:%x", sha256.Sum256(buf.Bytes())), Artifact: "/" + entry.Name() + ".tar",
		}
		if channel, err := os.ReadFile(filepath.Join(pkg, ".fixture-channel")); err == nil { // #nosec G304 -- test harness reads files it created in its own work directory
			release.Channel = strings.TrimSpace(string(channel))
		}
		if raw, err := os.ReadFile(filepath.Join(pkg, ".fixture-requires")); err == nil { // #nosec G304 -- test harness reads files it created in its own work directory
			if err := json.Unmarshal(raw, &release.Requires); err != nil {
				return nil, fmt.Errorf("%s: .fixture-requires: %w", entry.Name(), err)
			}
		}
		if raw, err := os.ReadFile(filepath.Join(pkg, ".fixture-generation")); err == nil { // #nosec G304 -- test harness reads files it created in its own work directory
			n, err := strconv.Atoi(strings.TrimSpace(string(raw)))
			if err != nil || n < 1 {
				return nil, fmt.Errorf("%s: .fixture-generation must be a positive number", entry.Name())
			}
			f.moving = append(f.moving, movingRelease{Release: release, generation: n})
			continue
		}
		f.releases = append(f.releases, release)
	}
	if len(f.releases) == 0 && len(f.moving) == 0 {
		return nil, fmt.Errorf("no package under %s", dir)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	f.listener = listener
	base := "http://" + listener.Addr().String()
	for i := range f.releases {
		f.releases[i].Artifact = base + f.releases[i].Artifact
	}
	for i := range f.moving {
		f.moving[i].Artifact = base + f.moving[i].Artifact
	}
	f.library = base + "/library.yaml"
	mux := http.NewServeMux()
	mux.HandleFunc("/library.yaml", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		offline, listed := f.offline, f.listed()
		f.mu.Unlock()
		if offline {
			http.Error(w, "library offline", http.StatusServiceUnavailable)
			return
		}
		_ = catalog.WriteIndex(w, listed)
	})
	// The control routes of a spec: move the channels on and take the library
	// away. They exist only in this test server.
	mux.HandleFunc("/_fixture/step", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.step++
		n := f.step
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]int{"step": n})
	})
	for path, offline := range map[string]bool{"/_fixture/offline": true, "/_fixture/online": false} {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			f.mu.Lock()
			f.offline = offline
			f.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]bool{"offline": offline})
		})
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		data, ok := f.archives[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	})
	f.catalog = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = f.catalog.Serve(listener) }()

	transport, address, err := fixtureTransport()
	if err != nil {
		return nil, err
	}
	f.owner = config.AddonConfig{
		Enabled: true, InstanceID: "owner", OwnerID: "owner", OwnerAddress: "https://" + address, StorageID: "disk",
		Root: filepath.Join(dataDir, "addons"), ListenAddress: address, Transport: transport,
		Host: contract.HostCompatibility{Host: 1, API: 1, Protocol: 1, SDK: 1, DataFormat: 1, OS: runtime.GOOS, Arch: runtime.GOARCH},
		// A spec checks again within seconds: the interval of the refresh
		// limit is short here and the limit itself stays on.
		UpdateRefreshInterval: time.Second,
	}
	if localOwner {
		f.owner.OwnershipMode = auth.AddonModeLocalOwner
	}
	return f, nil
}

// listed is the library document at the current step: the releases that never
// move and, of each moving add-on and channel, the one the step has reached.
func (f *addonsFixture) listed() []catalog.Release {
	out := append([]catalog.Release(nil), f.releases...)
	best := map[string]movingRelease{}
	for _, m := range f.moving {
		if m.generation > f.step {
			continue
		}
		key := m.AddonID + "/" + m.Channel + "/" + m.Version
		if cur, ok := best[key]; !ok || m.generation > cur.generation {
			best[key] = m
		}
	}
	keys := make([]string, 0, len(best))
	for k := range best {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, best[k].Release)
	}
	return out
}

// fixtureTransport builds the owner's mTLS carrier identity: one pinned
// certificate and one signing key, as a single-instance deployment has.
func fixtureTransport() (*gateway.CarrierConfig, string, error) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", err
	}
	address := probe.Addr().String()
	_ = probe.Close()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", err
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "fixture CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, pub, key)
	if err != nil {
		return nil, "", err
	}
	if ca, err = x509.ParseCertificate(der); err != nil {
		return nil, "", err
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	lp, lk, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", err
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "owner"}, NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
	ld, err := x509.CreateCertificate(rand.Reader, leaf, ca, lp, key)
	if err != nil {
		return nil, "", err
	}
	kd, err := x509.MarshalPKCS8PrivateKey(lk)
	if err != nil {
		return nil, "", err
	}
	pair, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ld}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kd}))
	if err != nil {
		return nil, "", err
	}
	signPub, signKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", err
	}
	return &gateway.CarrierConfig{
		InstanceID: "owner", OwnerID: "owner", OwnerAddress: "https://" + address,
		Certificate: pair, Roots: pool,
		Pins:             map[string][32]byte{"owner": sha256.Sum256(ld)},
		SigningKey:       signKey,
		VerificationKeys: map[string]ed25519.PublicKey{"owner": signPub},
	}, address, nil
}

func (f *addonsFixture) options(dataDir string) []config.AppOption {
	if f.localOwner {
		// One local owner and no accounts: sharing has nobody to share with.
		return []config.AppOption{config.WithAddons(f.owner)}
	}
	return []config.AppOption{
		config.WithAuthEnabled(true),
		config.WithAuthDatabaseURL(filepath.Join(dataDir, "auth.db")),
		config.WithAuthAPIKeyHMACSecret(fixtureSecret),
		config.WithAddons(f.owner),
	}
}

// seed creates the accounts, saves the library and installs and enables the
// packages named in `enable` ("id@version").
func (f *addonsFixture) seed(ctx context.Context, app *application.Application, out string, enable []string) error {
	db := app.AuthDB()
	if db == nil && !f.localOwner {
		return fmt.Errorf("authentication is not enabled")
	}
	creds := fixtureCredentials{Users: map[string]fixtureUser{}}
	var admin contract.Actor
	if f.localOwner {
		admin = contract.Actor{UserID: auth.AddonLocalOwnerID, CredentialID: auth.AddonLocalOwnerID, Mode: auth.AddonModeLocalOwner}
	}
	accounts := []struct {
		name, role string
		feature    bool
	}{{"admin", auth.RoleAdmin, true}, {"member", auth.RoleUser, true}, {"outsider", auth.RoleUser, false},
		// Two more people with the feature, for the roles of a shared database.
		{"editor", auth.RoleUser, true}, {"viewer", auth.RoleUser, true}}
	if f.localOwner {
		accounts = nil
	}
	for _, u := range accounts {
		user := auth.User{ID: "fixture-" + u.name, Email: u.name + "@fixture.test", Name: u.name, Provider: auth.ProviderLocal, Role: u.role, Status: auth.StatusActive}
		if err := db.Create(&user).Error; err != nil {
			return err
		}
		if u.feature && u.role != auth.RoleAdmin {
			if err := db.Create(&auth.UserPermission{ID: uuid.NewString(), UserID: user.ID, Permissions: auth.PermissionMap{auth.FeatureAddons: true}}).Error; err != nil {
				return err
			}
		}
		token, key, err := auth.CreateAPIKey(db, user.ID, "fixture", u.role, fixtureSecret, nil)
		if err != nil {
			return err
		}
		session, err := auth.CreateSession(db, user.ID, fixtureSecret)
		if err != nil {
			return err
		}
		creds.Users[u.name] = fixtureUser{ID: user.ID, Role: u.role, Token: token, Session: session}
		if u.name == "admin" {
			admin = contract.Actor{UserID: user.ID, CredentialID: "key:" + key.ID, Mode: auth.AddonModeNamedUser}
		}
	}
	sources, manager := app.AddonSources(), app.AddonManager()
	if sources == nil || manager == nil {
		return fmt.Errorf("the add-on owner did not start")
	}
	if err := sources.Save(ctx, admin, catalog.Source{ID: "fixture", Name: "Fixture", URL: f.library, Enabled: true, AllowLoopbackHTTP: true}); err != nil {
		return err
	}
	if _, err := sources.Refresh(ctx, "fixture"); err != nil {
		return err
	}
	for _, r := range f.listed() {
		creds.Packages = append(creds.Packages, r.AddonID+"@"+r.Version)
	}
	if len(f.moving) > 0 {
		creds.Control = "http://" + f.listener.Addr().String() + "/_fixture"
	}
	for i, spec := range enable {
		id, version, _ := strings.Cut(spec, "@")
		if _, err := manager.Submit(ctx, admin, addonsvc.LifecycleCommand{OperationID: fmt.Sprintf("seed-install-%d", i), AddonID: id, Action: "install", SourceID: "fixture", Version: version}); err != nil {
			return fmt.Errorf("install %s: %w", spec, err)
		}
		if _, err := manager.Submit(ctx, admin, addonsvc.LifecycleCommand{OperationID: fmt.Sprintf("seed-enable-%d", i), AddonID: id, Action: "enable", Generation: 1}); err != nil {
			return fmt.Errorf("enable %s: %w", spec, err)
		}
	}
	// Wait for every enabled process to be observed running, so a spec starts
	// from a settled owner.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		list, err := manager.Apps(ctx, admin)
		if err == nil {
			running := 0
			for _, a := range list {
				if a.State == "running" {
					running++
				}
			}
			if running >= len(enable) {
				break
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	b, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(out, b, 0o600)
}

func (f *addonsFixture) close() {
	if f.catalog != nil {
		_ = f.catalog.Close()
	}
}
