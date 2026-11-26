// SPDX-License-Identifier: MIT

// Package kubetest is a fake cluster for the tests of the Kubernetes add-on.
// The typed and the dynamic fake clients share one object tracker, so a write
// through one is read through the other. Logs, metrics, exec and the dry run
// of apply and patch are fakes of their own: client-go's fakes do not serve
// them. The real behaviour of those comes from the end-to-end suite.
package kubetest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/mudler/LocalAI/addons/kubernetes/internal/kubeapi"
	jsonpatch "gopkg.in/evanphx/json-patch.v4"
	authv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"k8s.io/apimachinery/pkg/version"
	fakediscovery "k8s.io/client-go/discovery/fake"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	k8stesting "k8s.io/client-go/testing"
)

// ExecCall is one command the fake ran.
type ExecCall struct {
	Namespace, Pod, Container string
	Command                   []string
}

// Cluster is a fake cluster.
type Cluster struct {
	Typed *fake.Clientset
	Dyn   *dynamicfake.FakeDynamicClient

	client *kubeapi.Client

	mu         sync.Mutex
	logs       map[string]string
	logOpts    []corev1.PodLogOptions
	nodes      []kubeapi.Usage
	pods       []kubeapi.Usage
	hasMetrics bool
	execRes    kubeapi.ExecResult
	execErr    error
	execs      []ExecCall
	execHook   func(ctx context.Context) error
	rules      []authv1.ResourceRule
	writes     int
}

func res(name, kind string, namespaced bool, verbs []string, short ...string) metav1.APIResource {
	return metav1.APIResource{Name: name, SingularName: strings.ToLower(kind), Kind: kind, Namespaced: namespaced, Verbs: verbs, ShortNames: short}
}

var all = []string{"get", "list", "watch", "create", "update", "patch", "delete"}

// Resources is what the fake discovery serves.
var Resources = []*metav1.APIResourceList{
	{GroupVersion: "v1", APIResources: []metav1.APIResource{
		res("pods", "Pod", true, all, "po"), {Name: "pods/log", Kind: "Pod", Namespaced: true, Verbs: []string{"get"}},
		{Name: "pods/eviction", Kind: "Eviction", Group: "policy", Version: "v1", Namespaced: true, Verbs: []string{"create"}},
		res("services", "Service", true, all, "svc"), res("secrets", "Secret", true, all), res("configmaps", "ConfigMap", true, all, "cm"),
		res("namespaces", "Namespace", false, all, "ns"), res("nodes", "Node", false, all, "no"), res("events", "Event", true, all, "ev"),
		res("serviceaccounts", "ServiceAccount", true, all, "sa"),
	}},
	{GroupVersion: "apps/v1", APIResources: []metav1.APIResource{
		res("deployments", "Deployment", true, all, "deploy"), res("statefulsets", "StatefulSet", true, all, "sts"),
		res("daemonsets", "DaemonSet", true, all, "ds"), res("replicasets", "ReplicaSet", true, all, "rs"),
	}},
	{GroupVersion: "policy/v1", APIResources: []metav1.APIResource{res("poddisruptionbudgets", "PodDisruptionBudget", true, all, "pdb")}},
	{GroupVersion: "batch/v1", APIResources: []metav1.APIResource{res("jobs", "Job", true, all)}},
	{GroupVersion: "rbac.authorization.k8s.io/v1", APIResources: []metav1.APIResource{
		res("clusterroles", "ClusterRole", false, all), res("roles", "Role", true, all)}},
	{GroupVersion: "apiextensions.k8s.io/v1", APIResources: []metav1.APIResource{res("customresourcedefinitions", "CustomResourceDefinition", false, all, "crd", "crds")}},
	{GroupVersion: "example.com/v1", APIResources: []metav1.APIResource{res("widgets", "Widget", true, all, "wd"),
		// A custom kind whose name ends in List, as some network add-ons have.
		res("ipallowlists", "IPAllowList", true, all)}},
}

