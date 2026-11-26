// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"fmt"
	"time"

	"github.com/mudler/LocalAI/addons/kubernetes/internal/policy"
	"github.com/mudler/LocalAI/addons/kubernetes/internal/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"
)

func (g *rig) podNames() []string {
	l, err := g.k.Typed.CoreV1().Pods("").List(context.Background(), metav1.ListOptions{})
	Expect(err).NotTo(HaveOccurred())
	var out []string
	for _, p := range l.Items {
		out = append(out, p.Namespace+"/"+p.Name)
	}
	return out
}

func strs(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

var _ = Describe("drain", func() {
	drainArgs := map[string]any{"node": "node-1"}

	It("cordons, evicts and reports done", func() {
		g := newRig(node("node-1"), owned(pod("team-a", "web-1", "node-1", time.Hour), "ReplicaSet", "web"),
			owned(pod("team-a", "web-2", "node-2", time.Hour), "ReplicaSet", "web"))
		p, err := g.preview("op", tools.ToolDrain, drainArgs)
		Expect(err).NotTo(HaveOccurred())
		Expect(sampleOf(p)["pods"]).To(ConsistOf("team-a/web-1"))
		Expect(g.k.Writes()).To(BeZero())
		_, data := g.run("op", tools.ToolDrain, drainArgs)
		Expect(data["done"]).To(BeTrue())
		Expect(strs(data["evicted"])).To(ConsistOf("team-a/web-1"))
		Expect(g.podNames()).To(ConsistOf("team-a/web-2"))
		n, _ := g.k.Typed.CoreV1().Nodes().Get(context.Background(), "node-1", metav1.GetOptions{})
		Expect(n.Spec.Unschedulable).To(BeTrue())
	})

	It("reports a pod a PDB protects as blocked and not done", func() {
		g := newRig(node("node-1"), owned(pod("team-a", "web-1", "node-1", time.Hour), "ReplicaSet", "web"),
			owned(pod("team-a", "db-1", "node-1", time.Hour), "StatefulSet", "db"))
		g.k.Typed.PrependReactor("create", "pods", func(a k8stesting.Action) (bool, runtime.Object, error) {
			if a.GetSubresource() == "eviction" && a.(k8stesting.CreateAction).GetObject().(*policyv1.Eviction).Name == "web-1" {
				return true, nil, apierrors.NewTooManyRequests("Cannot evict pod as it would violate the pod's disruption budget.", 10)
			}
			return false, nil, nil
		})
		_, data := g.run("op", tools.ToolDrain, drainArgs)
		Expect(data["done"]).To(BeFalse())
		Expect(strs(data["evicted"])).To(ConsistOf("team-a/db-1"))
		blocked := listOf(data["blocked"])
		Expect(blocked).To(HaveLen(1))
		Expect(blocked[0]["pod"]).To(Equal("team-a/web-1"))
		Expect(blocked[0]["reason"]).To(ContainSubstring("PodDisruptionBudget"))
	})

	It("refuses emptyDir pods unless deleteEmptyDirData", func() {
		p := owned(pod("team-a", "cache-1", "node-1", time.Hour), "ReplicaSet", "cache")
		p.Spec.Volumes = []corev1.Volume{{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}}
		g := newRig(node("node-1"), p)
		_, err := g.preview("op", tools.ToolDrain, drainArgs)
		Expect(err).To(MatchError(ContainSubstring("team-a/cache-1 (emptyDir")))
		_, data := g.run("op-2", tools.ToolDrain, map[string]any{"node": "node-1", "deleteEmptyDirData": true})
		Expect(data["done"]).To(BeTrue())
	})

	It("refuses unmanaged pods before evicting anything", func() {
		g := newRig(node("node-1"), pod("team-a", "bare", "node-1", time.Hour), owned(pod("team-a", "web-1", "node-1", time.Hour), "ReplicaSet", "web"))
		_, err := g.preview("op", tools.ToolDrain, drainArgs)
		Expect(err).To(MatchError(ContainSubstring("team-a/bare (unmanaged")))
		Expect(g.k.Writes()).To(BeZero())
	})

	It("skips DaemonSet and mirror pods by default", func() {
		mirror := owned(pod("kube-system", "etcd-node-1", "node-1", time.Hour), "Node", "node-1")
		mirror.Annotations = map[string]string{"kubernetes.io/config.mirror": "abc"}
		done := owned(pod("team-a", "job-1", "node-1", time.Hour), "Job", "job")
		done.Status.Phase = corev1.PodSucceeded
		g := newRig(node("node-1"), owned(pod("kube-system", "proxy", "node-1", time.Hour), "DaemonSet", "proxy"), mirror, done)
		_, data := g.run("op", tools.ToolDrain, drainArgs)
		Expect(data["done"]).To(BeTrue())
		Expect(data["evicted"]).To(BeEmpty())
		Expect(g.podNames()).To(HaveLen(3))
		_, err := g.preview("op-2", tools.ToolDrain, map[string]any{"node": "node-1", "ignoreDaemonSets": false})
		Expect(err).To(MatchError(ContainSubstring("kube-system/proxy (DaemonSet")))
	})

	It("refuses a pod outside the namespace allowlist before evicting anything", func() {
		g := newRig(node("node-1"), owned(pod("team-a", "a", "node-1", time.Hour), "ReplicaSet", "a"), owned(pod("team-b", "b", "node-1", time.Hour), "ReplicaSet", "b"))
		g.setPolicy(policy.Policy{Namespaces: []string{"team-a"}, WriteTools: writes})
		_, err := g.preview("op", tools.ToolDrain, drainArgs)
		Expect(err).To(MatchError(ContainSubstring("(namespaces)")))
		Expect(err).To(MatchError(ContainSubstring("team-b/b")))
		Expect(g.k.Writes()).To(BeZero())
	})

	It("stops at timeoutSeconds and lists pending pods; a second call continues", func() {
		g := newRig(node("node-1"), owned(pod("team-a", "slow", "node-1", time.Hour), "ReplicaSet", "slow"))
		// The API accepts the eviction, and the pod takes long to go.
		g.k.Typed.PrependReactor("create", "pods", func(a k8stesting.Action) (bool, runtime.Object, error) {
			return a.GetSubresource() == "eviction", nil, nil
		})
		slept := 0
		g.rt.Sleep = func(context.Context, time.Duration) error { slept++; g.now = g.now.Add(2 * time.Second); return nil }
		_, data := g.run("op", tools.ToolDrain, map[string]any{"node": "node-1", "timeoutSeconds": 10})
		Expect(data["done"]).To(BeFalse())
		Expect(strs(data["pending"])).To(ConsistOf("team-a/slow"))
		Expect(slept).To(Equal(5))
	})

	It("keeps one call inside the time budget: slow evictions leave the rest for the next call", func() {
		var objs []runtime.Object
		objs = append(objs, node("node-1"))
		for i := range 8 {
			objs = append(objs, owned(pod("team-a", fmt.Sprintf("web-%d", i), "node-1", time.Hour), "ReplicaSet", "web"))
		}
		g := newRig(objs...)
		start := g.now
		// Each eviction takes ten seconds.
		g.k.Typed.PrependReactor("create", "pods", func(a k8stesting.Action) (bool, runtime.Object, error) {
			if a.GetSubresource() == "eviction" {
				g.now = g.now.Add(10 * time.Second)
			}
			return false, nil, nil
		})
		g.rt.Sleep = func(context.Context, time.Duration) error { g.now = g.now.Add(2 * time.Second); return nil }
		_, data := g.run("op", tools.ToolDrain, drainArgs)
		Expect(data["done"]).To(BeFalse())
		Expect(g.now.Sub(start)).To(BeNumerically("<=", 55*time.Second), "the call ends inside the host limit")
		evicted, remaining := strs(data["evicted"]), strs(data["remaining"])
		Expect(len(evicted)).To(BeNumerically(">=", 1))
		Expect(len(evicted)).To(BeNumerically("<", 8))
		Expect(len(evicted) + len(remaining)).To(Equal(8))
		Expect(data["note"]).To(ContainSubstring("call drain again"))

		_, data = g.run("op-2", tools.ToolDrain, drainArgs)
		Expect(strs(data["evicted"])).To(ConsistOf(remainingOf(remaining)))
	})

	It("bounds the evictions of one call", func() {
		objs := []runtime.Object{node("node-1")}
		for i := range 60 {
			objs = append(objs, owned(pod("team-a", fmt.Sprintf("web-%02d", i), "node-1", time.Hour), "ReplicaSet", "web"))
		}
		g := newRig(objs...)
		_, data := g.run("op", tools.ToolDrain, drainArgs)
		Expect(data["done"]).To(BeFalse())
		Expect(strs(data["evicted"])).To(HaveLen(tools.MaxEvictions))
		Expect(strs(data["remaining"])).To(HaveLen(60 - tools.MaxEvictions))
	})

	It("reports a failure after the cordon as a partial result, never as nothing changed", func() {
		g := newRig(node("node-1"), owned(pod("team-a", "web-1", "node-1", time.Hour), "ReplicaSet", "web"),
			owned(pod("team-a", "web-2", "node-1", time.Hour), "ReplicaSet", "web"))
		g.k.Typed.PrependReactor("create", "pods", func(a k8stesting.Action) (bool, runtime.Object, error) {
			if a.GetSubresource() == "eviction" {
				return true, nil, apierrors.NewInternalError(fmt.Errorf("etcd is down"))
			}
			return false, nil, nil
		})
		rc, data := g.run("op", tools.ToolDrain, drainArgs)
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		Expect(data["cordoned"]).To(BeTrue())
		Expect(data["done"]).To(BeFalse())
		Expect(data["error"]).To(ContainSubstring("500"))
		Expect(strs(data["remaining"])).To(ConsistOf("team-a/web-1", "team-a/web-2"))
		n, _ := g.k.Typed.CoreV1().Nodes().Get(context.Background(), "node-1", metav1.GetOptions{})
		Expect(n.Spec.Unschedulable).To(BeTrue())
	})

	It("tells API throttling apart from a PodDisruptionBudget", func() {
		g := newRig(node("node-1"), owned(pod("team-a", "web-1", "node-1", time.Hour), "ReplicaSet", "web"))
		g.k.Typed.PrependReactor("create", "pods", func(a k8stesting.Action) (bool, runtime.Object, error) {
			if a.GetSubresource() == "eviction" {
				return true, nil, apierrors.NewTooManyRequests("Too many requests, please try again later.", 1)
			}
			return false, nil, nil
		})
		_, data := g.run("op", tools.ToolDrain, drainArgs)
		Expect(data["done"]).To(BeFalse())
		Expect(data["blocked"]).To(BeEmpty())
		Expect(strs(data["remaining"])).To(ConsistOf("team-a/web-1"))
		Expect(data["note"]).To(ContainSubstring("throttl"))
	})

	It("backs off briefly after a throttled eviction, and never past the budget", func() {
		g := newRig(node("node-1"), owned(pod("team-a", "web-1", "node-1", time.Hour), "ReplicaSet", "web"),
			owned(pod("team-a", "web-2", "node-1", time.Hour), "ReplicaSet", "web"), owned(pod("team-a", "web-3", "node-1", time.Hour), "ReplicaSet", "web"))
		retry := 1
		g.k.Typed.PrependReactor("create", "pods", func(a k8stesting.Action) (bool, runtime.Object, error) {
			if a.GetSubresource() == "eviction" {
				return true, nil, apierrors.NewTooManyRequests("Too many requests, please try again later.", retry)
			}
			return false, nil, nil
		})
		var waits []time.Duration
		g.rt.Sleep = func(_ context.Context, d time.Duration) error {
			waits = append(waits, d)
			g.now = g.now.Add(d)
			return nil
		}
		_, data := g.run("op", tools.ToolDrain, drainArgs)
		Expect(waits).To(Equal([]time.Duration{time.Second, time.Second}), "one short wait after each throttled eviction but the last")
		Expect(strs(data["remaining"])).To(ConsistOf("team-a/web-1", "team-a/web-2", "team-a/web-3"))

		retry, waits = 30, nil
		_, data = g.run("op-2", tools.ToolDrain, drainArgs)
		Expect(waits).NotTo(BeEmpty())
		for _, w := range waits {
			Expect(w).To(BeNumerically("<=", 2*time.Second), "a long Retry-After is cut to a brief wait")
		}
		Expect(strs(data["remaining"])).To(HaveLen(3))
	})

	It("stops evicting when a back-off would cross the budget", func() {
		g := newRig(node("node-1"), owned(pod("team-a", "web-1", "node-1", time.Hour), "ReplicaSet", "web"),
			owned(pod("team-a", "web-2", "node-1", time.Hour), "ReplicaSet", "web"))
		evictions := 0
		g.k.Typed.PrependReactor("create", "pods", func(a k8stesting.Action) (bool, runtime.Object, error) {
			if a.GetSubresource() == "eviction" {
				// The first eviction answers 44 seconds into the 50 second
				// budget: a 2 second wait would leave less than the reserve.
				evictions++
				g.now = g.now.Add(44 * time.Second)
				return true, nil, apierrors.NewTooManyRequests("Too many requests, please try again later.", 2)
			}
			return false, nil, nil
		})
		var waits []time.Duration
		g.rt.Sleep = func(_ context.Context, d time.Duration) error {
			waits = append(waits, d)
			g.now = g.now.Add(d)
			return nil
		}
		_, data := g.run("op", tools.ToolDrain, drainArgs)
		Expect(waits).To(BeEmpty())
		Expect(evictions).To(Equal(1))
		Expect(strs(data["remaining"])).To(ConsistOf("team-a/web-1", "team-a/web-2"))
		Expect(data["note"]).To(ContainSubstring("throttl"))
	})
})

func remainingOf(s []string) []any {
	out := make([]any, len(s))
	for i, x := range s {
		out[i] = x
	}
	return out
}
