// SPDX-License-Identifier: MIT
package tools_test

import (
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/addons/kubernetes/internal/policy"
	"github.com/mudler/LocalAI/addons/kubernetes/internal/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"
)

func configMap(ns, name, value string) string {
	return fmt.Sprintf("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: %s\n  namespace: %s\ndata:\n  mode: %s\n", name, ns, value)
}

var _ = Describe("apply and patch", func() {
	It("apply previews a dry-run diff and writes nothing until applied", func() {
		g := newRig(deployment("team-a", "web", 2))
		manifest := "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\n  namespace: team-a\nspec:\n  replicas: 4\n"
		p, err := g.preview("op", tools.ToolApply, map[string]any{"manifest": manifest})
		Expect(err).NotTo(HaveOccurred())
		s := sampleOf(p)
		Expect(s["change"]).To(Equal("update"))
		Expect(s["diff"]).To(ContainSubstring("-  replicas: 2"))
		Expect(s["diff"]).To(ContainSubstring("+  replicas: 4"))
		Expect(s["diff"]).To(HavePrefix("--- live\n+++ dry-run\n"))
		Expect(*g.deploy("team-a", "web").Spec.Replicas).To(BeNumerically("==", 2))
		Expect(g.k.Writes()).To(BeZero())
	})

	It("apply after the preview writes and returns the same diff", func() {
		g := newRig()
		p, err := g.preview("op", tools.ToolApply, map[string]any{"manifest": configMap("team-a", "settings", "fast")})
		Expect(err).NotTo(HaveOccurred())
		Expect(sampleOf(p)["change"]).To(Equal("create"))
		rc, err := g.apply("op", tools.ToolApply, map[string]any{"manifest": configMap("team-a", "settings", "fast")}, p)
		Expect(err).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		data := resultData(rc)
		Expect(data["applied"]).To(Equal("ConfigMap/team-a/settings"))
		Expect(data["diff"]).To(Equal(sampleOf(p)["diff"]))
		Expect(g.mustRead(tools.ToolGet, map[string]any{"kind": "cm", "namespace": "team-a", "name": "settings"})["yaml"]).To(ContainSubstring("mode: fast"))
	})

	It("holds the namespace of the manifest to the allowlist", func() {
		g := newRig()
		g.setPolicy(policy.Policy{Namespaces: []string{"team-a"}, WriteTools: writes})
		noNs := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: x\ndata: {a: b}\n"
		_, err := g.preview("op1", tools.ToolApply, map[string]any{"manifest": noNs})
		Expect(err).To(MatchError(ContainSubstring("(namespaces): the namespace default")))
		p, err := g.preview("op2", tools.ToolApply, map[string]any{"manifest": noNs, "namespace": "team-a"})
		Expect(err).NotTo(HaveOccurred())
		Expect(sampleOf(p)["namespace"]).To(Equal("team-a"))
		_, err = g.preview("op3", tools.ToolApply, map[string]any{"manifest": configMap("team-b", "x", "y"), "namespace": "team-a"})
		Expect(err).To(MatchError(ContainSubstring("names the namespace team-b and the call names team-a")))
		_, err = g.preview("op4", tools.ToolApply, map[string]any{"manifest": configMap("team-b", "x", "y")})
		Expect(err).To(MatchError(ContainSubstring("(namespaces)")))
		_, err = g.preview("op5", tools.ToolPatch, map[string]any{"kind": "cm", "namespace": "team-b", "name": "x", "patchType": "merge", "patch": "{}"})
		Expect(err).To(MatchError(ContainSubstring("(namespaces)")))
	})

	It("refuses cluster-scoped kinds for apply and patch", func() {
		g := newRig(namespace("team-a"))
		for _, m := range []string{
			"apiVersion: v1\nkind: Namespace\nmetadata:\n  name: evil\n",
			"apiVersion: rbac.authorization.k8s.io/v1\nkind: ClusterRole\nmetadata:\n  name: all\nrules: []\n",
			"apiVersion: apiextensions.k8s.io/v1\nkind: CustomResourceDefinition\nmetadata:\n  name: gizmos.example.com\n",
		} {
			_, err := g.preview("op", tools.ToolApply, map[string]any{"manifest": m})
			Expect(err).To(MatchError(ContainSubstring("(clusterScoped)")), m)
		}
		_, err := g.preview("op", tools.ToolPatch, map[string]any{"kind": "namespaces", "name": "team-a", "patchType": "merge", "patch": `{"metadata":{"labels":{"a":"b"}}}`})
		Expect(err).To(MatchError(ContainSubstring("(clusterScoped)")))
	})

	It("refuses multi-document and List manifests", func() {
		g := newRig()
		for _, m := range []string{
			configMap("team-a", "a", "1") + "---\n" + configMap("team-a", "b", "2"),
			"apiVersion: v1\nkind: List\nitems: []\n",
			"apiVersion: v1\nkind: ConfigMapList\nitems: []\n",
			"just text",
			"apiVersion: v1\nkind: ConfigMap\n",
		} {
			_, err := g.preview("op", tools.ToolApply, map[string]any{"manifest": m})
			Expect(err).To(HaveOccurred(), m)
		}
		_, err := g.preview("op", tools.ToolApply, map[string]any{"manifest": configMap("team-a", "a", "1") + "---\n"})
		Expect(err).NotTo(HaveOccurred(), "a trailing separator is one object")
		for _, m := range []string{
			"apiVersion: v1\nkind: List\nmetadata:\n  name: x\nitems: []\n",
			"apiVersion: v1\nkind: ConfigMapList\nmetadata:\n  name: x\nitems: []\n",
		} {
			_, err = g.preview("op", tools.ToolApply, map[string]any{"manifest": m})
			Expect(err).To(HaveOccurred(), m)
		}
		_, err = g.preview("op", tools.ToolApply, map[string]any{"manifest": "apiVersion: example.com/v1\nkind: IPAllowList\nmetadata:\n  name: office\n  namespace: team-a\nspec:\n  cidrs: [192.0.2.0/24]\n"})
		Expect(err).NotTo(HaveOccurred(), "a custom kind whose name ends in List is one object")
		_, err = g.preview("op", tools.ToolApply, map[string]any{"manifest": strings.Repeat("#", 16<<10+1)})
		Expect(err).To(MatchError(ContainSubstring("at most")))
	})

	It("patch with each patch type previews a diff", func() {
		g := newRig(deployment("team-a", "web", 2))
		for pt, patch := range map[string]string{
			"strategic": `{"spec":{"replicas":5}}`,
			"merge":     "spec:\n  replicas: 5\n",
			"json":      `[{"op":"replace","path":"/spec/replicas","value":5}]`,
		} {
			p, err := g.preview("op-"+pt, tools.ToolPatch, map[string]any{"kind": "deploy", "namespace": "team-a", "name": "web", "patchType": pt, "patch": patch})
			Expect(err).NotTo(HaveOccurred(), pt)
			Expect(sampleOf(p)["diff"]).To(ContainSubstring("+  replicas: 5"), pt)
		}
		Expect(g.k.Writes()).To(BeZero())
		_, data := g.run("op", tools.ToolPatch, map[string]any{"kind": "deploy", "namespace": "team-a", "name": "web", "patchType": "merge", "patch": `{"spec":{"replicas":5}}`})
		Expect(data["patched"]).To(Equal("Deployment/team-a/web"))
		Expect(*g.deploy("team-a", "web").Spec.Replicas).To(BeNumerically("==", 5))
		_, err := g.preview("op", tools.ToolPatch, map[string]any{"kind": "deploy", "namespace": "team-a", "name": "web", "patchType": "json", "patch": `{"spec":{}}`})
		Expect(err).To(MatchError(ContainSubstring("list of operations")))
	})

	It("passes the 415 of a strategic patch on a custom resource through", func() {
		g := newRig()
		g.k.Dyn.PrependReactor("get", "widgets", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, widget("team-a", "w"), nil
		})
		_, err := g.preview("op", tools.ToolPatch, map[string]any{"kind": "widgets", "namespace": "team-a", "name": "w", "patchType": "strategic", "patch": `{"spec":{"size":2}}`})
		Expect(err).To(MatchError(ContainSubstring("415 UnsupportedMediaType")))
	})

	It("a stale diff rejects the apply", func() {
		g := newRig(deployment("team-a", "web", 2))
		args := map[string]any{"kind": "deploy", "namespace": "team-a", "name": "web", "patchType": "merge", "patch": `{"spec":{"paused":true}}`}
		p, err := g.preview("op", tools.ToolPatch, args)
		Expect(err).NotTo(HaveOccurred())
		g.run("other", tools.ToolScale, scaleArgs)
		rc, err := g.apply("op", tools.ToolPatch, args, p)
		Expect(err).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptRejected))
		Expect(g.deploy("team-a", "web").Spec.Paused).To(BeFalse())
	})

	It("cuts a long diff and says so", func() {
		g := newRig()
		var b strings.Builder
		b.WriteString("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: big\n  namespace: team-a\ndata:\n")
		for i := range 400 {
			fmt.Fprintf(&b, "  key%04d: %s\n", i, strings.Repeat("v", 25))
		}
		p, err := g.preview("op", tools.ToolApply, map[string]any{"manifest": b.String()})
		Expect(err).NotTo(HaveOccurred())
		s := sampleOf(p)
		Expect(s["diffCut"]).To(BeTrue())
		Expect(len(s["diff"].(string))).To(BeNumerically("<=", 10<<10))
	})

	It("marks a changed masked env value on the card and does not call it no change", func() {
		g := newRig(deployment("team-a", "web", 2))
		patch := `{"spec":{"template":{"spec":{"containers":[{"name":"app","env":[{"name":"API_TOKEN","value":"rotated-value"}]}]}}}}`
		args := map[string]any{"kind": "deploy", "namespace": "team-a", "name": "web", "patchType": "strategic", "patch": patch}
		p, err := g.preview("op", tools.ToolPatch, args)
		Expect(err).NotTo(HaveOccurred())
		s := sampleOf(p)
		Expect(s["change"]).To(Equal("update"))
		Expect(s["diff"]).To(ContainSubstring("*** (new value)"))
		Expect(string(p.Summary)).NotTo(ContainSubstring("rotated-value"))
		Expect(string(p.Summary)).NotTo(ContainSubstring("inline-token-value"))
	})

	It("makes the preview stale when only a masked env value changed", func() {
		g := newRig(deployment("team-a", "web", 2))
		patch := `{"spec":{"template":{"spec":{"containers":[{"name":"app","env":[{"name":"API_TOKEN","value":"rotated-value"}]}]}}}}`
		args := map[string]any{"kind": "deploy", "namespace": "team-a", "name": "web", "patchType": "strategic", "patch": patch}
		p, err := g.preview("op", tools.ToolPatch, args)
		Expect(err).NotTo(HaveOccurred())
		d := g.deploy("team-a", "web")
		d.Spec.Template.Spec.Containers[0].Env[0].Value = "changed-elsewhere"
		_, err = g.k.Typed.AppsV1().Deployments("team-a").Update(ctx, d, metav1.UpdateOptions{})
		Expect(err).NotTo(HaveOccurred())
		rc, err := g.apply("op", tools.ToolPatch, args, p)
		Expect(err).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptRejected))
	})
})
