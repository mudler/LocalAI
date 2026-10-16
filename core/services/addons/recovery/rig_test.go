// SPDX-License-Identifier: MIT
package recovery_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/mudler/LocalAI/core/services/addons/gateway"
	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/core/services/addons/policy"
	"github.com/mudler/LocalAI/core/services/addons/recovery"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const addon = "tables"

func named(id string) contract.Actor {
	return contract.Actor{UserID: id, CredentialID: "key:" + id, Mode: "named-user"}
}

// receipt is a package receipt of the add-on with the given data format.
func receipt(version string, format uint64) packages.PackageReceipt {
	return packages.PackageReceipt{
		Digest: "sha256:" + version, SourceID: "library", Channel: "stable", Verification: "sha256+sigstore",
		Manifest: contract.Manifest{ID: addon, Version: version, DataFormat: contract.VersionRange{Min: 1, Max: format}},
	}
}

// staged is a candidate package with a receipt.
type staged struct{ r packages.PackageReceipt }

func (s staged) Receipt() packages.PackageReceipt { return s.r }

// fakeHost is the lifecycle manager of a test: one add-on, its journaled
// generation and a process that is running or not.
type fakeHost struct {
	mu       sync.Mutex
	info     recovery.Info
	quiesced bool
	resumes  int
	// start runs when the candidate is started. It may change the data.
	start func(dataDir string) error
	data  string
	// candidate is what is running when a candidate started.
	candidate bool
	published []uint64
	quiesceFn func() error
}

func (h *fakeHost) Info(context.Context, string) (recovery.Info, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.info, nil
}

type fakeQ struct{ h *fakeHost }

func (h *fakeHost) Quiesce(context.Context, string) (recovery.Quiesced, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.quiesceFn != nil {
		if e := h.quiesceFn(); e != nil {
			return nil, e
		}
	}
	h.quiesced = true
	h.info.Running = false
	return fakeQ{h}, nil
}

func (q fakeQ) Resume(context.Context) error {
	q.h.mu.Lock()
	defer q.h.mu.Unlock()
	q.h.quiesced = false
	q.h.resumes++
	if q.h.info.Enabled {
		q.h.info.Running = true
	}
	return nil
}

func (q fakeQ) Release() {
	q.h.mu.Lock()
	defer q.h.mu.Unlock()
	q.h.quiesced = false
}

func (h *fakeHost) Commit(_ context.Context, c recovery.Candidate) (packages.PackageReceipt, error) {
	return c.Receipt(), nil
}

func (h *fakeHost) StartCandidate(_ context.Context, _ string, _ packages.PackageReceipt, _ uint64) error {
	h.mu.Lock()
	start, data := h.start, h.data
	h.mu.Unlock()
	if start != nil {
		if e := start(data); e != nil {
			return e
		}
	}
	h.mu.Lock()
	h.candidate = true
	h.info.Running = true
	h.mu.Unlock()
	return nil
}

func (h *fakeHost) StopCandidate(context.Context, string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.candidate = false
	h.info.Running = false
	return nil
}

func (h *fakeHost) Publish(_ context.Context, _ string, r packages.PackageReceipt, n uint64) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.info.Receipt, h.info.Generation = r, n
	h.published = append(h.published, n)
	return nil
}

// restart is a process restart: nothing is quiesced or running.
func (h *fakeHost) restart() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.quiesced, h.candidate, h.info.Running = false, false, false
}

func (h *fakeHost) set(f func(*recovery.Info)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	f(&h.info)
}

// accounts is the account directory of a test.
type accounts struct {
	mu   sync.Mutex
	gone map[string]bool
	all  map[string]bool
}

func (a *accounts) check(_ context.Context, id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.gone[id] || !a.all[id] {
		return fmt.Errorf("no such account: %w", gateway.ErrForbidden)
	}
	return nil
}

func (a *accounts) revalidate(ctx context.Context, actor contract.Actor) error {
	if actor.Mode == "local-owner" {
		return nil
	}
	return a.check(ctx, actor.UserID)
}

