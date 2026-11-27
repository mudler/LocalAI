// SPDX-License-Identifier: MIT
package tools_test

import (
	"context"
	"time"

	"github.com/mudler/LocalAI/addons/kubernetes/internal/policy"
	"github.com/mudler/LocalAI/addons/kubernetes/internal/tools"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8stesting "k8s.io/client-go/testing"
)

func (g *rig) deploy(ns, name string) *appsv1.Deployment {
	d, err := g.k.Typed.AppsV1().Deployments(ns).Get(context.Background(), name, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred())
	return d
}

var scaleArgs = map[string]any{"kind": "Deployment", "namespace": "team-a", "name": "web", "replicas": 3}

var _ = Describe("the guarded writes", func() {
	It("previews each write with a sample and changes nothing", func() {
		d := deployment("team-a", "web", 2)
		g := newRig(d, replicaSet(d, "web-1", "1", "example/app:1"), replicaSet(d, "web-2", "2", "example/app:2"), pod("team-a", "p", "node-1", time.Hour), node("node-1"))
		for tool, args := range map[string]map[string]any{
			tools.ToolScale:          scaleArgs,
			tools.ToolRolloutRestart: {"kind": "Deployment", "namespace": "team-a", "name": "web"},
			tools.ToolRolloutUndo:    {"namespace": "team-a", "name": "web"},
			tools.ToolDeletePod:      {"namespace": "team-a", "name": "p", "gracePeriodSeconds": 5},
			tools.ToolCordon:         {"node": "node-1"},
			tools.ToolUncordon:       {"node": "node-1"},
		} {
			p, err := g.preview("op-"+tool, tool, args)
			Expect(err).NotTo(HaveOccurred(), tool)
			s := sampleOf(p)
			Expect(s["tool"]).To(Equal(tool))
			Expect(s["cluster"]).To(Equal(key))
			Expect(p.Revision.Data).NotTo(BeZero())
		}
		Expect(g.k.Writes()).To(BeZero())
		s, _ := g.preview("op", tools.ToolScale, scaleArgs)
		Expect(sampleOf(s)).To(HaveKeyWithValue("from", BeNumerically("==", 2)))
		Expect(sampleOf(s)).To(HaveKeyWithValue("to", BeNumerically("==", 3)))
	})

	It("applies after the preview and returns a committed receipt", func() {
		g := newRig(deployment("team-a", "web", 2))
		rc, data := g.run("op-1", tools.ToolScale, scaleArgs)
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		Expect(data["replicas"]).To(BeNumerically("==", 3))
		Expect(*g.deploy("team-a", "web").Spec.Replicas).To(BeNumerically("==", 3))
	})

	It("returns the same receipt for a repeated apply of one operation", func() {
		g := newRig(deployment("team-a", "web", 2))
		p, _ := g.preview("op-1", tools.ToolScale, scaleArgs)
		rc1, err := g.apply("op-1", tools.ToolScale, scaleArgs, p)
		Expect(err).NotTo(HaveOccurred())
		writes := g.k.Writes()
		rc2, err := g.apply("op-1", tools.ToolScale, scaleArgs, p)
		Expect(err).NotTo(HaveOccurred())
		Expect(rc2).To(Equal(rc1))
		Expect(g.k.Writes()).To(Equal(writes))
	})

	It("rejects an apply whose object or policy changed after the preview", func() {
		g := newRig(deployment("team-a", "web", 2))
		p, _ := g.preview("op-1", tools.ToolScale, scaleArgs)
		d := g.deploy("team-a", "web")
		five := int32(5)
		d.Spec.Replicas = &five
		_, err := g.k.Typed.AppsV1().Deployments("team-a").Update(context.Background(), d, metav1.UpdateOptions{})
		Expect(err).NotTo(HaveOccurred())
		rc, err := g.apply("op-1", tools.ToolScale, scaleArgs, p)
		Expect(err).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptRejected))
		Expect(resultData(rc)).To(HaveKeyWithValue("stale", true))
		Expect(*g.deploy("team-a", "web").Spec.Replicas).To(BeNumerically("==", 5))

		p, _ = g.preview("op-2", tools.ToolScale, scaleArgs)
		g.setPolicy(policy.Policy{Namespaces: []string{"team-a", "team-b"}, WriteTools: writes})
		rc, err = g.apply("op-2", tools.ToolScale, scaleArgs, p)
		Expect(err).NotTo(HaveOccurred())
		Expect(rc.State).To(Equal(contract.ReceiptRejected))
		Expect(*g.deploy("team-a", "web").Spec.Replicas).To(BeNumerically("==", 5))
	})

	It("refuses a disabled tool in preview and in apply and names the rule", func() {
		g := newRig(deployment("team-a", "web", 2))
		p, err := g.preview("op-1", tools.ToolScale, scaleArgs)
		Expect(err).NotTo(HaveOccurred())
		g.setPolicy(policy.Policy{Namespaces: []string{"*"}, WriteTools: []string{"cordon"}})
		_, err = g.apply("op-1", tools.ToolScale, scaleArgs, p)
		Expect(err).To(MatchError(ContainSubstring("refused by the connection policy (writeTools): scale is disabled by the connection policy")))
		_, err = g.preview("op-2", tools.ToolScale, scaleArgs)
		Expect(err).To(MatchError(ContainSubstring("(writeTools)")))
		Expect(*g.deploy("team-a", "web").Spec.Replicas).To(BeNumerically("==", 2))
	})

	It("refuses a namespace outside the allowlist in preview and in apply", func() {
		g := newRig(deployment("team-b", "api", 2))
		args := map[string]any{"kind": "Deployment", "namespace": "team-b", "name": "api", "replicas": 1}
		p, err := g.preview("op-1", tools.ToolScale, args)
		Expect(err).NotTo(HaveOccurred())
		g.setPolicy(policy.Policy{Namespaces: []string{"team-a"}, WriteTools: writes})
		_, err = g.apply("op-1", tools.ToolScale, args, p)
		Expect(err).To(MatchError(ContainSubstring("(namespaces)")))
		_, err = g.preview("op-2", tools.ToolScale, args)
		Expect(err).To(MatchError(ContainSubstring("(namespaces)")))
		Expect(*g.deploy("team-b", "api").Spec.Replicas).To(BeNumerically("==", 2))
	})

	It("retries scale once on a conflict and no other write", func() {
		g := newRig(deployment("team-a", "web", 2))
		conflicts := 0
		g.k.Prepend("patch", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
			if conflicts == 0 {
				conflicts++
				return true, nil, apierrors.NewConflict(schema.GroupResource{Group: "apps", Resource: "deployments"}, "web", nil)
			}
			return false, nil, nil
		})
		rc, _ := g.run("op", tools.ToolScale, scaleArgs)
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
		Expect(conflicts).To(Equal(1))

		// rollout-restart sends a merge patch with no resourceVersion, so the
		// API server never answers it with a conflict; it is sent once.
		g = newRig(deployment("team-a", "web", 2))
		restarts := 0
		restart := map[string]any{"kind": "Deployment", "namespace": "team-a", "name": "web"}
		g.k.Prepend("patch", "deployments", func(a k8stesting.Action) (bool, runtime.Object, error) {
			restarts++
			Expect(string(a.(k8stesting.PatchAction).GetPatch())).NotTo(ContainSubstring("resourceVersion"))
			return true, nil, apierrors.NewConflict(schema.GroupResource{Group: "apps", Resource: "deployments"}, "web", nil)
		})
		p, err := g.preview("op", tools.ToolRolloutRestart, restart)
		Expect(err).NotTo(HaveOccurred())
		_, err = g.apply("op", tools.ToolRolloutRestart, restart, p)
		Expect(err).To(MatchError(ContainSubstring("409 Conflict")))
		Expect(restarts).To(Equal(1))

		g = newRig(pod("team-a", "p", "n", time.Hour))
		calls := 0
		g.k.Prepend("delete", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
			calls++
			return true, nil, apierrors.NewConflict(schema.GroupResource{Resource: "pods"}, "p", nil)
		})
		p, _ = g.preview("op", tools.ToolDeletePod, map[string]any{"namespace": "team-a", "name": "p"})
		_, err = g.apply("op", tools.ToolDeletePod, map[string]any{"namespace": "team-a", "name": "p"}, p)
		Expect(err).To(MatchError(ContainSubstring("409 Conflict")))
		Expect(calls).To(Equal(1))
	})

	It("rollout restart sets restartedAt on the pod template", func() {
		g := newRig(deployment("team-a", "web", 2))
		_, data := g.run("op", tools.ToolRolloutRestart, map[string]any{"kind": "Deployment", "namespace": "team-a", "name": "web"})
		Expect(data["restartedAt"]).To(Equal("2026-11-26T12:00:00Z"))
		Expect(g.deploy("team-a", "web").Spec.Template.Annotations).To(HaveKeyWithValue("kubectl.kubernetes.io/restartedAt", "2026-11-26T12:00:00Z"))
	})

	It("undo picks the previous revision or the named one and refuses when there is none", func() {
		d := deployment("team-a", "web", 2)
		d.Annotations = map[string]string{"deployment.kubernetes.io/revision": "3"}
		objs := []runtime.Object{d, replicaSet(d, "web-1", "1", "example/app:1"), replicaSet(d, "web-2", "2", "example/app:2"), replicaSet(d, "web-3", "3", "example/app:3")}
		g := newRig(objs...)
		p, err := g.preview("op-1", tools.ToolRolloutUndo, map[string]any{"namespace": "team-a", "name": "web"})
		Expect(err).NotTo(HaveOccurred())
		Expect(sampleOf(p)).To(HaveKeyWithValue("to", BeNumerically("==", 2)))
		Expect(sampleOf(p)).To(HaveKeyWithValue("from", BeNumerically("==", 3)))
		g.run("op-2", tools.ToolRolloutUndo, map[string]any{"namespace": "team-a", "name": "web", "toRevision": 1})
		tpl := g.deploy("team-a", "web").Spec.Template
		Expect(tpl.Spec.Containers[0].Image).To(Equal("example/app:1"))
		Expect(tpl.Labels).NotTo(HaveKey("pod-template-hash"))

		_, err = g.preview("op-3", tools.ToolRolloutUndo, map[string]any{"namespace": "team-a", "name": "web", "toRevision": 9})
		Expect(err).To(MatchError(ContainSubstring("no revision 9")))
		lone := deployment("team-a", "solo", 1)
		lone.UID = "solo"
		g = newRig(lone, replicaSet(lone, "solo-1", "1", "example/app:1"))
		_, err = g.preview("op-4", tools.ToolRolloutUndo, map[string]any{"namespace": "team-a", "name": "solo"})
		Expect(err).To(MatchError(ContainSubstring("no earlier revision")))
	})

	It("cordon and uncordon set unschedulable", func() {
		g := newRig(node("node-1"))
		g.run("op-1", tools.ToolCordon, map[string]any{"node": "node-1"})
		n, _ := g.k.Typed.CoreV1().Nodes().Get(context.Background(), "node-1", metav1.GetOptions{})
		Expect(n.Spec.Unschedulable).To(BeTrue())
		g.run("op-2", tools.ToolUncordon, map[string]any{"node": "node-1"})
		n, _ = g.k.Typed.CoreV1().Nodes().Get(context.Background(), "node-1", metav1.GetOptions{})
		Expect(n.Spec.Unschedulable).To(BeFalse())
	})

	It("cordons whatever the namespace allowlist says, since nodes have none", func() {
		g := newRig(node("node-1"))
		g.setPolicy(policy.Policy{Namespaces: []string{"team-a"}, WriteTools: writes})
		rc, _ := g.run("op-1", tools.ToolCordon, map[string]any{"node": "node-1"})
		Expect(rc.State).To(Equal(contract.ReceiptCommitted))
	})

	It("delete-pod passes the grace period", func() {
		g := newRig(pod("team-a", "p", "n", time.Hour))
		var seen *int64
		g.k.Typed.PrependReactor("delete", "pods", func(a k8stesting.Action) (bool, runtime.Object, error) {
			seen = a.(k8stesting.DeleteAction).GetDeleteOptions().GracePeriodSeconds
			return false, nil, nil
		})
		g.run("op", tools.ToolDeletePod, map[string]any{"namespace": "team-a", "name": "p", "gracePeriodSeconds": 7})
		Expect(seen).NotTo(BeNil())
		Expect(*seen).To(BeNumerically("==", 7))
		_, err := g.k.Typed.CoreV1().Pods("team-a").Get(context.Background(), "p", metav1.GetOptions{})
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})

	It("refuses kinds a tool does not take", func() {
		g := newRig()
		_, err := g.preview("op", tools.ToolScale, map[string]any{"kind": "DaemonSet", "namespace": "team-a", "name": "x", "replicas": 1})
		Expect(err).To(MatchError(ContainSubstring("kind is one of")))
	})
})
