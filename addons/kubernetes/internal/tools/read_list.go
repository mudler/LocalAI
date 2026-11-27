// SPDX-License-Identifier: MIT
package tools

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/mudler/LocalAI/addons/kubernetes/internal/kubeapi"
	"github.com/mudler/LocalAI/addons/kubernetes/internal/mask"
	"github.com/mudler/LocalAI/addons/kubernetes/internal/policy"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/yaml"
)

const (
	lastApplied      = "kubectl.kubernetes.io/last-applied-configuration"
	defaultListLimit = 100
	maxListLimit     = 500
	// rowBudget keeps a list under MaxPayload with room for the wrapper.
	rowBudget = MaxPayload - 16<<10
)

// clean removes what only adds noise or repeats the object: the managed
// fields and the copy kubectl keeps in an annotation.
func clean(u *unstructured.Unstructured) {
	unstructured.RemoveNestedField(u.Object, "metadata", "managedFields")
	if a := u.GetAnnotations(); a != nil {
		if _, ok := a[lastApplied]; ok {
			delete(a, lastApplied)
			if len(a) == 0 {
				unstructured.RemoveNestedField(u.Object, "metadata", "annotations")
			} else {
				u.SetAnnotations(a)
			}
		}
	}
}

// present makes an object ready to show: cleaned, and masked as the policy
// says. Every object passes here before it becomes text.
func (c *call) present(u *unstructured.Unstructured, t kubeapi.Target) {
	clean(u)
	mask.Apply(u, t, c.pol)
}

// toYAML writes an object as YAML, capped.
func toYAML(u *unstructured.Unstructured) (string, bool) {
	b, err := yaml.Marshal(u.Object)
	if err != nil {
		return "", false
	}
	return kubeapi.Cap(string(b), kubeapi.MaxText)
}

// resolve reads the kind of a call.
func (c *call) resolve(kind string) (kubeapi.Target, error) {
	t, err := kubeapi.Resolve(c.c.Mapper, kind)
	if err != nil {
		return t, bad("%s", err.Error())
	}
	return t, nil
}

// namespaces returns the namespaces a namespaced read may touch: the one
// named, when it is allowed; with all or none named, every namespace ("")
// when the policy allows all, else the allowlist.
func (c *call) namespaces(arg string, all bool) ([]string, error) {
	if arg != "" && !all {
		if err := c.allow(arg); err != nil {
			return nil, err
		}
		return []string{arg}, nil
	}
	if c.pol.AllNamespaces() {
		return []string{""}, nil
	}
	if len(c.pol.Namespaces) == 0 {
		return nil, noNamespace()
	}
	return slices.Clone(c.pol.Namespaces), nil
}

// noNamespace refuses a call while the policy allows no namespace, as the
// policy of a damaged policy file does.
func noNamespace() error {
	return policy.Refuse(policy.RuleNamespaces, "the policy of this cluster allows no namespace; if the policy file is damaged, an administrator must save the policy again")
}

// allow checks one namespace against the policy.
func (c *call) allow(ns string) error {
	if !policy.ValidNamespace(ns) {
		return bad("%q is not a namespace name", ns)
	}
	if len(c.pol.Namespaces) == 0 {
		return noNamespace()
	}
	if !c.pol.NamespaceAllowed(ns) {
		return policy.Refuse(policy.RuleNamespaces, "the namespace %s is not on the allowlist of this cluster", ns)
	}
	return nil
}

// objectNamespace is the namespace of a call on one object: the argument, or
// default, checked against the policy. A cluster-scoped kind has none.
func (c *call) objectNamespace(t kubeapi.Target, arg string) (string, error) {
	if !t.Namespaced {
		return "", nil
	}
	if arg == "" {
		arg = "default"
	}
	return arg, c.allow(arg)
}

type listArgs struct {
	Cluster       string `json:"cluster"`
	Kind          string `json:"kind"`
	Namespace     string `json:"namespace"`
	AllNamespaces bool   `json:"allNamespaces"`
	LabelSelector string `json:"labelSelector"`
	FieldSelector string `json:"fieldSelector"`
	Limit         int    `json:"limit"`
}

