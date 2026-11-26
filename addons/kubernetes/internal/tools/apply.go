// SPDX-License-Identifier: MIT
package tools

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/mudler/LocalAI/addons/kubernetes/internal/kubeapi"
	"github.com/mudler/LocalAI/addons/kubernetes/internal/policy"
	"github.com/pmezard/go-difflib/difflib"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"
)

// apply and patch show the person a diff before anything is written: the
// live object against the result of the same call made with dryRun=All. The
// API server runs its defaults, validation and admission for the dry run, so
// the diff is what the write would really do. The apply step runs the dry
// run again; a different diff makes the preview stale.

const (
	maxManifest = 16 << 10
	maxPatch    = 8 << 10
	maxDiff     = 10 << 10
)

// diffYAML is a unified diff of two objects after the fields that change on
// every write are dropped. A missing live object diffs against nothing.
func diffYAML(live, next *unstructured.Unstructured) (string, bool) {
	text := func(u *unstructured.Unstructured) string {
		if u == nil {
			return ""
		}
		u = u.DeepCopy()
		clean(u)
		for _, f := range []string{"resourceVersion", "generation", "uid", "creationTimestamp", "selfLink"} {
			unstructured.RemoveNestedField(u.Object, "metadata", f)
		}
		unstructured.RemoveNestedField(u.Object, "status")
		b, _ := yaml.Marshal(u.Object)
		return string(b)
	}
	a, b := text(live), text(next)
	if a == b {
		return "", false
	}
	d, _ := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{A: difflib.SplitLines(a), B: difflib.SplitLines(b),
		FromFile: "live", ToFile: "dry-run", Context: 3})
	return kubeapi.Cap(d, maxDiff)
}

func hashText(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:8])
}

// change says what the diff means for the person.
func change(live *unstructured.Unstructured, diff string) string {
	switch {
	case live == nil:
		return "create"
	case diff == "":
		return "no change"
	}
	return "update"
}

// shown masks a copy of an object for a diff or a result.
func (c *call) shown(u *unstructured.Unstructured, t kubeapi.Target) *unstructured.Unstructured {
	if u == nil {
		return nil
	}
	u = u.DeepCopy()
	c.present(u, t)
	return u
}

// apply -------------------------------------------------------------------------

type applyOp struct {
	Cluster   string `json:"cluster"`
	Manifest  string `json:"manifest"`
	Namespace string `json:"namespace"`

	obj  *unstructured.Unstructured
	t    kubeapi.Target
	diff string
	cut  bool
}

// readManifest reads exactly one object.
func readManifest(text string) (*unstructured.Unstructured, error) {
	d := utilyaml.NewYAMLOrJSONDecoder(strings.NewReader(text), 4096)
	var first map[string]any
	if err := d.Decode(&first); err != nil || len(first) == 0 {
		return nil, bad("the manifest is not one YAML or JSON object")
	}
	for {
		var next map[string]any
		err := d.Decode(&next)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil || len(next) > 0 {
			return nil, bad("the manifest holds more than one object: apply one at a time")
		}
	}
	u := &unstructured.Unstructured{Object: first}
	if u.IsList() || strings.HasSuffix(u.GetKind(), "List") {
		return nil, bad("the manifest is a list: apply one object at a time")
	}
	if u.GetAPIVersion() == "" || u.GetKind() == "" || u.GetName() == "" {
		return nil, bad("the manifest needs apiVersion, kind and metadata.name")
	}
	return u, nil
}

func (c *call) parseApply(raw map[string]json.RawMessage) (writeOp, error) {
	var o applyOp
	if err := decodeArgs(raw, &o); err != nil {
		return nil, err
	}
	if len(o.Manifest) > maxManifest {
		return nil, bad("the manifest has at most %d bytes", maxManifest)
	}
	u, err := readManifest(o.Manifest)
	if err != nil {
		return nil, err
	}
	gvk := u.GroupVersionKind()
	m, err := c.c.Mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return nil, bad("the cluster serves no %s in %s: use api-resources", gvk.Kind, gvk.GroupVersion().String())
	}
	o.t = kubeapi.Target{GVR: m.Resource, Kind: gvk.Kind, Namespaced: m.Scope.Name() == meta.RESTScopeNameNamespace}
	if !o.t.Namespaced {
		return nil, policy.Refuse(policy.RuleClusterScoped, "%s is cluster-scoped, and apply and patch work on namespaced objects only", gvk.Kind)
	}
	ns := u.GetNamespace()
	switch {
	case ns != "" && o.Namespace != "" && ns != o.Namespace:
		return nil, bad("the manifest names the namespace %s and the call names %s", ns, o.Namespace)
	case ns == "":
		ns = o.Namespace
	}
	if ns == "" {
		ns = "default"
	}
	if err := c.allow(ns); err != nil {
		return nil, err
	}
	u.SetNamespace(ns)
	o.obj = u
	return &o, nil
}

