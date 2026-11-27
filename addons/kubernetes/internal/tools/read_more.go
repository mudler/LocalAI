// SPDX-License-Identifier: MIT
package tools

import (
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"sort"

	"github.com/mudler/LocalAI/addons/kubernetes/internal/kubeapi"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

// revisionAnnotation is where a Deployment's replica set keeps its revision.
const revisionAnnotation = "deployment.kubernetes.io/revision"

const (
	maxRelatedPods = 20
	// maxRelatedReplicaSets bounds the replica sets describe reads for one
	// Deployment.
	maxRelatedReplicaSets = 100
	maxNodePods           = 50
	maxObjectEvents       = 30
	defaultEventLimit     = 50
	maxEventLimit         = 200
	maxEventMessage       = 1 << 10
	defaultTailLines      = 200
	maxTailLines          = 5000
	maxSinceSeconds       = 7 * 24 * 3600
	maxLogRead            = 4 << 20
	maxTopRows            = 500
	maxConditionsShown    = 20
)

var (
	podsGVR        = schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	replicaSetsGVR = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "replicasets"}
)

func capText(s string, n int) string { o, _ := kubeapi.Cap(s, n); return o }

func conditions(u *unstructured.Unstructured) []map[string]any {
	conds, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	var out []map[string]any
	for _, c := range conds {
		m, _ := c.(map[string]any)
		if m == nil || len(out) >= maxConditionsShown {
			continue
		}
		out = append(out, map[string]any{"type": m["type"], "status": m["status"], "reason": m["reason"], "message": capText(fmt.Sprint(m["message"]), 300)})
	}
	return out
}

// ref names an object as Kind/namespace/name. The kind is given, because
// the items of a list may come without one.
func ref(kind string, u *unstructured.Unstructured) string {
	if u.GetNamespace() == "" {
		return kind + "/" + u.GetName()
	}
	return kind + "/" + u.GetNamespace() + "/" + u.GetName()
}

func ownedBy(u *unstructured.Unstructured, uid types.UID) bool {
	for _, o := range u.GetOwnerReferences() {
		if o.UID == uid {
			return true
		}
	}
	return false
}

func (c *call) describe(raw map[string]json.RawMessage) (any, error) {
	var a objectArgs
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	u, t, err := c.fetch(a)
	if err != nil {
		return nil, err
	}
	c.present(u, t)
	summary := map[string]any{"kind": t.Kind, "name": u.GetName(), "age": age(c.r.now(), u.GetCreationTimestamp()),
		"labels": u.GetLabels(), "status": status(u), "conditions": conditions(u)}
	if t.Namespaced {
		summary["namespace"] = u.GetNamespace()
	}
	var owners []string
	for _, o := range u.GetOwnerReferences() {
		owners = append(owners, o.Kind+"/"+o.Name)
	}
	if len(owners) > 0 {
		summary["owners"] = owners
	}
	related, err := c.related(u, t)
	if err != nil {
		return nil, err
	}
	events, err := c.objectEvents(u, maxObjectEvents)
	if err != nil {
		return nil, err
	}
	return map[string]any{"summary": summary, "related": related, "events": events}, nil
}

// related lists the objects that explain one object: the replica sets and
// pods of a workload, the node of a pod, the pods of a node.
func (c *call) related(u *unstructured.Unstructured, t kubeapi.Target) ([]string, error) {
	var out []string
	gr := t.GVR.GroupResource()
	switch gr {
	case schema.GroupResource{Resource: "pods"}:
		if n := str(u, "spec", "nodeName"); n != "" {
			out = append(out, "Node/"+n)
		}
		return out, nil
	case schema.GroupResource{Resource: "nodes"}:
		pods, err := c.podsWhere(fields.OneTermEqualSelector("spec.nodeName", u.GetName()), "", maxNodePods)
		if err != nil {
			return nil, err
		}
		for _, p := range pods {
			out = append(out, ref("Pod", &p))
		}
		return out, nil
	case schema.GroupResource{Group: "apps", Resource: "deployments"}:
		// The replica sets of a Deployment carry the labels of its selector.
		// Listing by them, with a cap, keeps a namespace with many replica
		// sets from making the read slow.
		opts := metav1.ListOptions{Limit: maxRelatedReplicaSets}
		if sel, ok, _ := unstructured.NestedStringMap(u.Object, "spec", "selector", "matchLabels"); ok && len(sel) > 0 {
			opts.LabelSelector = labels.SelectorFromSet(sel).String()
		}
		l, err := c.c.Dynamic.Resource(replicaSetsGVR).Namespace(u.GetNamespace()).List(c.ctx, opts)
		if err != nil {
			return nil, err
		}
		for i := range l.Items {
			if ownedBy(&l.Items[i], u.GetUID()) {
				out = append(out, ref("ReplicaSet", &l.Items[i])+" (revision "+l.Items[i].GetAnnotations()[revisionAnnotation]+")")
			}
		}
	case schema.GroupResource{Group: "apps", Resource: "statefulsets"}, schema.GroupResource{Group: "apps", Resource: "daemonsets"},
		schema.GroupResource{Group: "apps", Resource: "replicasets"}, schema.GroupResource{Group: "batch", Resource: "jobs"}:
	default:
		return out, nil
	}
	sel, ok, _ := unstructured.NestedStringMap(u.Object, "spec", "selector", "matchLabels")
	if !ok || len(sel) == 0 {
		return out, nil
	}
	l, err := c.c.Dynamic.Resource(podsGVR).Namespace(u.GetNamespace()).List(c.ctx, metav1.ListOptions{LabelSelector: labels.SelectorFromSet(sel).String(), Limit: maxRelatedPods})
	if err != nil {
		return nil, err
	}
	for i := range min(len(l.Items), maxRelatedPods) {
		out = append(out, ref("Pod", &l.Items[i]))
	}
	return out, nil
}

