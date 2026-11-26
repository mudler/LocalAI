// SPDX-License-Identifier: MIT
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mudler/LocalAI/addons/kubernetes/internal/kubeapi"
	"github.com/mudler/LocalAI/addons/kubernetes/internal/policy"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
)

// A drain runs inside one apply call, which the host stops after 60
// seconds. So one call waits at most MaxDrainTimeout for the evicted pods to
// go; what is left is "pending", and a second call continues: the node is
// already cordoned, and the pods that are gone are not evicted again.
// Evictions go through the Eviction API, so PodDisruptionBudgets hold.

const (
	defaultDrainTimeout = 40
	drainPoll           = 2 * time.Second
	mirrorAnnotation    = "kubernetes.io/config.mirror"
	maxDrainPods        = 500
)

type drainOp struct {
	Cluster            string `json:"cluster"`
	Node               string `json:"node"`
	IgnoreDaemonSets   *bool  `json:"ignoreDaemonSets"`
	DeleteEmptyDirData bool   `json:"deleteEmptyDirData"`
	TimeoutSeconds     int    `json:"timeoutSeconds"`

	evict []podRef
}

type podRef struct {
	Namespace string
	Name      string
	UID       types.UID
}

func (p podRef) String() string { return p.Namespace + "/" + p.Name }

type podNote struct {
	Pod    string `json:"pod"`
	Reason string `json:"reason"`
}

func (c *call) parseDrain(raw map[string]json.RawMessage) (writeOp, error) {
	var o drainOp
	if err := decodeArgs(raw, &o); err != nil {
		return nil, err
	}
	if o.Node == "" || len(o.Node) > 253 {
		return nil, bad("name the node")
	}
	if o.TimeoutSeconds == 0 {
		o.TimeoutSeconds = defaultDrainTimeout
	}
	if o.TimeoutSeconds < 1 || o.TimeoutSeconds > MaxDrainTimeout {
		return nil, bad("timeoutSeconds is 1 to %d; call drain again to continue", MaxDrainTimeout)
	}
	return &o, nil
}

func (o *drainOp) ignoreDaemonSets() bool { return o.IgnoreDaemonSets == nil || *o.IgnoreDaemonSets }

func controller(p *corev1.Pod) *metav1.OwnerReference {
	for i := range p.OwnerReferences {
		if r := &p.OwnerReferences[i]; r.Controller != nil && *r.Controller {
			return r
		}
	}
	return nil
}

func usesEmptyDir(p *corev1.Pod) bool {
	for _, v := range p.Spec.Volumes {
		if v.EmptyDir != nil {
			return true
		}
	}
	return false
}

// sortPods sorts the pods of the node, for a stable revision and output.
func podsOn(c *call, node string) ([]corev1.Pod, error) {
	sel := fields.OneTermEqualSelector("spec.nodeName", node)
	l, err := c.c.Typed.CoreV1().Pods("").List(c.ctx, metav1.ListOptions{FieldSelector: sel.String()})
	if err != nil {
		return nil, err
	}
	var out []corev1.Pod
	for _, p := range l.Items {
		if p.Spec.NodeName == node {
			out = append(out, p)
		}
	}
	slices.SortFunc(out, func(a, b corev1.Pod) int { return strings.Compare(a.Namespace+"/"+a.Name, b.Namespace+"/"+b.Name) })
	return out, nil
}

// plan sorts the pods of the node into those to evict, those to leave and
// those that block the drain. Nothing is evicted while one blocks it.
func (o *drainOp) plan(c *call) (evict []podRef, skipped, blocked []podNote, err error) {
	pods, err := podsOn(c, o.Node)
	if err != nil {
		return nil, nil, nil, err
	}
	if len(pods) > maxDrainPods {
		return nil, nil, nil, bad("the node runs more than %d pods; drain it with another tool", maxDrainPods)
	}
	var outside []string
	for i := range pods {
		p := &pods[i]
		ref := podRef{Namespace: p.Namespace, Name: p.Name, UID: p.UID}
		ctrl := controller(p)
		switch {
		case p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed:
			skipped = append(skipped, podNote{ref.String(), "finished"})
		case p.Annotations[mirrorAnnotation] != "":
			skipped = append(skipped, podNote{ref.String(), "mirror pod"})
		case ctrl != nil && ctrl.Kind == "DaemonSet" && o.ignoreDaemonSets():
			skipped = append(skipped, podNote{ref.String(), "DaemonSet"})
		case !c.pol.NamespaceAllowed(p.Namespace):
			outside = append(outside, ref.String())
		case ctrl != nil && ctrl.Kind == "DaemonSet":
			blocked = append(blocked, podNote{ref.String(), "DaemonSet: set ignoreDaemonSets"})
		case ctrl == nil:
			blocked = append(blocked, podNote{ref.String(), "unmanaged: no controller would make it again"})
		case usesEmptyDir(p) && !o.DeleteEmptyDirData:
			blocked = append(blocked, podNote{ref.String(), "emptyDir: its data is lost; set deleteEmptyDirData"})
		default:
			evict = append(evict, ref)
		}
	}
	if len(outside) > 0 {
		return nil, nil, nil, policy.Refuse(policy.RuleNamespaces, "the node runs pods in namespaces outside the allowlist (%s), so it cannot be drained", strings.Join(first(outside, 10), ", "))
	}
	return evict, skipped, blocked, nil
}

