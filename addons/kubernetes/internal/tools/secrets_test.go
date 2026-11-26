// SPDX-License-Identifier: MIT
package tools_test

import (
	"encoding/json"

	"github.com/mudler/LocalAI/addons/kubernetes/internal/policy"
	"github.com/mudler/LocalAI/addons/kubernetes/internal/tools"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const secretManifest = `apiVersion: v1
kind: Secret
metadata:
  name: db
  namespace: team-a
stringData:
  password: secret-value
data:
  token: c2VjcmV0LXZhbHVl
`

// clean fails when a Secret value appears in a text.
func clean(what string, b []byte) {
	for _, leak := range []string{"secret-value", "c2VjcmV0LXZhbHVl", "inline-token-value"} {
		Expect(string(b)).NotTo(ContainSubstring(leak), what)
	}
}

var _ = Describe("Secret values", func() {
	It("hides Secret values in every tool that can return them", func() {
		g := newRig(secret("team-a", "db"), deployment("team-a", "web", 1))
		for _, kind := range []string{"secret", "Secret", "secrets", "v1/Secret", "secrets.v1."} {
			for _, tool := range []string{tools.ToolGet, tools.ToolDescribe, tools.ToolList} {
				args := map[string]any{"kind": kind, "namespace": "team-a"}
				if tool != tools.ToolList {
					args["name"] = "db"
				}
				res, err := g.rt.Read(ctx, g.env("op", tool, args))
				Expect(err).NotTo(HaveOccurred(), kind+" "+tool)
				clean(kind+" "+tool, res.Payload)
			}
			p, err := g.preview("op-p-"+kind, tools.ToolPatch, map[string]any{"kind": kind, "namespace": "team-a", "name": "db", "patchType": "merge", "patch": `{"metadata":{"labels":{"a":"b"}}}`})
			Expect(err).NotTo(HaveOccurred(), kind)
			clean(kind+" patch preview", p.Summary)
		}
		out := g.mustRead(tools.ToolGet, map[string]any{"kind": "secret", "namespace": "team-a", "name": "db"})
		Expect(out["yaml"]).To(ContainSubstring("password: <12 bytes>"))

		res, err := g.rt.Read(ctx, g.env("op", tools.ToolGet, map[string]any{"kind": "deploy", "namespace": "team-a", "name": "web"}))
		Expect(err).NotTo(HaveOccurred())
		clean("deployment", res.Payload)
		Expect(string(res.Payload)).To(ContainSubstring("secretKeyRef"))

		for _, op := range []string{"apply-1", "apply-2"} {
			p, err := g.preview(op, tools.ToolApply, map[string]any{"manifest": secretManifest})
			Expect(err).NotTo(HaveOccurred())
			clean("apply preview", p.Summary)
			rc, err := g.apply(op, tools.ToolApply, map[string]any{"manifest": secretManifest}, p)
			Expect(err).NotTo(HaveOccurred())
			clean("apply result", rc.Result.Payload)
		}
		args := map[string]any{"kind": "secrets", "namespace": "team-a", "name": "db", "patchType": "merge", "patch": `{"data":{"token":"c2VjcmV0LXZhbHVl","more":"c2VjcmV0LXZhbHVl"}}`}
		p, err := g.preview("patch", tools.ToolPatch, args)
		Expect(err).NotTo(HaveOccurred())
		clean("patch preview", p.Summary)
		Expect(sampleOf(p)["diff"]).To(ContainSubstring("(new value)"))
		rc, err := g.apply("patch", tools.ToolPatch, args, p)
		Expect(err).NotTo(HaveOccurred())
		clean("patch result", rc.Result.Payload)
	})

	It("shows them when the policy allows it", func() {
		g := newRig(secret("team-a", "db"))
		g.setPolicy(policy.Policy{Namespaces: []string{"*"}, ReadSecretValues: true})
		out := g.mustRead(tools.ToolGet, map[string]any{"kind": "secret", "namespace": "team-a", "name": "db"})
		b, _ := json.Marshal(out)
		Expect(string(b)).To(ContainSubstring("c2VjcmV0LXZhbHVl"))
	})
})