var listKinds = map[schema.GroupVersionResource]string{
	{Version: "v1", Resource: "pods"}: "PodList", {Version: "v1", Resource: "services"}: "ServiceList",
	{Version: "v1", Resource: "secrets"}: "SecretList", {Version: "v1", Resource: "configmaps"}: "ConfigMapList",
	{Version: "v1", Resource: "namespaces"}: "NamespaceList", {Version: "v1", Resource: "nodes"}: "NodeList",
	{Version: "v1", Resource: "events"}: "EventList", {Version: "v1", Resource: "serviceaccounts"}: "ServiceAccountList",
	{Group: "apps", Version: "v1", Resource: "deployments"}: "DeploymentList", {Group: "apps", Version: "v1", Resource: "statefulsets"}: "StatefulSetList",
	{Group: "apps", Version: "v1", Resource: "daemonsets"}: "DaemonSetList", {Group: "apps", Version: "v1", Resource: "replicasets"}: "ReplicaSetList",
	{Group: "policy", Version: "v1", Resource: "poddisruptionbudgets"}: "PodDisruptionBudgetList", {Group: "batch", Version: "v1", Resource: "jobs"}: "JobList",
	{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles"}: "ClusterRoleList",
	{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "roles"}:        "RoleList",
	{Group: "example.com", Version: "v1", Resource: "widgets"}:                    "WidgetList",
	{Group: "example.com", Version: "v1", Resource: "ipallowlists"}:               "IPAllowListList",
}

// New returns a cluster with the objects in it.
func New(objs ...runtime.Object) *Cluster {
	typed := fake.NewClientset(objs...)
	disc := typed.Discovery().(*fakediscovery.FakeDiscovery)
	disc.Resources = Resources
	disc.FakedServerVersion = &version.Info{GitVersion: "v1.37.0", Major: "1", Minor: "37"}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme.Scheme, listKinds)
	dyn.PrependReactor("*", "*", k8stesting.ObjectReaction(typed.Tracker()))
	k := &Cluster{Typed: typed, Dyn: dyn, logs: map[string]string{}}
	groups, err := restmapper.GetAPIGroupResources(disc)
	if err != nil {
		panic(err)
	}
	mapper := restmapper.NewShortcutExpander(restmapper.NewDiscoveryRESTMapper(groups), disc, nil)
	typed.PrependReactor("create", "selfsubjectrulesreviews", func(a k8stesting.Action) (bool, runtime.Object, error) {
		k.mu.Lock()
		defer k.mu.Unlock()
		in := a.(k8stesting.CreateAction).GetObject().(*authv1.SelfSubjectRulesReview)
		out := in.DeepCopy()
		out.Status.ResourceRules = append([]authv1.ResourceRule(nil), k.rules...)
		return true, out, nil
	})
	// An eviction that no test refuses removes the pod, as the API server
	// does when the budgets allow it.
	typed.PrependReactor("create", "pods", func(a k8stesting.Action) (bool, runtime.Object, error) {
		if a.GetSubresource() != "eviction" {
			return false, nil, nil
		}
		ev := a.(k8stesting.CreateAction).GetObject().(*policyv1.Eviction)
		err := typed.Tracker().Delete(schema.GroupVersionResource{Version: "v1", Resource: "pods"}, ev.Namespace, ev.Name)
		return true, ev, err
	})
	k.client = &kubeapi.Client{Typed: typed, Dynamic: dyn, Discovery: disc, Mapper: mapper,
		Logs: logs{k}, Metrics: metricsFake{k}, Exec: execFake{k}, Writer: writer{k}}
	return k
}

// Client is the client of the fake cluster.
func (k *Cluster) Client() *kubeapi.Client { return k.client }

// Factory ignores the config and returns the fake.
func (k *Cluster) Factory() kubeapi.Factory {
	return func(*rest.Config) (*kubeapi.Client, error) { return k.client, nil }
}

