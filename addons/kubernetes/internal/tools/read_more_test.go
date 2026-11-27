// SPDX-License-Identifier: MIT
package tools_test

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mudler/LocalAI/addons/kubernetes/internal/kubeapi"
	"github.com/mudler/LocalAI/addons/kubernetes/internal/policy"
	"github.com/mudler/LocalAI/addons/kubernetes/internal/tools"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime"
)

func listOf(v any) []map[string]any {
	var out []map[string]any
	for _, x := range v.([]any) {
		out = append(out, x.(map[string]any))
	}
	return out
}

var _ = Describe("the describe, events, logs and top tools", func() {
	It("describe a deployment lists its replica sets, pods and events", func() {
		d := deployment("team-a", "web", 2)
		rs1, rs2 := replicaSet(d, "web-1", "1", "example/app:1"), replicaSet(d, "web-2", "2", "example/app:2")
		p := owned(pod("team-a", "web-2-abc", "node-1", time.Hour), "ReplicaSet", "web-2")
		p.Labels = map[string]string{"app": "web"}
		g := newRig(d, rs1, rs2, p, pod("team-a", "unrelated", "node-1", time.Hour),
			event("team-a", "e1", d, "Deployment", "ScalingReplicaSet", time.Minute),
			event("team-a", "e2", d, "Deployment", "Older", time.Hour),
			event("team-a", "e3", p, "Pod", "Pulled", time.Minute))
		out := g.mustRead(tools.ToolDescribe, map[string]any{"kind": "deploy", "namespace": "team-a", "name": "web"})
		Expect(out["summary"]).To(HaveKeyWithValue("kind", "Deployment"))
		Expect(out["related"]).To(ConsistOf("ReplicaSet/team-a/web-1 (revision 1)", "ReplicaSet/team-a/web-2 (revision 2)", "Pod/team-a/web-2-abc"))
		ev := listOf(out["events"])
		Expect(ev).To(HaveLen(2))
		Expect(ev[0]["reason"]).To(Equal("ScalingReplicaSet"))
	})

	It("describe a node lists its pods in allowed namespaces only", func() {
		g := newRig(node("node-1"), pod("team-a", "a", "node-1", time.Hour), pod("team-b", "b", "node-1", time.Hour), pod("team-a", "c", "node-2", time.Hour))
		g.setPolicy(policy.Policy{Namespaces: []string{"team-a"}})
		out := g.mustRead(tools.ToolDescribe, map[string]any{"kind": "nodes", "name": "node-1"})
		Expect(out["related"]).To(ConsistOf("Pod/team-a/a"))
	})

	It("events of a namespace are newest first and capped by limit", func() {
		p := pod("team-a", "p", "n", time.Hour)
		var objs []runtime.Object
		for i := range 5 {
			objs = append(objs, event("team-a", fmt.Sprintf("e%d", i), p, "Pod", fmt.Sprintf("R%d", i), time.Duration(i+1)*time.Minute))
		}
		g := newRig(objs...)
		out := g.mustRead(tools.ToolEvents, map[string]any{"namespace": "team-a", "limit": 3})
		ev := listOf(out["events"])
		Expect(ev).To(HaveLen(3))
		Expect(ev[0]["reason"]).To(Equal("R0"))
		Expect(ev[2]["reason"]).To(Equal("R2"))
		Expect(out["cut"]).To(BeTrue())
	})

	It("events without namespace read only allowed namespaces", func() {
		a, b := pod("team-a", "a", "n", time.Hour), pod("team-b", "b", "n", time.Hour)
		g := newRig(event("team-a", "ea", a, "Pod", "A", time.Minute), event("team-b", "eb", b, "Pod", "B", time.Minute))
		g.setPolicy(policy.Policy{Namespaces: []string{"team-a"}})
		ev := listOf(g.mustRead(tools.ToolEvents, nil)["events"])
		Expect(ev).To(HaveLen(1))
		Expect(ev[0]["namespace"]).To(Equal("team-a"))
		_, err := g.read(tools.ToolEvents, map[string]any{"namespace": "team-b"})
		Expect(err).To(MatchError(ContainSubstring("(namespaces)")))
	})

	It("logs default to 200 tail lines and pass container, since and previous through", func() {
		g := newRig()
		g.k.SetLogs("team-a", "web-1", "", "line 1\nline 2\n")
		g.k.SetLogs("team-a", "web-1", "side", "side line\n")
		out := g.mustRead(tools.ToolLogs, map[string]any{"namespace": "team-a", "pod": "web-1"})
		Expect(out["logs"]).To(Equal("line 1\nline 2\n"))
		Expect(out["logsCut"]).To(BeFalse())
		g.mustRead(tools.ToolLogs, map[string]any{"namespace": "team-a", "pod": "web-1", "container": "side", "tailLines": 10, "sinceSeconds": 60, "previous": true})
		opts := g.k.LogOptions()
		Expect(opts).To(HaveLen(2))
		Expect(*opts[0].TailLines).To(BeNumerically("==", 200))
		Expect(opts[0].SinceSeconds).To(BeNil())
		Expect(opts[1].Container).To(Equal("side"))
		Expect(*opts[1].TailLines).To(BeNumerically("==", 10))
		Expect(*opts[1].SinceSeconds).To(BeNumerically("==", 60))
		Expect(opts[1].Previous).To(BeTrue())
	})

	It("cuts long and binary logs on a rune boundary, keeping the newest lines", func() {
		g := newRig()
		body := strings.Repeat("é\xff log line\n", (10<<20)/14) + "the last line\n"
		g.k.SetLogs("team-a", "web-1", "", body)
		out := g.mustRead(tools.ToolLogs, map[string]any{"namespace": "team-a", "pod": "web-1"})
		text := out["logs"].(string)
		Expect(len(text)).To(BeNumerically("<=", kubeapi.MaxText))
		Expect(utf8.ValidString(text)).To(BeTrue())
		Expect(out["logsCut"]).To(BeTrue())
	})

	It("refuses logs outside the allowlist", func() {
		g := newRig()
		g.setPolicy(policy.Policy{Namespaces: []string{"team-a"}})
		_, err := g.read(tools.ToolLogs, map[string]any{"namespace": "team-b", "pod": "x"})
		Expect(err).To(MatchError(ContainSubstring("(namespaces)")))
	})

	It("top nodes and pods report CPU millicores and memory bytes", func() {
		g := newRig()
		g.k.SetMetrics([]kubeapi.Usage{{Name: "node-1", CPUMilli: 250, MemoryBytes: 1 << 30}},
			[]kubeapi.Usage{{Namespace: "team-a", Name: "a", CPUMilli: 5, MemoryBytes: 1 << 20}, {Namespace: "team-b", Name: "b", CPUMilli: 9}})
		rows := listOf(g.mustRead(tools.ToolTop, map[string]any{"resource": "nodes"})["rows"])
		Expect(rows[0]).To(HaveKeyWithValue("cpuMillicores", BeNumerically("==", 250)))
		Expect(rows[0]).To(HaveKeyWithValue("memoryBytes", BeNumerically("==", 1<<30)))
		Expect(listOf(g.mustRead(tools.ToolTop, map[string]any{"resource": "pods"})["rows"])).To(HaveLen(2))
		g.setPolicy(policy.Policy{Namespaces: []string{"team-a"}})
		rows = listOf(g.mustRead(tools.ToolTop, map[string]any{"resource": "pods"})["rows"])
		Expect(rows).To(HaveLen(1))
		Expect(rows[0]["name"]).To(Equal("a"))
	})

	It("top without metrics.k8s.io says the cluster serves no metrics", func() {
		g := newRig()
		_, err := g.read(tools.ToolTop, map[string]any{"resource": "nodes"})
		Expect(err).To(MatchError(ContainSubstring("this cluster serves no metrics.k8s.io API: install metrics-server to use top")))
	})
})