type nullRuntime struct{}

func (nullRuntime) Runtime(context.Context, string) (addonruntime.Instance, error) {
	return addonruntime.Instance{}, errors.New("no runtime")
}

type rig struct {
	root     string
	host     *fakeHost
	store    *policy.Store
	broker   *policy.Broker
	policy   *policy.Policy
	port     *policy.RecoveryPort
	rec      *recovery.Recovery
	accounts *accounts
	retain   recovery.Retention
	// open sqlite handles that keep a WAL alive.
	dbs []*sql.DB
	now time.Time
}

func newRig(mode policy.Mode, users ...string) *rig {
	GinkgoHelper()
	r := &rig{root: GinkgoT().TempDir(), now: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	Expect(os.Chmod(r.root, 0o700)).To(Succeed())
	DeferCleanup(func() {
		_ = filepath.WalkDir(r.root, func(p string, d fs.DirEntry, e error) error {
			if e == nil && d.IsDir() {
				_ = os.Chmod(p, 0o700)
			}
			return nil
		})
	})
	r.accounts = &accounts{gone: map[string]bool{}, all: map[string]bool{}}
	for _, u := range users {
		r.accounts.all[u] = true
	}
	r.host = &fakeHost{data: filepath.Join(r.root, "data", addon)}
	r.host.info = recovery.Info{Installed: true, Enabled: true, Running: true, Generation: 1, Receipt: receipt("0.1.0", 1), Published: []uint64{1}}
	r.openPolicy(mode)
	DeferCleanup(func() {
		for _, d := range r.dbs {
			_ = d.Close()
		}
		r.closePolicy()
	})
	r.retain = recovery.Retention{Keep: 3}
	r.newRecovery()
	return r
}

func (r *rig) openPolicy(mode policy.Mode) {
	GinkgoHelper()
	store, e := policy.OpenStore(filepath.Join(r.root, "policy"), mode)
	Expect(e).NotTo(HaveOccurred())
	broker, e := policy.OpenBroker(filepath.Join(r.root, "proposals"), policy.BrokerOptions{Runtime: nullRuntime{}})
	Expect(e).NotTo(HaveOccurred())
	p := policy.New(store, policy.Options{
		Revalidate:      r.accounts.revalidate,
		CreateAuthority: func(context.Context, contract.Actor, string) error { return nil },
		Proposals:       broker,
		DrainTimeout:    2 * time.Second,
	})
	broker.Attach(p)
	r.store, r.broker, r.policy = store, broker, p
	r.port = &policy.RecoveryPort{Policy: p, Broker: broker, Now: func() time.Time { return r.now }}
}

func (r *rig) closePolicy() {
	if r.broker != nil {
		_ = r.broker.Close()
	}
	if r.store != nil {
		_ = r.store.Close()
	}
	r.broker, r.store = nil, nil
}

func (r *rig) newRecovery() {
	GinkgoHelper()
	rec, e := recovery.New(recovery.Config{
		Root: r.root, Host: r.host, Policy: r.port, Retention: r.retain, Now: func() time.Time { return r.now },
		Admin:   func(_ context.Context, a contract.Actor) error { return adminOnly(a) },
		Account: r.accounts.check, DrainTimeout: 2 * time.Second,
		SelectionsDir: filepath.Join(r.root, "selections"),
	})
	Expect(e).NotTo(HaveOccurred())
	r.rec = rec
}

func adminOnly(a contract.Actor) error {
	if a.UserID != "admin" {
		return errors.New("not an administrator")
	}
	return nil
}

func (r *rig) admin() contract.Actor { return named("admin") }

// tick moves the clock, so backups have distinct creation times.
func (r *rig) tick() { r.now = r.now.Add(time.Minute) }

// seed makes the data of the add-on: one WAL database with rows that are only
// in the WAL, a plain file and a scratch export that is not backed up.
func (r *rig) seed(rows int) string {
	GinkgoHelper()
	dir := filepath.Join(r.root, "data", addon, "tables", "databases")
	Expect(os.MkdirAll(dir, 0o700)).To(Succeed())
	path := filepath.Join(dir, "r1.db")
	db, e := sql.Open("sqlite3", "file:"+path+"?_journal_mode=WAL&_synchronous=FULL")
	Expect(e).NotTo(HaveOccurred())
	db.SetMaxOpenConns(1)
	r.dbs = append(r.dbs, db)
	_, e = db.Exec("CREATE TABLE IF NOT EXISTS t(id INTEGER PRIMARY KEY, v TEXT)")
	Expect(e).NotTo(HaveOccurred())
	for i := 0; i < rows; i++ {
		_, e = db.Exec("INSERT INTO t(v) VALUES(?)", fmt.Sprintf("row-%d", i))
		Expect(e).NotTo(HaveOccurred())
	}
	Expect(os.WriteFile(filepath.Join(r.root, "data", addon, "note.txt"), []byte("kept"), 0o600)).To(Succeed())
	Expect(os.MkdirAll(filepath.Join(r.root, "data", addon, "exports"), 0o700)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(r.root, "data", addon, "exports", "scratch.csv"), []byte("x"), 0o600)).To(Succeed())
	return path
}