// Prepend adds a reactor to the typed and the dynamic client.
func (k *Cluster) Prepend(verb, resource string, fn k8stesting.ReactionFunc) {
	k.Typed.PrependReactor(verb, resource, fn)
	k.Dyn.PrependReactor(verb, resource, fn)
}

// Writes counts the actions that change something, of both clients.
func (k *Cluster) Writes() int {
	n := 0
	for _, list := range [][]k8stesting.Action{k.Typed.Actions(), k.Dyn.Actions()} {
		for _, a := range list {
			switch a.GetVerb() {
			case "create", "update", "patch", "delete", "deletecollection":
				if a.GetResource().Resource == "selfsubjectrulesreviews" {
					continue
				}
				n++
			}
		}
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	return n + k.writes
}

// SetLogs sets the log of a container.
func (k *Cluster) SetLogs(ns, pod, container, text string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.logs[ns+"/"+pod+"/"+container] = text
}

// LogOptions are the options of every log read, in order.
func (k *Cluster) LogOptions() []corev1.PodLogOptions {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]corev1.PodLogOptions(nil), k.logOpts...)
}

// SetMetrics serves metrics. Nil for both means the cluster has none.
func (k *Cluster) SetMetrics(nodes, pods []kubeapi.Usage) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.nodes, k.pods, k.hasMetrics = nodes, pods, nodes != nil || pods != nil
}

// SetExec sets the answer of every exec.
func (k *Cluster) SetExec(r kubeapi.ExecResult, err error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.execRes, k.execErr = r, err
}

// SetExecHook runs fn in every exec before it answers; a test blocks there.
func (k *Cluster) SetExecHook(fn func(ctx context.Context) error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.execHook = fn
}

// Execs lists the commands that ran.
func (k *Cluster) Execs() []ExecCall {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]ExecCall(nil), k.execs...)
}

// SetRules sets what SelfSubjectRulesReview answers.
func (k *Cluster) SetRules(r []authv1.ResourceRule) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.rules = r
}

type logs struct{ k *Cluster }

func (l logs) Logs(_ context.Context, ns, pod string, o *corev1.PodLogOptions) (io.ReadCloser, error) {
	l.k.mu.Lock()
	defer l.k.mu.Unlock()
	l.k.logOpts = append(l.k.logOpts, *o)
	text, ok := l.k.logs[ns+"/"+pod+"/"+o.Container]
	if !ok {
		return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "pods"}, pod)
	}
	return io.NopCloser(strings.NewReader(text)), nil
}

type metricsFake struct{ k *Cluster }

func (m metricsFake) Nodes(context.Context) ([]kubeapi.Usage, error) {
	m.k.mu.Lock()
	defer m.k.mu.Unlock()
	if !m.k.hasMetrics {
		return nil, kubeapi.ErrNoMetrics
	}
	return m.k.nodes, nil
}

func (m metricsFake) Pods(_ context.Context, ns string) ([]kubeapi.Usage, error) {
	m.k.mu.Lock()
	defer m.k.mu.Unlock()
	if !m.k.hasMetrics {
		return nil, kubeapi.ErrNoMetrics
	}
	var out []kubeapi.Usage
	for _, p := range m.k.pods {
		if ns == "" || p.Namespace == ns {
			out = append(out, p)
		}
	}
	return out, nil
}

type execFake struct{ k *Cluster }

func (e execFake) Exec(ctx context.Context, ns, pod, container string, cmd []string) (kubeapi.ExecResult, error) {
	e.k.mu.Lock()
	e.k.execs = append(e.k.execs, ExecCall{Namespace: ns, Pod: pod, Container: container, Command: append([]string(nil), cmd...)})
	hook, res, err := e.k.execHook, e.k.execRes, e.k.execErr
	e.k.mu.Unlock()
	if hook != nil {
		if herr := hook(ctx); herr != nil {
			return kubeapi.ExecResult{}, herr
		}
	}
	return res, err
}

// writer approximates server-side apply and patch on the tracker. A dry run
// computes the result and stores nothing.
type writer struct{ k *Cluster }

