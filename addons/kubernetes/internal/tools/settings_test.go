// SPDX-License-Identifier: MIT
package tools_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/addons/kubernetes/internal/policy"
	"github.com/mudler/LocalAI/addons/kubernetes/internal/tools"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the policy tools of the page", func() {
	It("get-policy lists every cluster's policy and says whether the caller is an administrator", func() {
		g := newRig()
		g.setPolicy(policy.Policy{Namespaces: []string{"team-a"}})
		d := g.mustRead(tools.ToolGetPolicy, nil)
		Expect(d["admin"]).To(BeFalse())
		Expect(d["clusters"]).To(HaveKey(server))
		Expect(d["defaultSet"]).To(BeTrue())
		Expect(d["builtinDefault"]).To(HaveKeyWithValue("writeTools", BeEmpty()))
		Expect(d["builtinDefault"]).To(HaveKeyWithValue("readSecretValues", BeTrue()))
		Expect(d["writeTools"]).To(HaveLen(10))
		g.admin = true
		Expect(g.mustRead(tools.ToolGetPolicy, nil)["admin"]).To(BeTrue())
	})

	It("set-policy refuses a non-administrator and changes nothing", func() {
		g := newRig()
		_, err := g.read(tools.ToolSetPolicy, map[string]any{"key": key, "namespaces": []string{"team-a"}})
		Expect(err).To(MatchError(ContainSubstring("only an administrator")))
		before, _ := g.pol.Get(key)
		_, err = g.read(tools.ToolSetPolicy, map[string]any{"default": true, "namespaces": []string{"team-a"}})
		Expect(err).To(MatchError(ContainSubstring("only an administrator")))
		p, _ := g.pol.Get(key)
		Expect(p).To(Equal(before))
		Expect(g.pol.View().Clusters).To(BeEmpty())
	})

	It("set-policy saves and the next call uses it", func() {
		g := newRig()
		g.admin = true
		d := g.mustRead(tools.ToolSetPolicy, map[string]any{"key": key, "namespaces": []string{"team-a"}, "writeTools": []string{"scale", "exec"}})
		Expect(d["saved"]).To(BeTrue())
		p, _ := g.pol.Get(key)
		Expect(p.Namespaces).To(Equal([]string{"team-a"}))
		Expect(p.ToolEnabled("exec")).To(BeTrue())
		Expect(p.ToolEnabled("apply")).To(BeFalse())
		g.mustRead(tools.ToolSetPolicy, map[string]any{"key": key, "readSecretValues": true})
		p, _ = g.pol.Get(key)
		Expect(p.ReadSecretValues).To(BeTrue())
		Expect(p.Namespaces).To(Equal([]string{"team-a"}), "a field the page leaves out stays")
	})

	It("set-policy refuses a key that is not a cluster key and bad entries", func() {
		g := newRig()
		g.admin = true
		for _, args := range []map[string]any{
			{"key": "not-a-key", "namespaces": []string{"*"}},
			{"key": key, "namespaces": []string{}},
			{"key": key, "writeTools": []string{"rm"}},
			{"key": key, "extra": 1},
			{"key": key, "default": true, "namespaces": []string{"*"}},
			{"key": "https://127.0.0.1:6443", "namespaces": []string{"*"}},
		} {
			_, err := g.read(tools.ToolSetPolicy, args)
			Expect(err).To(HaveOccurred(), args)
		}
		Expect(g.pol.View().Clusters).To(BeEmpty())
	})

	It("set-policy keys a cluster by its server, from a server URL or a connection key", func() {
		g := newRig()
		g.admin = true
		d := g.mustRead(tools.ToolSetPolicy, map[string]any{"key": "https://CLUSTER.example:6443/", "namespaces": []string{"team-a"}})
		Expect(d["key"]).To(Equal(server))
		g.mustRead(tools.ToolSetPolicy, map[string]any{"key": server + "#context=other", "readSecretValues": true})
		p, _ := g.pol.Get(key)
		Expect(p.Namespaces).To(Equal([]string{"team-a"}))
		Expect(p.ReadSecretValues).To(BeTrue())
		// Another context name of the same server finds the same policy.
		g.key = server + "#context=renamed"
		g.cred = strings.ReplaceAll(kubeconfig, "name: dev", "name: renamed")
		g.cred = strings.ReplaceAll(g.cred, "current-context: dev", "current-context: renamed")
		_, err := g.read(tools.ToolList, map[string]any{"kind": "pods", "namespace": "team-b"})
		Expect(err).To(MatchError(ContainSubstring("(namespaces)")))
	})

	It("set-policy sets the default policy of the clusters with none", func() {
		g := newRig()
		g.admin = true
		d := g.mustRead(tools.ToolSetPolicy, map[string]any{"default": true, "writeTools": []string{"scale"}})
		Expect(d["default"]).To(BeTrue())
		p, _ := g.pol.Get(key)
		Expect(p.WriteTools).To(Equal([]string{"scale"}))
		Expect(p.Namespaces).To(Equal([]string{"*"}), "a field the page leaves out stays")
	})

	It("shows Secret values of a cluster no administrator configured", func() {
		g := newRig(secret("team-a", "db"))
		g.pol = policy.Open("")
		g.rt = tools.New(g.k.Factory(), g.pol)
		out := g.mustRead(tools.ToolGet, map[string]any{"kind": "secret", "namespace": "team-a", "name": "db"})
		Expect(out["yaml"]).To(ContainSubstring("c2VjcmV0LXZhbHVl"))
	})

	It("denies every write of a cluster no administrator configured", func() {
		g := newRig(deployment("team-a", "web", 1))
		g.pol = policy.Open("")
		g.rt = tools.New(g.k.Factory(), g.pol)
		Expect(g.mustRead(tools.ToolGet, map[string]any{"kind": "deploy", "namespace": "team-a", "name": "web"})).NotTo(BeNil(), "reads work")
		Expect(g.mustRead(tools.ToolList, map[string]any{"kind": "pods", "allNamespaces": true})).NotTo(BeNil(), "in every namespace")
		_, err := g.preview("op", tools.ToolScale, map[string]any{"kind": "Deployment", "namespace": "team-a", "name": "web", "replicas": 2})
		Expect(err).To(MatchError(ContainSubstring("(writeTools)")))
	})

	It("check holds its namespace to the policy", func() {
		g := newRig()
		g.setPolicy(policy.Policy{Namespaces: []string{"team-a"}})
		d := g.mustRead(tools.ToolCheck, nil)
		Expect(d["check"]).To(HaveKeyWithValue("namespace", "team-a"))
		_, err := g.read(tools.ToolCheck, map[string]any{"namespace": "team-b"})
		Expect(err).To(MatchError(ContainSubstring("(namespaces)")))
		_, err = g.read(tools.ToolCheck, map[string]any{"namespace": "Bad_NS"})
		Expect(err).To(MatchError(ContainSubstring("not a namespace name")))
	})

	It("uses the damaged policy while the policy file is broken, and a save never widens it", func() {
		dir := GinkgoT().TempDir()
		good := policy.Open(dir)
		_, err := good.SetDefault(policy.Policy{Namespaces: []string{"*"}, WriteTools: writes})
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(dir, "policy.json"), []byte("{"), 0o600)).To(Succeed())
		g := newRig(deployment("team-a", "web", 1), node("node-1"))
		g.pol = policy.Open(dir)
		g.rt = tools.New(g.k.Factory(), g.pol)
		g.rt.Admin = adminFn(func() bool { return g.admin })
		Expect(g.mustRead(tools.ToolGetPolicy, nil)["broken"]).To(BeTrue())
		_, err = g.read(tools.ToolCheck, nil)
		Expect(err).To(MatchError(ContainSubstring("(namespaces)")), "no namespace is readable")
		_, err = g.read(tools.ToolList, map[string]any{"kind": "pods"})
		Expect(err).To(MatchError(ContainSubstring("allows no namespace")))
		_, err = g.read(tools.ToolList, map[string]any{"kind": "nodes"})
		Expect(err).NotTo(HaveOccurred(), "cluster-scoped objects stay readable, as under every policy")
		_, err = g.preview("op", tools.ToolScale, map[string]any{"kind": "Deployment", "namespace": "team-a", "name": "web", "replicas": 2})
		Expect(err).To(MatchError(ContainSubstring("(writeTools)")), "writes are off")
		g.admin = true
		_, err = g.read(tools.ToolSetPolicy, map[string]any{"key": key, "readSecretValues": true})
		Expect(err).To(MatchError(ContainSubstring("namespace")), "a partial save starts from the damaged policy, which names no namespace")
		g.mustRead(tools.ToolSetPolicy, map[string]any{"key": key, "namespaces": []string{"team-a"}, "readSecretValues": true})
		p, _ := g.pol.Get(key)
		Expect(p.WriteTools).To(BeEmpty(), "a partial save starts from the policy of a damaged file")
		Expect(p.Namespaces).To(Equal([]string{"team-a"}))
		other, _ := g.pol.Get("https://other.example:6443")
		Expect(other).To(Equal(policy.Damaged()), "the other clusters keep the damaged policy until their policy is entered again")
		r, err := g.rt.Read(ctx, g.env("op", tools.ToolGetPolicy, nil))
		Expect(err).NotTo(HaveOccurred())
		var out reply
		Expect(json.Unmarshal(r.Payload, &out)).To(Succeed())
		Expect(out.Data["broken"]).To(BeFalse())
		Expect(out.Data["defaultSet"]).To(BeTrue(), "the damaged policy is stored as the default")
		Expect(out.Data["default"]).To(HaveKeyWithValue("namespaces", BeEmpty()))
	})

	It("lists the entries whose key cannot be read", func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "policy.json"), []byte(`{"clusters":{"not a server":{"namespaces":["team-x"]}}}`), 0o600)).To(Succeed())
		g := newRig()
		g.pol = policy.Open(dir)
		g.rt = tools.New(g.k.Factory(), g.pol)
		Expect(g.mustRead(tools.ToolGetPolicy, nil)["invalid"]).To(Equal([]any{"not a server"}))
	})
})