// closeDBs ends the open handles, as the stop of the process does.
func (r *rig) closeDBs() {
	for _, d := range r.dbs {
		_ = d.Close()
	}
	r.dbs = nil
}

func (r *rig) rowsIn(path string) int {
	GinkgoHelper()
	db, e := sql.Open("sqlite3", "file:"+path+"?mode=ro")
	Expect(e).NotTo(HaveOccurred())
	defer func() { _ = db.Close() }()
	var n int
	Expect(db.QueryRow("SELECT COUNT(*) FROM t").Scan(&n)).To(Succeed())
	return n
}

func (r *rig) dbPath() string {
	return filepath.Join(r.root, "data", addon, "tables", "databases", "r1.db")
}

// addRow writes into the live data directory, as the add-on (or a candidate
// migrating) does.
func (r *rig) addRow(v string) {
	GinkgoHelper()
	db, e := sql.Open("sqlite3", "file:"+r.dbPath())
	Expect(e).NotTo(HaveOccurred())
	defer func() { _ = db.Close() }()
	_, e = db.Exec("INSERT INTO t(v) VALUES(?)", v)
	Expect(e).NotTo(HaveOccurred())
}

// register makes a resource of the add-on owned by owner and returns its key.
func (r *rig) register(owner string) policy.ResourceKey {
	GinkgoHelper()
	key, e := r.policy.Register(context.Background(), named(owner), addon)
	Expect(e).NotTo(HaveOccurred())
	Expect(r.policy.Commit(context.Background(), named(owner), key)).To(Succeed())
	return key
}

func localActor() contract.Actor {
	return contract.Actor{UserID: "local-owner", CredentialID: "local-owner", Mode: "local-owner"}
}

// newRigAt is newRig on an existing root, as a second start of the owner.
func newRigAt(root string, mode policy.Mode, users ...string) *rig {
	GinkgoHelper()
	r := &rig{root: root, now: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	r.accounts = &accounts{gone: map[string]bool{}, all: map[string]bool{}}
	for _, u := range users {
		r.accounts.all[u] = true
	}
	r.host = &fakeHost{data: filepath.Join(r.root, "data", addon)}
	r.host.info = recovery.Info{Installed: true, Enabled: true, Running: true, Generation: 1, Receipt: receipt("0.1.0", 1), Published: []uint64{1}}
	r.openPolicy(mode)
	DeferCleanup(func() {
		for _, d := range r.dbs {
			_ = d.Close()
		}
		r.closePolicy()
	})
	r.retain = recovery.Retention{Keep: 3}
	r.newRecovery()
	return r
}
