// SPDX-License-Identifier: MIT
package tools

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/mudler/LocalAI/addons/kubernetes/internal/kubeapi"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

const (
	restartedAt  = "kubectl.kubernetes.io/restartedAt"
	fieldManager = kubeapi.FieldManager
)

var workloadGVR = map[string]schema.GroupVersionResource{
	"Deployment":  {Group: "apps", Version: "v1", Resource: "deployments"},
	"StatefulSet": {Group: "apps", Version: "v1", Resource: "statefulsets"},
	"ReplicaSet":  {Group: "apps", Version: "v1", Resource: "replicasets"},
	"DaemonSet":   {Group: "apps", Version: "v1", Resource: "daemonsets"},
}

// target is a namespaced object a write names.
type target struct {
	Cluster   string `json:"cluster"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

func (c *call) checkTarget(t target, kinds ...string) (schema.GroupVersionResource, error) {
	gvr, ok := workloadGVR[t.Kind]
	allowed := false
	for _, k := range kinds {
		allowed = allowed || k == t.Kind
	}
	if !ok || !allowed {
		return gvr, bad("kind is one of %v", kinds)
	}
	if t.Name == "" || len(t.Name) > 253 {
		return gvr, bad("name the object")
	}
	return gvr, c.allow(t.Namespace)
}

func (c *call) getObject(gvr schema.GroupVersionResource, ns, name string) (*unstructured.Unstructured, error) {
	return c.c.Dynamic.Resource(gvr).Namespace(ns).Get(c.ctx, name, metav1.GetOptions{})
}

// scale ---------------------------------------------------------------------

type scaleOp struct {
	target
	Replicas *int64 `json:"replicas"`
	gvr      schema.GroupVersionResource
}

func (c *call) parseScale(raw map[string]json.RawMessage) (writeOp, error) {
	var o scaleOp
	if err := decodeArgs(raw, &o); err != nil {
		return nil, err
	}
	if o.Replicas == nil || *o.Replicas < 0 || *o.Replicas > 1000 {
		return nil, bad("replicas is 0 to 1000")
	}
	gvr, err := c.checkTarget(o.target, "Deployment", "StatefulSet", "ReplicaSet")
	o.gvr = gvr
	return &o, err
}

func (o *scaleOp) inspect(c *call) (map[string]any, uint64, error) {
	u, err := c.getObject(o.gvr, o.Namespace, o.Name)
	if err != nil {
		return nil, 0, err
	}
	from := num(u, "spec", "replicas")
	s := map[string]any{"tool": ToolScale, "kind": o.Kind, "namespace": o.Namespace, "name": o.Name, "from": from, "to": *o.Replicas}
	return s, c.revision(ToolScale, o.Kind, o.Namespace, o.Name, itoa(*o.Replicas), itoa(from), itoa(u.GetGeneration()), string(u.GetUID())), nil
}

func (o *scaleOp) apply(c *call) (map[string]any, error) {
	err := retryConflict(func() error {
		u, err := c.getObject(o.gvr, o.Namespace, o.Name)
		if err != nil {
			return err
		}
		// The resourceVersion makes the write fail with 409 when another
		// writer came first, instead of overwriting blindly.
		body, _ := json.Marshal(map[string]any{"metadata": map[string]any{"resourceVersion": u.GetResourceVersion()}, "spec": map[string]any{"replicas": *o.Replicas}})
		_, err = c.c.Dynamic.Resource(o.gvr).Namespace(o.Namespace).Patch(c.ctx, o.Name, types.MergePatchType, body, metav1.PatchOptions{FieldManager: fieldManager})
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"kind": o.Kind, "namespace": o.Namespace, "name": o.Name, "replicas": *o.Replicas}, nil
}

// rollout-restart ------------------------------------------------------------

type restartOp struct {
	target
	gvr schema.GroupVersionResource
}

func (c *call) parseRestart(raw map[string]json.RawMessage) (writeOp, error) {
	var o restartOp
	if err := decodeArgs(raw, &o); err != nil {
		return nil, err
	}
	gvr, err := c.checkTarget(o.target, "Deployment", "StatefulSet", "DaemonSet")
	o.gvr = gvr
	return &o, err
}

func (o *restartOp) inspect(c *call) (map[string]any, uint64, error) {
	u, err := c.getObject(o.gvr, o.Namespace, o.Name)
	if err != nil {
		return nil, 0, err
	}
	s := map[string]any{"tool": ToolRolloutRestart, "kind": o.Kind, "namespace": o.Namespace, "name": o.Name}
	return s, c.revision(ToolRolloutRestart, o.Kind, o.Namespace, o.Name, itoa(u.GetGeneration()), string(u.GetUID())), nil
}

func (o *restartOp) apply(c *call) (map[string]any, error) {
	at := c.r.now().UTC().Format(time.RFC3339)
	// A merge patch of one annotation, with no resourceVersion: the API
	// server applies it to the current object and never answers 409, so
	// there is nothing to retry.
	body, _ := json.Marshal(map[string]any{"spec": map[string]any{"template": map[string]any{"metadata": map[string]any{"annotations": map[string]any{restartedAt: at}}}}})
	_, err := c.c.Dynamic.Resource(o.gvr).Namespace(o.Namespace).Patch(c.ctx, o.Name, types.MergePatchType, body, metav1.PatchOptions{FieldManager: fieldManager})
	if err != nil {
		return nil, err
	}
	return map[string]any{"kind": o.Kind, "namespace": o.Namespace, "name": o.Name, "restartedAt": at}, nil
}

// rollout-undo ---------------------------------------------------------------

type undoOp struct {
	Cluster    string `json:"cluster"`
	Namespace  string `json:"namespace"`
	Name       string `json:"name"`
	ToRevision int64  `json:"toRevision"`
	// filled by inspect
	template map[string]any
}

func (c *call) parseUndo(raw map[string]json.RawMessage) (writeOp, error) {
	var o undoOp
	if err := decodeArgs(raw, &o); err != nil {
		return nil, err
	}
	if o.ToRevision < 0 {
		return nil, bad("toRevision is 1 or more")
	}
	_, err := c.checkTarget(target{Kind: "Deployment", Namespace: o.Namespace, Name: o.Name}, "Deployment")
	return &o, err
}

type revisionRS struct {
	rev  int64
	u    unstructured.Unstructured
	uid  string
	tmpl map[string]any
}

func (o *undoOp) inspect(c *call) (map[string]any, uint64, error) {
	d, err := c.getObject(workloadGVR["Deployment"], o.Namespace, o.Name)
	if err != nil {
		return nil, 0, err
	}
	l, err := c.c.Dynamic.Resource(replicaSetsGVR).Namespace(o.Namespace).List(c.ctx, metav1.ListOptions{})
	if err != nil {
		return nil, 0, err
	}
	var sets []revisionRS
	for _, rs := range l.Items {
		if !ownedBy(&rs, d.GetUID()) {
			continue
		}
		n, err := strconv.ParseInt(rs.GetAnnotations()[revisionAnnotation], 10, 64)
		if err != nil {
			continue
		}
		tmpl, _, _ := unstructured.NestedMap(rs.Object, "spec", "template")
		sets = append(sets, revisionRS{rev: n, u: rs, uid: string(rs.GetUID()), tmpl: tmpl})
	}
	sort.Slice(sets, func(i, j int) bool { return sets[i].rev > sets[j].rev })
	current := int64(0)
	if n, err := strconv.ParseInt(d.GetAnnotations()[revisionAnnotation], 10, 64); err == nil {
		current = n
	} else if len(sets) > 0 {
		current = sets[0].rev
	}
	var pick *revisionRS
	for i := range sets {
		if (o.ToRevision > 0 && sets[i].rev == o.ToRevision) || (o.ToRevision == 0 && sets[i].rev < current) {
			pick = &sets[i]
			break
		}
	}
	switch {
	case o.ToRevision > 0 && o.ToRevision == current:
		return nil, 0, bad("revision %d is the current one", current)
	case pick == nil && o.ToRevision > 0:
		return nil, 0, bad("the Deployment has no revision %d", o.ToRevision)
	case pick == nil:
		return nil, 0, bad("no earlier revision: the Deployment has only revision %d", current)
	}
	tmpl, ok := pick.tmpl, pick.tmpl != nil
	if !ok {
		return nil, 0, bad("revision %d has no pod template", pick.rev)
	}
	tmpl = runtimeCopy(tmpl)
	unstructured.RemoveNestedField(tmpl, "metadata", "labels", "pod-template-hash")
	o.template = tmpl
	s := map[string]any{"tool": ToolRolloutUndo, "kind": "Deployment", "namespace": o.Namespace, "name": o.Name, "from": current, "to": pick.rev}
	return s, c.revision(ToolRolloutUndo, o.Namespace, o.Name, itoa(current), itoa(pick.rev), pick.uid, itoa(d.GetGeneration()), string(d.GetUID())), nil
}

func runtimeCopy(m map[string]any) map[string]any {
	return (&unstructured.Unstructured{Object: m}).DeepCopy().Object
}

func (o *undoOp) apply(c *call) (map[string]any, error) {
	body, _ := json.Marshal([]map[string]any{{"op": "replace", "path": "/spec/template", "value": o.template}})
	if _, err := c.c.Dynamic.Resource(workloadGVR["Deployment"]).Namespace(o.Namespace).Patch(c.ctx, o.Name, types.JSONPatchType, body, metav1.PatchOptions{FieldManager: fieldManager}); err != nil {
		return nil, err
	}
	return map[string]any{"kind": "Deployment", "namespace": o.Namespace, "name": o.Name, "note": "rolled back; the Deployment controller makes the new revision"}, nil
}

// delete-pod -----------------------------------------------------------------

type deletePodOp struct {
	Cluster            string `json:"cluster"`
	Namespace          string `json:"namespace"`
	Name               string `json:"name"`
	GracePeriodSeconds *int64 `json:"gracePeriodSeconds"`
	uid                types.UID
}

func (c *call) parseDeletePod(raw map[string]json.RawMessage) (writeOp, error) {
	var o deletePodOp
	if err := decodeArgs(raw, &o); err != nil {
		return nil, err
	}
	if o.GracePeriodSeconds != nil && (*o.GracePeriodSeconds < 0 || *o.GracePeriodSeconds > 3600) {
		return nil, bad("gracePeriodSeconds is 0 to 3600")
	}
	if o.Name == "" {
		return nil, bad("name the pod")
	}
	return &o, c.allow(o.Namespace)
}

func (o *deletePodOp) inspect(c *call) (map[string]any, uint64, error) {
	p, err := c.c.Typed.CoreV1().Pods(o.Namespace).Get(c.ctx, o.Name, metav1.GetOptions{})
	if err != nil {
		return nil, 0, err
	}
	o.uid = p.UID
	s := map[string]any{"tool": ToolDeletePod, "kind": "Pod", "namespace": o.Namespace, "name": o.Name, "node": p.Spec.NodeName}
	grace := "default"
	if o.GracePeriodSeconds != nil {
		s["gracePeriodSeconds"] = *o.GracePeriodSeconds
		grace = itoa(*o.GracePeriodSeconds)
	}
	return s, c.revision(ToolDeletePod, o.Namespace, o.Name, grace, string(p.UID)), nil
}

func (o *deletePodOp) apply(c *call) (map[string]any, error) {
	// The UID precondition makes sure a pod with the same name that came
	// after the preview is not the one deleted.
	err := c.c.Typed.CoreV1().Pods(o.Namespace).Delete(c.ctx, o.Name, metav1.DeleteOptions{GracePeriodSeconds: o.GracePeriodSeconds, Preconditions: &metav1.Preconditions{UID: &o.uid}})
	if err != nil {
		return nil, err
	}
	return map[string]any{"kind": "Pod", "namespace": o.Namespace, "name": o.Name, "deleted": true}, nil
}

// cordon and uncordon --------------------------------------------------------

type cordonOp struct {
	Cluster string `json:"cluster"`
	Node    string `json:"node"`
	tool    string
}

func (c *call) parseCordon(tool string, raw map[string]json.RawMessage) (writeOp, error) {
	o := cordonOp{tool: tool}
	if err := decodeArgs(raw, &o); err != nil {
		return nil, err
	}
	if o.Node == "" || len(o.Node) > 253 {
		return nil, bad("name the node")
	}
	return &o, nil
}

func (o *cordonOp) inspect(c *call) (map[string]any, uint64, error) {
	n, err := c.c.Typed.CoreV1().Nodes().Get(c.ctx, o.Node, metav1.GetOptions{})
	if err != nil {
		return nil, 0, err
	}
	s := map[string]any{"tool": o.tool, "kind": "Node", "node": o.Node, "from": fmt.Sprintf("unschedulable=%t", n.Spec.Unschedulable),
		"to": fmt.Sprintf("unschedulable=%t", o.tool == ToolCordon)}
	return s, c.revision(o.tool, o.Node, strconv.FormatBool(n.Spec.Unschedulable), string(n.UID)), nil
}

func (o *cordonOp) apply(c *call) (map[string]any, error) {
	if err := setUnschedulable(c, o.Node, o.tool == ToolCordon); err != nil {
		return nil, err
	}
	return map[string]any{"kind": "Node", "node": o.Node, "unschedulable": o.tool == ToolCordon}, nil
}

func setUnschedulable(c *call, node string, on bool) error {
	body, _ := json.Marshal(map[string]any{"spec": map[string]any{"unschedulable": on}})
	_, err := c.c.Typed.CoreV1().Nodes().Patch(c.ctx, node, types.MergePatchType, body, metav1.PatchOptions{FieldManager: fieldManager})
	return err
}