func first(s []string, n int) []string {
	if len(s) > n {
		return append(s[:n:n], fmt.Sprintf("and %d more", len(s)-n))
	}
	return s
}

func refs(r []podRef) []string {
	out := make([]string, 0, len(r))
	for _, p := range r {
		out = append(out, p.String())
	}
	return out
}

func (o *drainOp) inspect(c *call) (map[string]any, uint64, error) {
	n, err := c.c.Typed.CoreV1().Nodes().Get(c.ctx, o.Node, metav1.GetOptions{})
	if err != nil {
		return nil, 0, err
	}
	evict, skipped, blocked, err := o.plan(c)
	if err != nil {
		return nil, 0, err
	}
	if len(blocked) > 0 {
		var lines []string
		for _, b := range blocked {
			lines = append(lines, b.Pod+" ("+b.Reason+")")
		}
		return nil, 0, bad("nothing was evicted: these pods block the drain: %s", strings.Join(first(lines, 10), "; "))
	}
	o.evict = evict
	parts := []string{o.Node, string(n.UID), fmt.Sprint(o.ignoreDaemonSets(), o.DeleteEmptyDirData, o.TimeoutSeconds)}
	for _, p := range evict {
		parts = append(parts, string(p.UID))
	}
	s := map[string]any{"tool": ToolDrain, "kind": "Node", "node": o.Node, "cordoned": n.Spec.Unschedulable,
		"pods": first(refs(evict), 50), "skipped": firstNotes(skipped, 50), "timeoutSeconds": o.TimeoutSeconds}
	return s, c.revision(ToolDrain, parts...), nil
}

func firstNotes(n []podNote, max int) []podNote {
	if n == nil {
		return []podNote{}
	}
	return n[:min(len(n), max)]
}

func (r *Runtime) sleep(ctx context.Context, d time.Duration) error {
	if r.Sleep != nil {
		return r.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (o *drainOp) apply(c *call) (map[string]any, error) {
	if err := setUnschedulable(c, o.Node, true); err != nil {
		return nil, err
	}
	var evicted []podRef
	blocked := []podNote{}
	for _, p := range o.evict {
		err := c.c.Typed.CoreV1().Pods(p.Namespace).EvictV1(c.ctx, &policyv1.Eviction{
			ObjectMeta:    metav1.ObjectMeta{Namespace: p.Namespace, Name: p.Name},
			DeleteOptions: &metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &p.UID}}})
		switch {
		case err == nil, apierrors.IsNotFound(err):
			evicted = append(evicted, p)
		case apierrors.IsTooManyRequests(err):
			blocked = append(blocked, podNote{p.String(), "PodDisruptionBudget: " + kubeapi.Short(err.Error())})
		case apierrors.IsForbidden(err) || apierrors.IsConflict(err) || apierrors.IsInvalid(err):
			blocked = append(blocked, podNote{p.String(), kubeapi.Short(err.Error())})
		default:
			return nil, err
		}
	}
	// Wait for the evicted pods to go, up to the timeout of this call.
	deadline := c.r.now().Add(time.Duration(o.TimeoutSeconds) * time.Second)
	pending := evicted
	for {
		var still []podRef
		for _, p := range pending {
			got, err := c.c.Typed.CoreV1().Pods(p.Namespace).Get(c.ctx, p.Name, metav1.GetOptions{})
			if err == nil && got.UID == p.UID {
				still = append(still, p)
			} else if err != nil && !apierrors.IsNotFound(err) {
				still = append(still, p)
			}
		}
		pending = still
		if len(pending) == 0 || !c.r.now().Before(deadline) {
			break
		}
		if err := c.r.sleep(c.ctx, drainPoll); err != nil {
			break
		}
	}
	return map[string]any{"node": o.Node, "cordoned": true, "evicted": refs(evicted), "blocked": blocked,
		"pending": refs(pending), "done": len(blocked) == 0 && len(pending) == 0}, nil
}