// podsWhere lists pods by a field selector in the namespaces the policy
// allows. The fake cluster ignores field selectors, so the node is checked
// again here.
func (c *call) podsWhere(sel fields.Selector, ns string, max int) ([]unstructured.Unstructured, error) {
	nss, err := c.namespaces(ns, ns == "")
	if err != nil {
		return nil, err
	}
	var out []unstructured.Unstructured
	for _, n := range nss {
		l, err := c.c.Dynamic.Resource(podsGVR).Namespace(n).List(c.ctx, metav1.ListOptions{FieldSelector: sel.String()})
		if err != nil {
			return nil, err
		}
		for _, p := range l.Items {
			if len(out) >= max {
				return out, nil
			}
			if !c.pol.NamespaceAllowed(p.GetNamespace()) || !sel.Matches(fields.Set{"spec.nodeName": str(&p, "spec", "nodeName")}) {
				continue
			}
			out = append(out, p)
		}
	}
	return out, nil
}

type eventRow struct {
	Namespace string `json:"namespace"`
	Type      string `json:"type"`
	Reason    string `json:"reason"`
	Object    string `json:"object"`
	Message   string `json:"message"`
	Count     int64  `json:"count"`
	Last      string `json:"last"`
	at        metav1.Time
}

func eventTime(e *corev1.Event) metav1.Time {
	switch {
	case !e.LastTimestamp.IsZero():
		return e.LastTimestamp
	case !e.EventTime.IsZero():
		return metav1.NewTime(e.EventTime.Time)
	case !e.FirstTimestamp.IsZero():
		return e.FirstTimestamp
	}
	return e.CreationTimestamp
}

// events lists the events of some namespaces, newest first. With uid, only
// those of one object.
func (c *call) eventsIn(nss []string, uid types.UID, limit int) ([]eventRow, bool, error) {
	var rows []eventRow
	opts := metav1.ListOptions{}
	if uid != "" {
		opts.FieldSelector = fields.OneTermEqualSelector("involvedObject.uid", string(uid)).String()
	}
	for _, ns := range nss {
		l, err := c.c.Typed.CoreV1().Events(ns).List(c.ctx, opts)
		if err != nil {
			return nil, false, err
		}
		for i := range l.Items {
			e := &l.Items[i]
			if (uid != "" && e.InvolvedObject.UID != uid) || !c.pol.NamespaceAllowed(e.Namespace) {
				continue
			}
			at := eventTime(e)
			rows = append(rows, eventRow{Namespace: e.Namespace, Type: e.Type, Reason: capText(e.Reason, 128),
				Object: capText(e.InvolvedObject.Kind+"/"+e.InvolvedObject.Name, 300), Message: capText(e.Message, maxEventMessage),
				Count: int64(e.Count), Last: age(c.r.now(), at) + " ago", at: at})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[j].at.Before(&rows[i].at) })
	cut := len(rows) > limit
	if cut {
		rows = rows[:limit]
	}
	return rows, cut, nil
}

func (c *call) objectEvents(u *unstructured.Unstructured, limit int) ([]eventRow, error) {
	nss := []string{u.GetNamespace()}
	if u.GetNamespace() == "" {
		var err error
		if nss, err = c.namespaces("", true); err != nil {
			return nil, err
		}
	}
	rows, _, err := c.eventsIn(nss, u.GetUID(), limit)
	if rows == nil {
		rows = []eventRow{}
	}
	return rows, err
}