func (c *call) list(raw map[string]json.RawMessage) (any, error) {
	var a listArgs
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.Limit == 0 {
		a.Limit = defaultListLimit
	}
	if a.Limit < 1 || a.Limit > maxListLimit {
		return nil, bad("limit is 1 to %d", maxListLimit)
	}
	t, err := c.resolve(a.Kind)
	if err != nil {
		return nil, err
	}
	if t.GVR.Resource == "events" {
		return nil, bad("use the events tool to read events")
	}
	nss := []string{""}
	if t.Namespaced {
		if nss, err = c.namespaces(a.Namespace, a.AllNamespaces); err != nil {
			return nil, err
		}
	}
	var items []unstructured.Unstructured
	var notRead []string
	cut := false
	for i, ns := range nss {
		if i > 0 && !c.r.now().Before(c.deadline) {
			notRead = nss[i:]
			break
		}
		l, err := c.c.Dynamic.Resource(t.GVR).Namespace(ns).List(c.ctx, metav1.ListOptions{
			LabelSelector: a.LabelSelector, FieldSelector: a.FieldSelector, Limit: int64(a.Limit)})
		if err != nil && i > 0 && c.ctx.Err() != nil {
			// The budget ran out during this namespace: return what was read.
			notRead = nss[i:]
			break
		}
		if err != nil {
			return nil, err
		}
		if l.GetContinue() != "" {
			cut = true
		}
		for _, it := range l.Items {
			// The selectors cannot widen the allowlist: every row is checked.
			if t.Namespaced && !c.pol.NamespaceAllowed(it.GetNamespace()) {
				continue
			}
			items = append(items, it)
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		ti, tj := items[i].GetCreationTimestamp(), items[j].GetCreationTimestamp()
		if !ti.Equal(&tj) {
			return tj.Before(&ti)
		}
		return items[i].GetNamespace()+"/"+items[i].GetName() < items[j].GetNamespace()+"/"+items[j].GetName()
	})
	if len(items) > a.Limit {
		items, cut = items[:a.Limit], true
	}
	rows := make([]map[string]any, 0, len(items))
	size := 0
	for i := range items {
		row := c.row(&items[i], t)
		size += len(plainJSON(row)) + 1
		if size > rowBudget {
			cut = true
			break
		}
		rows = append(rows, row)
	}
	out := map[string]any{"kind": t.Kind, "rows": rows, "count": len(rows), "cut": cut}
	if len(notRead) > 0 {
		out["partial"] = true
		out["notRead"] = first(notRead, 20)
		out["note"] = "The read ran out of time before it read every namespace. List the namespaces that were not read one at a time."
	}
	return out, nil
}

// age writes a duration the way a person reads it: 45s, 12m, 5h3m, 3d4h.
func age(now time.Time, t metav1.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t.Time)
	if d < 0 {
		d = 0
	}
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
	case d >= time.Hour:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	case d >= time.Minute:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}

func str(u *unstructured.Unstructured, path ...string) string {
	s, _, _ := unstructured.NestedString(u.Object, path...)
	return s
}

func num(u *unstructured.Unstructured, path ...string) int64 {
	if n, ok, _ := unstructured.NestedInt64(u.Object, path...); ok {
		return n
	}
	if f, ok, _ := unstructured.NestedFloat64(u.Object, path...); ok {
		return int64(f)
	}
	return 0
}

// status is the phase, else the Ready condition, else empty.
func status(u *unstructured.Unstructured) string {
	if p := str(u, "status", "phase"); p != "" {
		return p
	}
	conds, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	for _, c := range conds {
		m, _ := c.(map[string]any)
		if m["type"] == "Ready" {
			if m["status"] == "True" {
				return "Ready"
			}
			return "NotReady"
		}
	}
	return ""
}

