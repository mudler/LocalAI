// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/addons/kubernetes/internal/policy"
	"github.com/mudler/LocalAI/addons/kubernetes/internal/tools"
	"github.com/mudler/LocalAI/addons/kubernetes/kubetest"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	token  = "kube-token-very-secret-0123"
	server = "https://cluster.example:6443"
	key    = server + "#context=dev"
)

var ctx = context.Background()

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

// setPolicy stores the policy of the rig's cluster.
func (g *rig) setPolicy(p policy.Policy) {
	_, err := g.pol.Set(key, p)
	Expect(err).NotTo(HaveOccurred())
}