type eventsArgs struct {
	Cluster   string `json:"cluster"`
	Namespace string `json:"namespace"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Limit     int    `json:"limit"`
}

func (c *call) events(raw map[string]json.RawMessage) (any, error) {
	var a eventsArgs
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.Limit == 0 {
		a.Limit = defaultEventLimit
	}
	if a.Limit < 1 || a.Limit > maxEventLimit {
		return nil, bad("limit is 1 to %d", maxEventLimit)
	}
	if a.Kind != "" || a.Name != "" {
		u, t, err := c.fetch(objectArgs{Kind: a.Kind, Name: a.Name, Namespace: a.Namespace})
		if err != nil {
			return nil, err
		}
		rows, err := c.objectEvents(u, a.Limit)
		if err != nil {
			return nil, err
		}
		return map[string]any{"object": ref(t.Kind, u), "events": rows, "count": len(rows)}, nil
	}
	nss, err := c.namespaces(a.Namespace, a.Namespace == "")
	if err != nil {
		return nil, err
	}
	rows, cut, err := c.eventsIn(nss, "", a.Limit)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []eventRow{}
	}
	return map[string]any{"events": rows, "count": len(rows), "cut": cut}, nil
}

type logsArgs struct {
	Cluster      string `json:"cluster"`
	Namespace    string `json:"namespace"`
	Pod          string `json:"pod"`
	Container    string `json:"container"`
	TailLines    int64  `json:"tailLines"`
	SinceSeconds int64  `json:"sinceSeconds"`
	Previous     bool   `json:"previous"`
}

func (c *call) logs(raw map[string]json.RawMessage) (any, error) {
	var a logsArgs
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.Namespace == "" || a.Pod == "" {
		return nil, bad("name the namespace and the pod")
	}
	if err := c.allow(a.Namespace); err != nil {
		return nil, err
	}
	if a.TailLines == 0 {
		a.TailLines = defaultTailLines
	}
	if a.TailLines < 1 || a.TailLines > maxTailLines {
		return nil, bad("tailLines is 1 to %d", maxTailLines)
	}
	if a.SinceSeconds < 0 || a.SinceSeconds > maxSinceSeconds {
		return nil, bad("sinceSeconds is 1 to %d", maxSinceSeconds)
	}
	opts := &corev1.PodLogOptions{Container: a.Container, TailLines: &a.TailLines, Previous: a.Previous}
	if a.SinceSeconds > 0 {
		opts.SinceSeconds = &a.SinceSeconds
	}
	rc, err := c.c.Logs.Logs(c.ctx, a.Namespace, a.Pod, opts)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	// The newest lines matter: read a bounded amount and keep its end.
	b, err := io.ReadAll(io.LimitReader(rc, maxLogRead+1))
	if err != nil {
		return nil, err
	}
	readCut := len(b) > maxLogRead
	text, cut := kubeapi.CapTail(string(b), kubeapi.MaxText)
	return map[string]any{"namespace": a.Namespace, "pod": a.Pod, "container": a.Container, "logs": text, "logsCut": cut || readCut}, nil
}

type topArgs struct {
	Cluster   string `json:"cluster"`
	Resource  string `json:"resource"`
	Namespace string `json:"namespace"`
}

func (c *call) top(raw map[string]json.RawMessage) (any, error) {
	var a topArgs
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	var rows []kubeapi.Usage
	switch a.Resource {
	case "nodes":
		u, err := c.c.Metrics.Nodes(c.ctx)
		if err != nil {
			return nil, err
		}
		rows = u
	case "pods":
		nss, err := c.namespaces(a.Namespace, a.Namespace == "")
		if err != nil {
			return nil, err
		}
		for _, ns := range nss {
			u, err := c.c.Metrics.Pods(c.ctx, ns)
			if err != nil {
				return nil, err
			}
			for _, p := range u {
				if c.pol.NamespaceAllowed(p.Namespace) {
					rows = append(rows, p)
				}
			}
		}
	default:
		return nil, bad("resource is nodes or pods")
	}
	slices.SortStableFunc(rows, func(x, y kubeapi.Usage) int { return cmp.Compare(y.CPUMilli, x.CPUMilli) })
	cut := len(rows) > maxTopRows
	if cut {
		rows = rows[:maxTopRows]
	}
	if rows == nil {
		rows = []kubeapi.Usage{}
	}
	return map[string]any{"resource": a.Resource, "rows": rows, "cut": cut}, nil
}
