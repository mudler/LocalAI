// SPDX-License-Identifier: MIT
package tools

import (
	"context"
	"encoding/json"
	"errors"
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
// seconds. The whole call, checks included, keeps to the budget of an apply
// (applyBudget, counted from the start of Apply): one call evicts at most
// MaxEvictions pods and stops evicting when little time is left, and it
// waits for the evicted pods to go at most timeoutSeconds and never past the
// budget. Pods it did not evict are "remaining" and pods that are still
// going are "pending"; a second call continues. The node is already
// cordoned then, and the pods that are gone are not evicted again.
// Evictions go through the Eviction API, so PodDisruptionBudgets hold.
//
// Once the cordon is written, the call has changed the cluster: a later
// error ends the call with a partial result, never with an error that says
// nothing changed.

const (
	defaultDrainTimeout = 40
	drainPoll           = 2 * time.Second
	mirrorAnnotation    = "kubernetes.io/config.mirror"
	maxDrainPods        = 500
	// MaxEvictions bounds the evictions of one call.
	MaxEvictions = 50
	// evictReserve is the time an eviction may take: no eviction starts
	// later than this before the end of the budget.
	evictReserve = 5 * time.Second
	// throttleWait is the wait after a throttled eviction when the API
	// server suggests none, and maxThrottleWait the longest one: a longer
	// Retry-After is cut, since the call must end within its budget.
	throttleWait    = time.Second
	maxThrottleWait = 2 * time.Second
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

// throttled reports a 429 of API Priority and Fairness, as opposed to the 429
// of an eviction that a PodDisruptionBudget refuses.
func throttled(err error) bool {
	if !apierrors.IsTooManyRequests(err) {
		return false
	}
	var st apierrors.APIStatus
	if errors.As(err, &st) {
		s := st.Status()
		if s.Details != nil {
			for _, cause := range s.Details.Causes {
				if cause.Type == policyv1.DisruptionBudgetCause {
					return false
				}
			}
		}
		if strings.Contains(strings.ToLower(s.Message), "disruption budget") {
			return false
		}
	}
	return true
}

// backoff is the wait after a throttled eviction: the Retry-After of the API
// server, at least throttleWait and at most maxThrottleWait.
func backoff(err error) time.Duration {
	d := throttleWait
	if s, ok := apierrors.SuggestsClientDelay(err); ok && time.Duration(s)*time.Second > d {
		d = time.Duration(s) * time.Second
	}
	return min(d, maxThrottleWait)
}

func (o *drainOp) apply(c *call) (map[string]any, error) {
	if err := setUnschedulable(c, o.Node, true); err != nil {
		return nil, err
	}
	var evicted, remaining []podRef
	blocked := []podNote{}
	var failure error
	slow := false
	stop := false
	for i, p := range o.evict {
		if stop || i >= MaxEvictions || !c.r.now().Add(evictReserve).Before(c.deadline) {
			remaining = append(remaining, o.evict[i:]...)
			break
		}
		err := c.c.Typed.CoreV1().Pods(p.Namespace).EvictV1(c.ctx, &policyv1.Eviction{
			ObjectMeta:    metav1.ObjectMeta{Namespace: p.Namespace, Name: p.Name},
			DeleteOptions: &metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &p.UID}}})
		switch {
		case err == nil, apierrors.IsNotFound(err):
			evicted = append(evicted, p)
		case throttled(err):
			// Back off briefly before the next eviction, so the drain does
			// not add to the load the API server is shedding. A wait that
			// would leave less than the reserve ends the evictions instead.
			slow = true
			remaining = append(remaining, p)
			if wait := backoff(err); i+1 < len(o.evict) {
				stop = !c.r.now().Add(wait+evictReserve).Before(c.deadline) || c.r.sleep(c.ctx, wait) != nil
			}
		case apierrors.IsTooManyRequests(err):
			blocked = append(blocked, podNote{p.String(), "PodDisruptionBudget: " + kubeapi.Framed(err, kubeapi.ShortMessage)})
		case apierrors.IsForbidden(err) || apierrors.IsConflict(err) || apierrors.IsInvalid(err):
			blocked = append(blocked, podNote{p.String(), kubeapi.Framed(err, kubeapi.ShortMessage)})
		default:
			// The node is cordoned: stop here and say what was done.
			failure = err
			remaining = append(remaining, o.evict[i:]...)
		}
		if failure != nil {
			break
		}
	}
	// Wait for the evicted pods to go, up to the timeout of this call and
	// never past the budget of the apply.
	deadline := c.r.now().Add(time.Duration(o.TimeoutSeconds) * time.Second)
	if c.deadline.Before(deadline) {
		deadline = c.deadline
	}
	pending := evicted
	for failure == nil {
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
	done := failure == nil && len(blocked) == 0 && len(pending) == 0 && len(remaining) == 0
	out := map[string]any{"node": o.Node, "cordoned": true, "evicted": refs(evicted), "blocked": blocked,
		"pending": refs(pending), "remaining": refs(remaining), "done": done}
	var notes []string
	if failure != nil {
		out["error"] = kubeapi.Framed(failure, kubeapi.ShortMessage)
		notes = append(notes, "The node is cordoned and the drain stopped on an error.")
	}
	if slow {
		notes = append(notes, "The API server is throttling requests; wait a little.")
	}
	if len(remaining) > 0 || len(pending) > 0 {
		notes = append(notes, "Some pods are not gone yet: call drain again to continue.")
	}
	if len(notes) > 0 {
		out["note"] = strings.Join(notes, " ")
	}
	return out, nil
}
