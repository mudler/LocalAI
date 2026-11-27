// SPDX-License-Identifier: MIT
package tools_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mudler/LocalAI/addons/kubernetes/internal/policy"
	"github.com/mudler/LocalAI/addons/kubernetes/internal/tools"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func rowsOf(d map[string]any) []map[string]any {
	var out []map[string]any
	for _, r := range d["rows"].([]any) {
		out = append(out, r.(map[string]any))
	}
	return out
}

var _ = Describe("the list, get and api-resources tools", func() {
	It("lists pods with their columns, newest first", func() {
		g := newRig(pod("team-a", "old", "node-1", 3*time.Hour), pod("team-a", "new", "node-1", time.Minute), pod("team-b", "other", "node-2", time.Hour))
		d := g.mustRead(tools.ToolList, map[string]any{"kind": "pods", "namespace": "team-a"})
		rows := rowsOf(d)
		Expect(rows).To(HaveLen(2))
		Expect(rows[0]["name"]).To(Equal("new"))
		Expect(rows[0]).To(HaveKeyWithValue("ready", "1/1"))
		Expect(rows[0]).To(HaveKeyWithValue("restarts", BeNumerically("==", 2)))
		Expect(rows[0]).To(HaveKeyWithValue("node", "node-1"))
		Expect(rows[0]).To(HaveKeyWithValue("status", "Running"))
		Expect(rows[1]).To(HaveKeyWithValue("age", "3h0m"))
		Expect(d["cut"]).To(BeFalse())
		Expect(rowsOf(g.mustRead(tools.ToolList, map[string]any{"kind": "po", "allNamespaces": true}))).To(HaveLen(3))
	})

	It("filters list results to allowed namespaces whatever the selectors say", func() {
		g := newRig(pod("team-a", "a", "n", time.Hour), pod("team-b", "b", "n", time.Hour))
		g.setPolicy(policy.Policy{Namespaces: []string{"team-a"}})
		for _, args := range []map[string]any{
			{"kind": "pods", "allNamespaces": true},
			{"kind": "pods"},
			{"kind": "pods", "fieldSelector": "metadata.namespace=team-b"},
			{"kind": "pods", "allNamespaces": true, "fieldSelector": "metadata.namespace=team-b"},
		} {
			d := g.mustRead(tools.ToolList, args)
			for _, r := range rowsOf(d) {
				Expect(r["namespace"]).To(Equal("team-a"), fmt.Sprint(args))
			}
		}
	})

	It("refuses a namespaced call outside the allowlist and names the rule", func() {
		g := newRig(pod("team-b", "b", "n", time.Hour))
		g.setPolicy(policy.Policy{Namespaces: []string{"team-a"}})
		_, err := g.read(tools.ToolList, map[string]any{"kind": "pods", "namespace": "team-b"})
		Expect(err).To(MatchError(ContainSubstring("refused by the connection policy (namespaces)")))
		_, err = g.read(tools.ToolGet, map[string]any{"kind": "pods", "namespace": "team-b", "name": "b"})
		Expect(err).To(MatchError(ContainSubstring("refused by the connection policy (namespaces)")))
		_, err = g.read(tools.ToolGet, map[string]any{"kind": "pods", "name": "b"})
		Expect(err).To(MatchError(ContainSubstring("(namespaces)")), "default is not on the list")
	})

	It("reads cluster-scoped kinds whatever the allowlist", func() {
		g := newRig(node("node-1"), namespace("team-b"))
		g.setPolicy(policy.Policy{Namespaces: []string{"team-a"}})
		rows := rowsOf(g.mustRead(tools.ToolList, map[string]any{"kind": "nodes"}))
		Expect(rows).To(HaveLen(1))
		Expect(rows[0]).To(HaveKeyWithValue("roles", "worker"))
		Expect(rows[0]).To(HaveKeyWithValue("version", "v1.37.0"))
		Expect(rows[0]).NotTo(HaveKey("namespace"))
		Expect(g.mustRead(tools.ToolGet, map[string]any{"kind": "node", "name": "node-1"})["yaml"]).To(ContainSubstring("name: node-1"))
	})

	It("caps a long list and says so", func() {
		var objs []runtime.Object
		for i := range 600 {
			objs = append(objs, pod("team-a", fmt.Sprintf("p-%03d", i), "n", time.Duration(i)*time.Minute))
		}
		g := newRig(objs...)
		d := g.mustRead(tools.ToolList, map[string]any{"kind": "pods", "limit": 500})
		Expect(rowsOf(d)).To(HaveLen(500))
		Expect(d["cut"]).To(BeTrue())
		Expect(d["count"]).To(BeNumerically("==", 500))
	})

	It("shrinks rows that would pass the payload cap and stays valid JSON", func() {
		var objs []runtime.Object
		long := strings.Repeat("x", 240)
		for i := range 500 {
			s := &corev1.Service{ObjectMeta: meta("team-a", fmt.Sprintf("%s-%03d", long, i), time.Minute),
				Spec: corev1.ServiceSpec{Type: corev1.ServiceType(long), ClusterIP: long}}
			for p := range 40 {
				s.Spec.Ports = append(s.Spec.Ports, corev1.ServicePort{Port: int32(10000 + p), Protocol: "TCP"})
			}
			s.Status.Conditions = []metav1.Condition{{Type: "Ready", Status: "True"}}
			objs = append(objs, s)
		}
		g := newRig(objs...)
		res, err := g.rt.Read(ctx, g.env("op", tools.ToolList, map[string]any{"kind": "services", "limit": 500}))
		Expect(err).NotTo(HaveOccurred())
		Expect(len(res.Payload)).To(BeNumerically("<=", tools.MaxPayload))
		var r reply
		Expect(json.Unmarshal(res.Payload, &r)).To(Succeed())
		Expect(r.Data["cut"]).To(BeTrue())
		Expect(len(rowsOf(r.Data))).To(BeNumerically("<", 500))
	})

	It("get returns YAML without managedFields and last-applied", func() {
		d := deployment("team-a", "web", 2)
		d.Annotations = map[string]string{"kubectl.kubernetes.io/last-applied-configuration": `{"x":1}`, "team": "a"}
		g := newRig(d)
		out := g.mustRead(tools.ToolGet, map[string]any{"kind": "deployments.apps", "namespace": "team-a", "name": "web"})
		y := out["yaml"].(string)
		Expect(y).To(ContainSubstring("name: web"))
		Expect(y).To(ContainSubstring("team: a"))
		Expect(y).NotTo(ContainSubstring("last-applied-configuration"))
		Expect(y).NotTo(ContainSubstring("managedFields"))
		Expect(out["kind"]).To(Equal("Deployment"))
	})

	It("get of a missing object passes 404 NotFound through", func() {
		g := newRig()
		_, err := g.read(tools.ToolGet, map[string]any{"kind": "pods", "namespace": "team-a", "name": "nope"})
		Expect(err).To(MatchError(ContainSubstring("404 NotFound")))
	})

	It("refuses an unknown kind and sends events to the events tool", func() {
		g := newRig()
		_, err := g.read(tools.ToolList, map[string]any{"kind": "gizmos"})
		Expect(err).To(MatchError(ContainSubstring("api-resources")))
		_, err = g.read(tools.ToolList, map[string]any{"kind": "events"})
		Expect(err).To(MatchError(ContainSubstring("events tool")))
	})

	It("api-resources lists kinds with group, version, namespaced and verbs, filtered by group", func() {
		g := newRig()
		d := g.mustRead(tools.ToolAPIResources, nil)
		Expect(d["count"]).To(BeNumerically(">", 10))
		d = g.mustRead(tools.ToolAPIResources, map[string]any{"group": "apps"})
		rows := rowsOf(map[string]any{"rows": d["resources"]})
		Expect(rows).To(HaveLen(4))
		Expect(rows[0]).To(HaveKeyWithValue("group", "apps"))
		Expect(rows[0]).To(HaveKey("verbs"))
		d = g.mustRead(tools.ToolAPIResources, map[string]any{"group": "core"})
		for _, r := range rowsOf(map[string]any{"rows": d["resources"]}) {
			Expect(r["group"]).To(Equal(""))
			Expect(r["name"]).NotTo(ContainSubstring("/"))
		}
	})
})