func (o *applyOp) inspect(c *call) (map[string]any, uint64, error) {
	live, err := c.c.Dynamic.Resource(o.t.GVR).Namespace(o.obj.GetNamespace()).Get(c.ctx, o.obj.GetName(), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		live, err = nil, nil
	}
	if err != nil {
		return nil, 0, err
	}
	dry, err := c.c.Writer.Apply(c.ctx, o.t.GVR, o.obj.GetNamespace(), o.obj.DeepCopy(), true)
	if err != nil {
		return nil, 0, err
	}
	o.diff, o.cut = diffYAML(c.shown(live, o.t), c.shown(dry, o.t))
	uid := ""
	if live != nil {
		uid = string(live.GetUID())
	}
	s := map[string]any{"tool": ToolApply, "kind": o.t.Kind, "namespace": o.obj.GetNamespace(), "name": o.obj.GetName(),
		"change": change(live, o.diff), "diff": o.diff, "diffCut": o.cut}
	return s, c.revision(ToolApply, o.t.GVR.String(), o.obj.GetNamespace(), o.obj.GetName(), hashText(o.Manifest), hashText(o.diff), uid), nil
}

func (o *applyOp) apply(c *call) (map[string]any, error) {
	if _, err := c.c.Writer.Apply(c.ctx, o.t.GVR, o.obj.GetNamespace(), o.obj.DeepCopy(), false); err != nil {
		return nil, err
	}
	return map[string]any{"applied": o.t.Kind + "/" + o.obj.GetNamespace() + "/" + o.obj.GetName(), "diff": o.diff, "diffCut": o.cut}, nil
}

// patch -------------------------------------------------------------------------

type patchOp struct {
	Cluster   string `json:"cluster"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	PatchType string `json:"patchType"`
	Patch     string `json:"patch"`

	t    kubeapi.Target
	pt   types.PatchType
	data []byte
	diff string
	cut  bool
}

var patchTypes = map[string]types.PatchType{
	"strategic": types.StrategicMergePatchType,
	"merge":     types.MergePatchType,
	"json":      types.JSONPatchType,
}

func (c *call) parsePatch(raw map[string]json.RawMessage) (writeOp, error) {
	var o patchOp
	if err := decodeArgs(raw, &o); err != nil {
		return nil, err
	}
	pt, ok := patchTypes[o.PatchType]
	if !ok {
		return nil, bad("patchType is strategic, merge or json")
	}
	if o.Patch == "" || len(o.Patch) > maxPatch {
		return nil, bad("the patch has 1 to %d bytes", maxPatch)
	}
	data, err := yaml.YAMLToJSON([]byte(o.Patch))
	if err != nil {
		return nil, bad("the patch is not JSON or YAML")
	}
	data = bytes.TrimSpace(data)
	if (pt == types.JSONPatchType) != (len(data) > 0 && data[0] == '[') {
		return nil, bad("a json patch is a list of operations; a strategic or merge patch is an object")
	}
	if o.Name == "" || len(o.Name) > 253 {
		return nil, bad("name the object")
	}
	t, err := c.resolve(o.Kind)
	if err != nil {
		return nil, err
	}
	if !t.Namespaced {
		return nil, policy.Refuse(policy.RuleClusterScoped, "%s is cluster-scoped, and apply and patch work on namespaced objects only", t.Kind)
	}
	if o.Namespace == "" {
		o.Namespace = "default"
	}
	if err := c.allow(o.Namespace); err != nil {
		return nil, err
	}
	o.t, o.pt, o.data = t, pt, data
	return &o, nil
}

func (o *patchOp) inspect(c *call) (map[string]any, uint64, error) {
	live, err := c.c.Dynamic.Resource(o.t.GVR).Namespace(o.Namespace).Get(c.ctx, o.Name, metav1.GetOptions{})
	if err != nil {
		return nil, 0, err
	}
	dry, err := c.c.Writer.Patch(c.ctx, o.t.GVR, o.Namespace, o.Name, o.pt, o.data, true)
	if err != nil {
		return nil, 0, err
	}
	o.diff, o.cut = diffYAML(c.shown(live, o.t), c.shown(dry, o.t))
	s := map[string]any{"tool": ToolPatch, "kind": o.t.Kind, "namespace": o.Namespace, "name": o.Name, "patchType": o.PatchType,
		"change": change(live, o.diff), "diff": o.diff, "diffCut": o.cut}
	return s, c.revision(ToolPatch, o.t.GVR.String(), o.Namespace, o.Name, o.PatchType, hashText(string(o.data)), hashText(o.diff), string(live.GetUID())), nil
}

func (o *patchOp) apply(c *call) (map[string]any, error) {
	if _, err := c.c.Writer.Patch(c.ctx, o.t.GVR, o.Namespace, o.Name, o.pt, o.data, false); err != nil {
		return nil, err
	}
	return map[string]any{"patched": o.t.Kind + "/" + o.Namespace + "/" + o.Name, "diff": o.diff, "diffCut": o.cut}, nil
}