func (w writer) live(gvr schema.GroupVersionResource, ns, name string) (*unstructured.Unstructured, error) {
	u, err := w.k.Dyn.Resource(gvr).Namespace(ns).Get(context.Background(), name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	return u, err
}

func (w writer) store(gvr schema.GroupVersionResource, ns string, u *unstructured.Unstructured, exists bool) (*unstructured.Unstructured, error) {
	var obj runtime.Object = u
	if typed, err := scheme.Scheme.New(u.GroupVersionKind()); err == nil {
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, typed); err != nil {
			return nil, apierrors.NewBadRequest(err.Error())
		}
		obj = typed
	}
	w.k.mu.Lock()
	w.k.writes++
	w.k.mu.Unlock()
	tr := w.k.Typed.Tracker()
	if exists {
		if err := tr.Update(gvr, obj, ns); err != nil {
			return nil, err
		}
	} else if err := tr.Create(gvr, obj, ns); err != nil {
		return nil, err
	}
	return w.live(gvr, ns, u.GetName())
}

func (w writer) Apply(_ context.Context, gvr schema.GroupVersionResource, ns string, obj *unstructured.Unstructured, dryRun bool) (*unstructured.Unstructured, error) {
	live, err := w.live(gvr, ns, obj.GetName())
	if err != nil {
		return nil, err
	}
	next := obj.DeepCopy()
	next.SetNamespace(ns)
	// As the API server: a resourceVersion in the object must match the live
	// one.
	if rv := obj.GetResourceVersion(); rv != "" && (live == nil || live.GetResourceVersion() != rv) {
		return nil, apierrors.NewConflict(gvr.GroupResource(), obj.GetName(), errors.New("the object has been modified"))
	}
	if live != nil {
		base, _ := json.Marshal(live.Object)
		patch, _ := json.Marshal(obj.Object)
		merged, err := jsonpatch.MergePatch(base, patch)
		if err != nil {
			return nil, apierrors.NewBadRequest(err.Error())
		}
		next = &unstructured.Unstructured{}
		if err := json.Unmarshal(merged, &next.Object); err != nil {
			return nil, err
		}
	}
	if dryRun {
		return next, nil
	}
	return w.store(gvr, ns, next, live != nil)
}

func (w writer) Patch(_ context.Context, gvr schema.GroupVersionResource, ns, name string, pt types.PatchType, data []byte, dryRun bool) (*unstructured.Unstructured, error) {
	live, err := w.live(gvr, ns, name)
	if err != nil {
		return nil, err
	}
	if live == nil {
		return nil, apierrors.NewNotFound(gvr.GroupResource(), name)
	}
	base, _ := json.Marshal(live.Object)
	var out []byte
	switch pt {
	case types.MergePatchType:
		out, err = jsonpatch.MergePatch(base, data)
	case types.JSONPatchType:
		var p jsonpatch.Patch
		if p, err = jsonpatch.DecodePatch(data); err == nil {
			out, err = p.Apply(base)
		}
	case types.StrategicMergePatchType:
		typed, serr := scheme.Scheme.New(live.GroupVersionKind())
		if serr != nil {
			return nil, &apierrors.StatusError{ErrStatus: metav1.Status{Status: metav1.StatusFailure, Code: http.StatusUnsupportedMediaType,
				Reason: metav1.StatusReasonUnsupportedMediaType, Message: "the body of the request was in an unknown format - accepted media types include: application/json-patch+json, application/merge-patch+json, application/apply-patch+yaml"}}
		}
		out, err = strategicpatch.StrategicMergePatch(base, data, typed)
	default:
		return nil, errors.New("unknown patch type")
	}
	if err != nil {
		return nil, apierrors.NewBadRequest(err.Error())
	}
	next := &unstructured.Unstructured{}
	if err := json.NewDecoder(bytes.NewReader(out)).Decode(&next.Object); err != nil {
		return nil, apierrors.NewBadRequest(err.Error())
	}
	if dryRun {
		return next, nil
	}
	return w.store(gvr, ns, next, true)
}