func (c *call) row(u *unstructured.Unstructured, t kubeapi.Target) map[string]any {
	short := func(s string) string { o, _ := kubeapi.Cap(s, 253); return o }
	r := map[string]any{"name": short(u.GetName()), "status": short(status(u)), "age": age(c.r.now(), u.GetCreationTimestamp())}
	if t.Namespaced {
		r["namespace"] = u.GetNamespace()
	}
	switch t.GVR.GroupResource() {
	case schema.GroupResource{Resource: "pods"}:
		ready, total, restarts := 0, 0, int64(0)
		cs, _, _ := unstructured.NestedSlice(u.Object, "status", "containerStatuses")
		for _, x := range cs {
			m, _ := x.(map[string]any)
			total++
			if m["ready"] == true {
				ready++
			}
			switch n := m["restartCount"].(type) {
			case int64:
				restarts += n
			case float64:
				restarts += int64(n)
			}
		}
		if total == 0 {
			sp, _, _ := unstructured.NestedSlice(u.Object, "spec", "containers")
			total = len(sp)
		}
		r["ready"] = fmt.Sprintf("%d/%d", ready, total)
		r["restarts"] = restarts
		r["node"] = short(str(u, "spec", "nodeName"))
	case schema.GroupResource{Group: "apps", Resource: "deployments"}, schema.GroupResource{Group: "apps", Resource: "statefulsets"}, schema.GroupResource{Group: "apps", Resource: "replicasets"}:
		r["ready"] = fmt.Sprintf("%d/%d", num(u, "status", "readyReplicas"), num(u, "spec", "replicas"))
		r["upToDate"] = num(u, "status", "updatedReplicas")
		r["available"] = num(u, "status", "availableReplicas")
	case schema.GroupResource{Group: "apps", Resource: "daemonsets"}:
		r["desired"] = num(u, "status", "desiredNumberScheduled")
		r["ready"] = num(u, "status", "numberReady")
	case schema.GroupResource{Resource: "nodes"}:
		var roles []string
		for l := range u.GetLabels() {
			if role, ok := strings.CutPrefix(l, "node-role.kubernetes.io/"); ok && role != "" {
				roles = append(roles, role)
			}
		}
		sort.Strings(roles)
		r["roles"] = short(strings.Join(roles, ","))
		r["version"] = short(str(u, "status", "nodeInfo", "kubeletVersion"))
		unsched, _, _ := unstructured.NestedBool(u.Object, "spec", "unschedulable")
		r["unschedulable"] = unsched
	case schema.GroupResource{Resource: "services"}:
		r["type"] = short(str(u, "spec", "type"))
		r["clusterIP"] = short(str(u, "spec", "clusterIP"))
		ports, _, _ := unstructured.NestedSlice(u.Object, "spec", "ports")
		var ps []string
		for _, p := range ports {
			m, _ := p.(map[string]any)
			ps = append(ps, fmt.Sprintf("%v/%v", m["port"], m["protocol"]))
		}
		r["ports"] = short(strings.Join(ps, ","))
	case schema.GroupResource{Group: "batch", Resource: "jobs"}:
		r["completions"] = fmt.Sprintf("%d/%d", num(u, "status", "succeeded"), num(u, "spec", "completions"))
	case schema.GroupResource{Resource: "secrets"}:
		data, _, _ := unstructured.NestedMap(u.Object, "data")
		r["type"] = short(str(u, "type"))
		r["keys"] = len(data)
	}
	return r
}

type objectArgs struct {
	Cluster   string `json:"cluster"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

// fetch reads one object the policy allows.
func (c *call) fetch(a objectArgs) (*unstructured.Unstructured, kubeapi.Target, error) {
	t, err := c.resolve(a.Kind)
	if err != nil {
		return nil, t, err
	}
	if a.Name == "" || len(a.Name) > 253 {
		return nil, t, bad("name the object")
	}
	ns, err := c.objectNamespace(t, a.Namespace)
	if err != nil {
		return nil, t, err
	}
	u, err := c.c.Dynamic.Resource(t.GVR).Namespace(ns).Get(c.ctx, a.Name, metav1.GetOptions{})
	return u, t, err
}

func (c *call) get(raw map[string]json.RawMessage) (any, error) {
	var a objectArgs
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	u, t, err := c.fetch(a)
	if err != nil {
		return nil, err
	}
	c.present(u, t)
	text, cut := toYAML(u)
	return map[string]any{"kind": t.Kind, "namespace": u.GetNamespace(), "name": u.GetName(), "yaml": text, "yamlCut": cut}, nil
}

type apiResourcesArgs struct {
	Cluster string `json:"cluster"`
	Group   string `json:"group"`
}

func (c *call) apiResources(raw map[string]json.RawMessage) (any, error) {
	var a apiResourcesArgs
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	group := strings.TrimSpace(a.Group)
	if group == "core" {
		group = ""
	}
	_, lists, err := c.c.Discovery.ServerGroupsAndResources()
	if err != nil && len(lists) == 0 {
		return nil, err
	}
	if err != nil {
		c.warnings = append(c.warnings, "some API groups did not answer, so the list may miss kinds")
	}
	seen := map[string]bool{}
	var rows []map[string]any
	for _, l := range lists {
		gv, perr := schema.ParseGroupVersion(l.GroupVersion)
		if perr != nil || (a.Group != "" && gv.Group != group) {
			continue
		}
		for _, r := range l.APIResources {
			if strings.Contains(r.Name, "/") || seen[gv.Group+"/"+r.Name] {
				continue
			}
			seen[gv.Group+"/"+r.Name] = true
			rows = append(rows, map[string]any{"name": r.Name, "kind": r.Kind, "group": gv.Group, "version": gv.Version,
				"namespaced": r.Namespaced, "shortNames": r.ShortNames, "verbs": r.Verbs})
		}
	}
	return map[string]any{"resources": rows, "count": len(rows)}, nil
}
