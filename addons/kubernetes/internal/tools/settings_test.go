// SPDX-License-Identifier: MIT
package tools_test

import (
	"encoding/json"
	"os"
	"path/filepath"

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
		Expect(d["clusters"]).To(HaveKey(key))
		Expect(d["writeTools"]).To(HaveLen(10))
		g.admin = true
		Expect(g.mustRead(tools.ToolGetPolicy, nil)["admin"]).To(BeTrue())
	})

	It("set-policy refuses a non-administrator and changes nothing", func() {
		g := newRig()
		_, err := g.read(tools.ToolSetPolicy, map[string]any{"key": key, "namespaces": []string{"team-a"}})
		Expect(err).To(MatchError(ContainSubstring("only an administrator")))
		p, _ := g.pol.Get(key)
		Expect(p).To(Equal(policy.Defaults()))
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
		} {
			_, err := g.read(tools.ToolSetPolicy, args)
			Expect(err).To(HaveOccurred(), args)
		}
		all, _ := g.pol.All()
		Expect(all).To(BeEmpty())
	})

	It("refuses every tool while the policy file is broken", func() {
		dir := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(dir, "policy.json"), []byte("{"), 0o600)).To(Succeed())
		g := newRig()
		g.pol = policy.Open(dir)
		g.rt = tools.New(g.k.Factory(), g.pol)
		_, err := g.read(tools.ToolCheck, nil)
		Expect(err).To(MatchError(ContainSubstring("policy of the add-on cannot be read")))
		r, err := g.rt.Read(ctx, g.env("op", tools.ToolGetPolicy, nil))
		Expect(err).NotTo(HaveOccurred())
		var out reply
		Expect(json.Unmarshal(r.Payload, &out)).To(Succeed())
		Expect(out.Data["broken"]).To(BeTrue())
	})
})
