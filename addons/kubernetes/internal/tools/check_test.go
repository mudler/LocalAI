// SPDX-License-Identifier: MIT
package tools_test

import (
	"strings"

	"github.com/mudler/LocalAI/addons/kubernetes/internal/tools"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	authv1 "k8s.io/api/authorization/v1"
)

var _ = Describe("the check tool", func() {
	It("answers version and rules for the bound cluster", func() {
		g := newRig()
		g.k.SetRules([]authv1.ResourceRule{{Verbs: []string{"get"}, APIGroups: []string{""}, Resources: []string{"pods"}}})
		d := g.mustRead(tools.ToolCheck, nil)
		Expect(d["cluster"]).To(Equal(key))
		chk := d["check"].(map[string]any)
		Expect(chk["version"]).To(Equal("v1.37.0"))
		Expect(chk["namespace"]).To(Equal("default"))
		Expect(chk["rules"]).To(HaveLen(1))
		d = g.mustRead(tools.ToolCheck, map[string]any{"cluster": key, "namespace": "team-a"})
		Expect(d["check"].(map[string]any)["namespace"]).To(Equal("team-a"))
	})

	It("refuses a call whose cluster differs from the bound key", func() {
		g := newRig()
		_, err := g.read(tools.ToolCheck, map[string]any{"cluster": "https://other.example:6443#context=dev"})
		Expect(err).To(MatchError(ContainSubstring("not the one the host allowed")))
	})

	It("says the connection has no credential", func() {
		g := newRig()
		g.cred = ""
		_, err := g.read(tools.ToolCheck, nil)
		Expect(err).To(MatchError(ContainSubstring("no kubeconfig")))
	})

	It("never puts the credential in a result or an error", func() {
		for _, mod := range []func(*rig){
			func(g *rig) { g.key = "https://other.example:6443#context=dev" },
			func(g *rig) { g.key = server + "#context=nope" },
			func(g *rig) { g.key = "nonsense" },
			func(g *rig) { g.cred = strings.Replace(kubeconfig, "token:", "tokenFile: /x\n    token:", 1) },
			func(g *rig) { g.cred = strings.Replace(kubeconfig, "token:", "username: "+token+"\n    token:", 1) },
			func(g *rig) { g.cred = kubeconfig + strings.Repeat("#", 17<<10) },
		} {
			g := newRig()
			mod(g)
			_, err := g.read(tools.ToolCheck, nil)
			Expect(err).To(HaveOccurred())
		}
	})
})
