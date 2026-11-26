// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"time"

	"github.com/mudler/LocalAI/addons/kubernetes/internal/policy"
	"github.com/mudler/LocalAI/addons/kubernetes/internal/tools"
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
})
