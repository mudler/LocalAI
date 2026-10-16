// SPDX-License-Identifier: MIT
package tables_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/core/services/addons/policy"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTablesDatabases(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Tables databases addon")
}

var binary string

var _ = BeforeSuite(func() {
	binary = filepath.Join(GinkgoT().TempDir(), "tables")
	cmd := exec.Command("go", "build", "-o", binary, "../../../addons/tables/cmd/tables")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=1")
	out, e := cmd.CombinedOutput()
	Expect(e).NotTo(HaveOccurred(), string(out))
})

// rig is one owner: a supervisor over the real Tables executable, a policy
// store, and the host's bridge to the current runtime.
type rig struct {
	root      string
	dataRoot  string
	sup       *addonruntime.Supervisor
	pkg       packages.PackageReceipt
	gen       uint64
	policy    *policy.Policy
	store     *policy.Store
	policyDir string
	installed bool
}

func (r *rig) Runtime(ctx context.Context, id string) (addonruntime.Instance, error) {
	return r.sup.Current(ctx, id, r.gen)
}
func (r *rig) DeclaredTools(_ context.Context, id string, _ uint64) ([]string, error) {
	return r.pkg.Manifest.Capabilities.Tools, nil
}
func (r *rig) Installed(context.Context, string) (bool, error) { return r.installed, nil }
func (r *rig) backing() func(context.Context, policy.ResourceKey) (bool, error) {
	return policy.BackingExists(policy.BackingOptions{Runtime: r, Tools: r, Installed: r.Installed})
}

func user(id string) contract.Actor {
	return contract.Actor{UserID: id, CredentialID: "session:" + id, Mode: "named-user"}
}

func newRig() *rig {
	GinkgoHelper()
	root := GinkgoT().TempDir()
	r := &rig{root: root, dataRoot: filepath.Join(root, "data"), installed: true, gen: 1}
	data, e := os.ReadFile("../../../addons/tables/manifest.json")
	Expect(e).NotTo(HaveOccurred())
	m, e := contract.ParseManifest(data)
	Expect(e).NotTo(HaveOccurred())
	m.OS, m.Arch = goruntime.GOOS, goruntime.GOARCH
	dir := filepath.Join(root, "package")
	Expect(os.MkdirAll(dir, 0o700)).To(Succeed())
	exe, e := os.ReadFile(binary)
	Expect(e).NotTo(HaveOccurred())
	Expect(os.WriteFile(filepath.Join(dir, m.Executable), exe, 0o500)).To(Succeed())
	r.pkg = packages.PackageReceipt{Manifest: m, Directory: dir, Digest: "sha256:tables"}
	b, e := json.Marshal(r.pkg)
	Expect(e).NotTo(HaveOccurred())
	Expect(os.WriteFile(filepath.Join(dir, "receipt.json"), b, 0o400)).To(Succeed())
	limits := contract.DefaultLimits()
	r.sup, e = addonruntime.NewSupervisor(addonruntime.Config{StateRoot: filepath.Join(root, "runtime"), DataRoot: r.dataRoot, Limits: limits, ReadyTimeout: 10 * time.Second, StableHealth: limits.BackoffMax})
	Expect(e).NotTo(HaveOccurred())
	DeferCleanup(func() { Expect(r.sup.Close(context.Background())).To(Succeed()) })
	r.openPolicy()
	return r
}

func (r *rig) openPolicy() {
	GinkgoHelper()
	r.policyDir = filepath.Join(r.root, "policy")
	st, e := policy.OpenStore(r.policyDir, policy.ModeNamedUser)
	Expect(e).NotTo(HaveOccurred())
	r.store = st
	r.policy = policy.New(st, policy.Options{
		Revalidate:      func(context.Context, contract.Actor) error { return nil },
		CreateAuthority: func(context.Context, contract.Actor, string) error { return nil },
	})
}

func (r *rig) closePolicy() { Expect(r.store.Close()).To(Succeed()) }

func (r *rig) start(gen uint64) addonruntime.Instance {
	GinkgoHelper()
	r.gen = gen
	i, e := r.sup.Start(context.Background(), r.pkg, gen)
	Expect(e).NotTo(HaveOccurred())
	return i
}

func (r *rig) dbFile(id string) string {
	return filepath.Join(r.dataRoot, "tables", "tables", "databases", id+".db")
}

func args(v map[string]any) json.RawMessage { b, _ := json.Marshal(v); return b }

func (r *rig) envelope(inst addonruntime.Instance, actor contract.Actor, scope, op, resource string, a json.RawMessage) contract.Envelope {
	return contract.Envelope{
		Request:   contract.Request{AddonID: "tables", ResourceID: resource, OperationID: op, Generation: inst.Generation, Action: "schema", Arguments: a},
		Authority: contract.Authority{Actor: actor, Scope: scope, ExpiresAt: time.Now().Add(time.Minute)},
	}
}

func (r *rig) create(inst addonruntime.Instance, actor contract.Actor, name string) (policy.ResourceKey, error) {
	return r.policy.Create(context.Background(), actor, "tables", policy.RuntimeCreate(r, r, contract.DefaultLimits(), actor, args(map[string]any{"tool": "databases-create", "name": name})))
}

func (r *rig) read(inst addonruntime.Instance, actor contract.Actor, resource, tool string) (contract.Result, error) {
	return inst.Client.Read(context.Background(), r.envelope(inst, actor, "read", "read-"+resource, resource, args(map[string]any{"tool": tool})))
}

func (r *rig) rename(inst addonruntime.Instance, actor contract.Actor, resource, name, op string) (contract.Receipt, error) {
	a := args(map[string]any{"tool": "databases-rename", "name": name})
	p, e := inst.Client.Preview(context.Background(), r.envelope(inst, actor, "preview", op, resource, a))
	if e != nil {
		return contract.Receipt{}, e
	}
	return inst.Client.Apply(context.Background(), r.envelope(inst, actor, "apply", op, resource, a), p.Revision)
}

func schemaName(res contract.Result) string {
	GinkgoHelper()
	var out struct {
		Name string `json:"name"`
	}
	Expect(json.Unmarshal(res.Payload, &out)).To(Succeed())
	return out.Name
}

// reservations lists the IDs still pending in the policy store, read from its file.
func (r *rig) reservations() []string {
	GinkgoHelper()
	got, e := r.policy.Pending()
	Expect(e).NotTo(HaveOccurred())
	return got
}
