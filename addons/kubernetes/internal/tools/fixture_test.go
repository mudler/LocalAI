// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/addons/kubernetes/internal/kubeapi"
	"github.com/mudler/LocalAI/addons/kubernetes/internal/policy"
	"github.com/mudler/LocalAI/addons/kubernetes/internal/tools"
	"github.com/mudler/LocalAI/addons/kubernetes/kubetest"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	token  = "kube-token-very-secret-0123"
	server = "https://cluster.example:6443"
	key    = server + "#context=dev"
)

var ctx = context.Background()

// writes are the write tools a test policy turns on: all but exec.
var writes = []string{"scale", "rollout-restart", "rollout-undo", "delete-pod", "cordon", "uncordon", "drain", "apply", "patch"}

var kubeconfig = fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: c
  cluster:
    server: %s
    certificate-authority-data: %s
contexts:
- name: dev
  context: {cluster: c, user: u}
current-context: dev
users:
- name: u
  user:
    token: %s
`, server, base64.StdEncoding.EncodeToString([]byte("-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n")), token)

type adminFn func() bool

func (f adminFn) IsAdmin(context.Context, string) bool { return f() }

// rig is a runtime over a fake cluster.
type rig struct {
	k     *kubetest.Cluster
	rt    *tools.Runtime
	pol   *policy.Store
	admin bool
	cred  string
	key   string
	now   time.Time
}

func newRig(objs ...runtime.Object) *rig {
	g := &rig{k: kubetest.New(objs...), cred: kubeconfig, key: key, now: time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)}
	g.pol = policy.Open("")
	// The tests of the write tools need writes on, and the tests of the
	// masking need Secret values off: the administrator set a default policy
	// that allows every write tool but exec and hides Secret values.
	_, err := g.pol.SetDefault(policy.Policy{Namespaces: []string{policy.All}, WriteTools: writes, ReadSecretValues: false})
	Expect(err).NotTo(HaveOccurred())
	g.rt = tools.New(g.k.Factory(), g.pol)
	g.rt.Now = func() time.Time { return g.now }
	g.rt.Admin = adminFn(func() bool { return g.admin })
	return g
}

func (g *rig) env(op, tool string, args map[string]any) contract.Envelope {
	if args == nil {
		args = map[string]any{}
	}
	args["tool"] = tool
	raw, _ := json.Marshal(args)
	return contract.Envelope{Request: contract.Request{AddonID: "kubernetes", OperationID: op, Arguments: raw, Credential: g.cred, ResourceKey: g.key}}
}

type reply struct {
	Source    string         `json:"source"`
	Untrusted bool           `json:"untrusted"`
	Notice    string         `json:"notice"`
	Warnings  []string       `json:"warnings"`
	Data      map[string]any `json:"data"`
}

// noLeak fails when the credential appears in a text.
func noLeak(s string) {
	Expect(s).NotTo(ContainSubstring(token))
	Expect(s).NotTo(ContainSubstring("kube-token"))
}

func (g *rig) read(tool string, args map[string]any) (reply, error) {
	res, err := g.rt.Read(ctx, g.env("op-read", tool, args))
	if err != nil {
		noLeak(err.Error())
		return reply{}, err
	}
	Expect(len(res.Payload)).To(BeNumerically("<=", tools.MaxPayload))
	noLeak(string(res.Payload))
	var r reply
	Expect(json.Unmarshal(res.Payload, &r)).To(Succeed())
	Expect(r.Untrusted).To(BeTrue())
	Expect(r.Notice).To(Equal(tools.Notice))
	return r, nil
}

func (g *rig) mustRead(tool string, args map[string]any) map[string]any {
	r, err := g.read(tool, args)
	Expect(err).NotTo(HaveOccurred())
	return r.Data
}

// mustPolicy is the policy of the rig's cluster now.
func (g *rig) mustPolicy() policy.Policy {
	p, err := g.pol.Get(key)
	Expect(err).NotTo(HaveOccurred())
	return p
}

// setPolicy stores the policy of the rig's cluster.
func (g *rig) setPolicy(p policy.Policy) {
	_, err := g.pol.Set(key, p)
	Expect(err).NotTo(HaveOccurred())
}

func (g *rig) preview(op, tool string, args map[string]any) (contract.Preview, error) {
	p, err := g.rt.Preview(ctx, g.env(op, tool, args))
	if err != nil {
		noLeak(err.Error())
	} else {
		noLeak(string(p.Summary))
		Expect(len(p.Summary)).To(BeNumerically("<=", 12<<10))
	}
	return p, err
}

func (g *rig) apply(op, tool string, args map[string]any, p contract.Preview) (contract.Receipt, error) {
	rc, err := g.rt.Apply(ctx, g.env(op, tool, args), p.Revision)
	if err != nil {
		noLeak(err.Error())
	} else {
		noLeak(string(rc.Result.Payload))
	}
	return rc, err
}

// run previews and applies a write, as the host does for a confirmed call and
// for one the person lets run without a card.
func (g *rig) run(op, tool string, args map[string]any) (contract.Receipt, map[string]any) {
	p, err := g.preview(op, tool, args)
	Expect(err).NotTo(HaveOccurred())
	rc, err := g.apply(op, tool, args, p)
	Expect(err).NotTo(HaveOccurred())
	return rc, resultData(rc)
}

func resultData(rc contract.Receipt) map[string]any {
	var r reply
	if len(rc.Result.Payload) > 0 {
		Expect(json.Unmarshal(rc.Result.Payload, &r)).To(Succeed())
	}
	return r.Data
}

func sampleOf(p contract.Preview) map[string]any {
	var s struct {
		Sample map[string]any `json:"sample"`
	}
	Expect(json.Unmarshal(p.Summary, &s)).To(Succeed())
	return s.Sample
}

// rvRecorder keeps the resourceVersion of every apply the add-on sends.
type rvRecorder struct {
	kubeapi.Applier
	seen []string
}

func (r *rvRecorder) Apply(c context.Context, gvr schema.GroupVersionResource, ns string, obj *unstructured.Unstructured, dryRun bool) (*unstructured.Unstructured, error) {
	r.seen = append(r.seen, obj.GetResourceVersion())
	return r.Applier.Apply(c, gvr, ns, obj, dryRun)
}

// mustLive reads a core object of the fake cluster and its resourceVersion.
func (g *rig) mustLive(resource, ns, name string) (*unstructured.Unstructured, string) {
	u, err := g.k.Dyn.Resource(schema.GroupVersionResource{Version: "v1", Resource: resource}).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred())
	return u, u.GetResourceVersion()
}
