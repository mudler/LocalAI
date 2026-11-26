// SPDX-License-Identifier: MIT
package tools_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/mudler/LocalAI/addons/kubernetes/internal/policy"
	"github.com/mudler/LocalAI/addons/kubernetes/internal/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"
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

		fresh := strings.Replace(secretManifest, "name: db", "name: fresh", 1)
		p, err := g.preview("apply-1", tools.ToolApply, map[string]any{"manifest": fresh})
		Expect(err).NotTo(HaveOccurred())
		clean("apply preview", p.Summary)
		rc, err := g.apply("apply-1", tools.ToolApply, map[string]any{"manifest": fresh}, p)
		Expect(err).NotTo(HaveOccurred())
		clean("apply result", rc.Result.Payload)
		_, err = g.preview("apply-2", tools.ToolApply, map[string]any{"manifest": fresh})
		Expect(err).To(MatchError(ContainSubstring("(readSecretValues)")), "the keys now exist, so a second apply could test a guess")
		clean("apply refusal", []byte(err.Error()))
		args := map[string]any{"kind": "secrets", "namespace": "team-a", "name": "db", "patchType": "merge", "patch": `{"data":{"token":"c2VjcmV0LXZhbHVl","more":"c2VjcmV0LXZhbHVl"}}`}
		p, err = g.preview("patch", tools.ToolPatch, args)
		Expect(err).NotTo(HaveOccurred())
		clean("patch preview", p.Summary)
		Expect(sampleOf(p)["diff"]).To(ContainSubstring("(new value)"))
		rc, err = g.apply("patch", tools.ToolPatch, args, p)
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

	It("refuses a json patch of a Secret that copies, moves or tests a value", func() {
		g := newRig(secret("team-a", "db"))
		for name, patch := range map[string]string{
			"copy to annotation": `[{"op":"copy","from":"/data/password","path":"/metadata/annotations/leak"}]`,
			"move to annotation": `[{"op":"move","from":"/data/password","path":"/metadata/annotations/leak"}]`,
			"copy to label":      `[{"op":"copy","from":"/data/password","path":"/metadata/labels/leak"}]`,
			"copy stringData":    `[{"op":"copy","from":"/stringData/x","path":"/metadata/annotations/leak"}]`,
			"copy of the root":   `[{"op":"copy","from":"","path":"/metadata/annotations/leak"}]`,
			"test right value":   `[{"op":"test","path":"/data/password","value":"c2VjcmV0LXZhbHVl"}]`,
			"test wrong value":   `[{"op":"test","path":"/data/password","value":"d3Jvbmc="}]`,
			"test of metadata":   `[{"op":"test","path":"/metadata/name","value":"db"}]`,
			"copy of metadata":   `[{"op":"copy","from":"/metadata/name","path":"/metadata/annotations/n"}]`,
			"replace a value":    `[{"op":"replace","path":"/data/password","value":"eA=="}]`,
			"upper-case keys":    `[{"OP":"COPY","FROM":"/data/password","PATH":"/metadata/annotations/leak"}]`,
			"escaped pointer":    `[{"op":"add","path":"/dat\u0061/x","value":"eA=="}]`,
			"upper-case data":    `[{"op":"add","path":"/Data/x","value":"eA=="}]`,
			"~1 in a data key":   `[{"op":"replace","path":"/data/a~1b","value":"eA=="}]`,
		} {
			args := map[string]any{"kind": "secrets", "namespace": "team-a", "name": "db", "patchType": "json", "patch": patch}
			_, err := g.preview("op-"+name, tools.ToolPatch, args)
			Expect(err).To(MatchError(ContainSubstring("(readSecretValues)")), name)
			clean(name, []byte(err.Error()))
			_, err = g.apply("op-"+name, tools.ToolPatch, args, contract.Preview{})
			Expect(err).To(MatchError(ContainSubstring("(readSecretValues)")), name)
		}
		Expect(g.k.Writes()).To(BeZero())

		ok := map[string]any{"kind": "secrets", "namespace": "team-a", "name": "db", "patchType": "json",
			"patch": `[{"op":"add","path":"/metadata/labels","value":{"team":"a"}}]`}
		_, err := g.preview("op-label", tools.ToolPatch, ok)
		Expect(err).NotTo(HaveOccurred(), "a json patch of the metadata stays possible")
	})

	It("refuses a json patch of any kind that copies, moves or tests, or names an env list, while values are hidden", func() {
		g := newRig(deployment("team-a", "web", 1), &corev1.ConfigMap{ObjectMeta: meta("team-a", "cfg", time.Hour), Data: map[string]string{"a": "b"}})
		const value = "/spec/template/spec/containers/0/env/0/value"
		for name, c := range map[string]struct{ kind, patch string }{
			"env copy to a label":  {"deploy", `[{"op":"add","path":"/metadata/labels","value":{}},{"op":"copy","from":"` + value + `","path":"/metadata/labels/leak"}]`},
			"env move":             {"deploy", `[{"op":"move","from":"` + value + `","path":"/metadata/labels/leak"}]`},
			"env test right value": {"deploy", `[{"op":"test","path":"` + value + `","value":"inline-token-value"}]`},
			"env test wrong value": {"deploy", `[{"op":"test","path":"` + value + `","value":"wrong"}]`},
			"test of metadata":     {"deploy", `[{"op":"test","path":"/metadata/name","value":"web"}]`},
			"copy of a config map": {"configmaps", `[{"op":"copy","from":"/data/a","path":"/metadata/labels/a"}]`},
			"env rename":           {"deploy", `[{"op":"replace","path":"/spec/template/spec/containers/0/env/0/name","value":"PLAIN"}]`},
			"env add":              {"deploy", `[{"op":"add","path":"/spec/template/spec/containers/0/env/-","value":{"name":"A","value":"b"}}]`},
			"env with upper case":  {"deploy", `[{"op":"replace","path":"/spec/template/spec/containers/0/ENV/0/name","value":"PLAIN"}]`},
			"env escaped":          {"deploy", `[{"op":"replace","path":"/spec/template/spec/containers/0/e\u006ev/0/name","value":"PLAIN"}]`},
			"env of an init list":  {"deploy", `[{"op":"remove","path":"/spec/template/spec/initContainers/0/env"}]`},
			"upper-case op keys":   {"deploy", `[{"OP":"TEST","PATH":"` + value + `","VALUE":"x"}]`},
		} {
			args := map[string]any{"kind": c.kind, "namespace": "team-a", "name": map[string]string{"deploy": "web", "configmaps": "cfg"}[c.kind], "patchType": "json", "patch": c.patch}
			_, err := g.preview("op-"+name, tools.ToolPatch, args)
			Expect(err).To(MatchError(ContainSubstring("(readSecretValues)")), name)
			clean(name, []byte(err.Error()))
			_, err = g.apply("op-"+name, tools.ToolPatch, args, contract.Preview{})
			Expect(err).To(MatchError(ContainSubstring("(readSecretValues)")), name)
		}
		Expect(g.k.Writes()).To(BeZero())
		out := g.mustRead(tools.ToolGet, map[string]any{"kind": "deploy", "namespace": "team-a", "name": "web"})
		Expect(out["yaml"]).NotTo(ContainSubstring("leak"))

		for name, patch := range map[string]string{
			"replicas":          `[{"op":"replace","path":"/spec/replicas","value":3}]`,
			"~1 in a label key": `[{"op":"add","path":"/metadata/labels","value":{}},{"op":"add","path":"/metadata/labels/example.com~1team","value":"a"}]`,
			"environment label": `[{"op":"add","path":"/metadata/labels","value":{}},{"op":"add","path":"/metadata/labels/envoy","value":"a"}]`,
		} {
			_, err := g.preview("ok-"+name, tools.ToolPatch, map[string]any{"kind": "deploy", "namespace": "team-a", "name": "web", "patchType": "json", "patch": patch})
			Expect(err).NotTo(HaveOccurred(), name)
		}
	})

	It("allows a json patch that copies or tests an env value when the policy shows values", func() {
		g := newRig(deployment("team-a", "web", 1))
		g.setPolicy(policy.Policy{Namespaces: []string{"*"}, WriteTools: writes, ReadSecretValues: true})
		_, err := g.preview("op", tools.ToolPatch, map[string]any{"kind": "deploy", "namespace": "team-a", "name": "web", "patchType": "json",
			"patch": `[{"op":"test","path":"/spec/template/spec/containers/0/env/0/value","value":"inline-token-value"}]`})
		Expect(err).NotTo(HaveOccurred())
	})

	It("refuses a server-side apply that sets a hidden value again while values are hidden", func() {
		g := newRig(secret("team-a", "db"), deployment("team-a", "web", 1))
		for name, manifest := range map[string]string{
			"data key":       "apiVersion: v1\nkind: Secret\nmetadata:\n  name: db\n  namespace: team-a\ndata:\n  password: d3Jvbmc=\n",
			"stringData key": "apiVersion: v1\nkind: Secret\nmetadata:\n  name: db\n  namespace: team-a\nstringData:\n  password: wrong\n",
			"env value":      "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\n  namespace: team-a\nspec:\n  template:\n    spec:\n      containers:\n      - name: app\n        env:\n        - name: API_TOKEN\n          value: wrong\n",
		} {
			args := map[string]any{"manifest": manifest}
			_, err := g.preview("op-"+name, tools.ToolApply, args)
			Expect(err).To(MatchError(ContainSubstring("(readSecretValues)")), name)
			Expect(err.Error()).To(ContainSubstring("patch"), name)
			_, err = g.apply("op-"+name, tools.ToolApply, args, contract.Preview{})
			Expect(err).To(MatchError(ContainSubstring("(readSecretValues)")), name)
		}
		Expect(g.k.Writes()).To(BeZero())

		for name, manifest := range map[string]string{
			"new data key":   "apiVersion: v1\nkind: Secret\nmetadata:\n  name: db\n  namespace: team-a\ndata:\n  other: eA==\n",
			"new Secret":     "apiVersion: v1\nkind: Secret\nmetadata:\n  name: fresh\n  namespace: team-a\nstringData:\n  password: x\n",
			"plain env":      "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\n  namespace: team-a\nspec:\n  template:\n    spec:\n      containers:\n      - name: app\n        env:\n        - name: MODE\n          value: dev\n",
			"only metadata":  "apiVersion: v1\nkind: Secret\nmetadata:\n  name: db\n  namespace: team-a\n  labels:\n    a: b\n",
			"init container": "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\n  namespace: team-a\nspec:\n  template:\n    spec:\n      initContainers:\n      - name: app\n        env:\n        - name: API_TOKEN\n          value: x\n",
		} {
			_, err := g.preview("ok-"+name, tools.ToolApply, map[string]any{"manifest": manifest})
			Expect(err).NotTo(HaveOccurred(), name)
		}

		g.setPolicy(policy.Policy{Namespaces: []string{"*"}, WriteTools: writes, ReadSecretValues: true})
		_, err := g.preview("shown", tools.ToolApply, map[string]any{"manifest": "apiVersion: v1\nkind: Secret\nmetadata:\n  name: db\n  namespace: team-a\nstringData:\n  password: x\n"})
		Expect(err).NotTo(HaveOccurred(), "the check applies only while values are hidden")
	})

	It("refuses an apply that sets a live Secret annotation again while values are hidden", func() {
		s := secret("team-a", "db")
		s.Annotations["example.com/api-key"] = "secret-value"
		s.Annotations["kubernetes.io/service-account.name"] = "builder"
		g := newRig(s)
		head := "apiVersion: v1\nkind: Secret\nmetadata:\n  name: db\n  namespace: team-a\n  annotations:\n"
		for name, manifest := range map[string]string{
			"live annotation": head + "    example.com/api-key: guess\n",
			"last applied":    head + "    kubectl.kubernetes.io/last-applied-configuration: guess\n",
		} {
			args := map[string]any{"manifest": manifest}
			_, err := g.preview("op-"+name, tools.ToolApply, args)
			Expect(err).To(MatchError(ContainSubstring("(readSecretValues)")), name)
			_, err = g.apply("op-"+name, tools.ToolApply, args, contract.Preview{})
			Expect(err).To(MatchError(ContainSubstring("(readSecretValues)")), name)
		}
		Expect(g.k.Writes()).To(BeZero())

		for name, manifest := range map[string]string{
			"new annotation":  head + "    example.com/owner: team-a\n",
			"safe annotation": head + "    kubernetes.io/service-account.name: builder\n",
		} {
			_, err := g.preview("ok-"+name, tools.ToolApply, map[string]any{"manifest": manifest})
			Expect(err).NotTo(HaveOccurred(), name)
		}
		_, err := g.preview("new-secret", tools.ToolApply, map[string]any{"manifest": "apiVersion: v1\nkind: Secret\nmetadata:\n  name: fresh\n  namespace: team-a\n  annotations:\n    example.com/api-key: x\n"})
		Expect(err).NotTo(HaveOccurred(), "a Secret that does not exist yet has no value to guess")

		g.setPolicy(policy.Policy{Namespaces: []string{"*"}, WriteTools: writes, ReadSecretValues: true})
		_, err = g.preview("shown", tools.ToolApply, map[string]any{"manifest": head + "    example.com/api-key: guess\n"})
		Expect(err).NotTo(HaveOccurred(), "the check applies only while values are hidden")
	})

	It("pins the apply of a hidden value to the live resourceVersion", func() {
		s := secret("team-a", "db")
		s.ResourceVersion = "7"
		g := newRig(s)
		rec := &rvRecorder{Applier: g.k.Client().Writer}
		g.k.Client().Writer = rec
		_, live := g.mustLive("secrets", "team-a", "db")
		Expect(live).NotTo(BeEmpty())
		g.run("op", tools.ToolApply, map[string]any{"manifest": "apiVersion: v1\nkind: Secret\nmetadata:\n  name: db\n  namespace: team-a\n  labels:\n    a: b\n"})
		Expect(rec.seen).To(HaveLen(3), "the dry run of the preview, the dry run of the apply, the apply")
		for _, rv := range rec.seen {
			Expect(rv).To(Equal(live))
		}

		rec.seen = nil
		g.run("op-new", tools.ToolApply, map[string]any{"manifest": "apiVersion: v1\nkind: Secret\nmetadata:\n  name: fresh\n  namespace: team-a\n"})
		Expect(rec.seen).To(Equal([]string{"", "", ""}), "a new object has nothing to pin")

		rec.seen = nil
		g.setPolicy(policy.Policy{Namespaces: []string{"*"}, WriteTools: writes, ReadSecretValues: true})
		g.run("op-shown", tools.ToolApply, map[string]any{"manifest": "apiVersion: v1\nkind: Secret\nmetadata:\n  name: db\n  namespace: team-a\n  labels:\n    a: c\n"})
		Expect(rec.seen).To(Equal([]string{"", "", ""}), "an ordinary apply keeps its semantics when values are shown")
	})

	It("allows a json patch that copies a Secret value when the policy shows values", func() {
		g := newRig(secret("team-a", "db"))
		g.setPolicy(policy.Policy{Namespaces: []string{"*"}, WriteTools: writes, ReadSecretValues: true})
		args := map[string]any{"kind": "secrets", "namespace": "team-a", "name": "db", "patchType": "json",
			"patch": `[{"op":"copy","from":"/data/password","path":"/metadata/annotations/copy"}]`}
		_, err := g.preview("op", tools.ToolPatch, args)
		Expect(err).NotTo(HaveOccurred())
	})

	It("masks the annotations of a Secret, so a value put there earlier stays hidden", func() {
		s := secret("team-a", "db")
		s.Annotations["leak"] = "secret-value"
		g := newRig(s)
		for _, tool := range []string{tools.ToolGet, tools.ToolDescribe} {
			res, err := g.rt.Read(ctx, g.env("op", tool, map[string]any{"kind": "secret", "namespace": "team-a", "name": "db"}))
			Expect(err).NotTo(HaveOccurred())
			clean(tool, res.Payload)
		}
		args := map[string]any{"kind": "secrets", "namespace": "team-a", "name": "db", "patchType": "merge",
			"patch": `{"metadata":{"annotations":{"leak":"other-value"}}}`}
		p, err := g.preview("op-ann", tools.ToolPatch, args)
		Expect(err).NotTo(HaveOccurred())
		clean("annotation diff", p.Summary)
		Expect(string(p.Summary)).NotTo(ContainSubstring("other-value"))
		Expect(sampleOf(p)["diff"]).To(ContainSubstring("(new value)"))
	})

	It("keeps only the code and the reason of an API error about a Secret", func() {
		g := newRig(secret("team-a", "db"), deployment("team-a", "web", 1))
		echo := func(a k8stesting.Action) (bool, runtime.Object, error) {
			if a.GetResource().Resource != "secrets" {
				return false, nil, nil
			}
			return true, nil, &apierrors.StatusError{ErrStatus: metav1.Status{Status: metav1.StatusFailure, Code: http.StatusUnprocessableEntity,
				Reason: metav1.StatusReasonInvalid, Message: `Secret "db" is invalid: data[password]: Invalid value: "secret-value"`}}
		}
		g.k.Prepend("get", "secrets", echo)
		args := map[string]any{"kind": "secrets", "namespace": "team-a", "name": "db", "patchType": "merge", "patch": `{"metadata":{"labels":{"a":"b"}}}`}
		_, err := g.preview("op", tools.ToolPatch, args)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("422 Invalid"))
		clean("secret error", []byte(err.Error()))
	})
})
