// SPDX-License-Identifier: MIT
package tools

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/addons/kubernetes/internal/kubeapi"
	"github.com/mudler/LocalAI/addons/kubernetes/internal/mask"
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

// objText is the YAML of an object without the fields that change on every
// write.
func objText(u *unstructured.Unstructured) string {
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

// stateHash hashes both sides of a diff before masking, so a change the
// masked diff hides still makes the preview stale.
func stateHash(live, next *unstructured.Unstructured) string {
	return hashText(objText(live) + "\x00" + objText(next))
}

// diffYAML is a unified diff of two objects after the fields that change on
// every write are dropped. A missing live object diffs against nothing.
func diffYAML(live, next *unstructured.Unstructured) (string, bool) {
	a, b := objText(live), objText(next)
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

// shownPair masks the two sides of a diff. It marks the Secret keys and the
// hidden env values that change, since the masking would otherwise hide the
// change.
func (c *call) shownPair(live, next *unstructured.Unstructured, t kubeapi.Target) (*unstructured.Unstructured, *unstructured.Unstructured) {
	if c.pol.ReadSecretValues {
		return c.shown(live, t), c.shown(next, t)
	}
	var l, n *unstructured.Unstructured
	if live != nil {
		l = live.DeepCopy()
		clean(l)
	}
	if next != nil {
		n = next.DeepCopy()
		clean(n)
	}
	if mask.IsSecret(t) {
		mask.SecretPair(l, n)
	} else {
		mask.EnvPair(l, n)
	}
	return l, n
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
	// A list is kind List, or a list kind of a type (ConfigMapList), which
	// holds its objects in items. A custom kind whose name only ends in List
	// (IPAllowList) is one object; a list kind that the cluster does not
	// serve is refused when it is mapped.
	if u.GetKind() == "List" || (strings.HasSuffix(u.GetKind(), "List") && u.IsList()) {
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

// guard keeps an API error about a Secret from quoting a hidden value.
func (c *call) guard(t kubeapi.Target, err error) error {
	if c.hidesValues(t) {
		return kubeapi.Withhold(err)
	}
	return err
}

func (o *applyOp) inspect(c *call) (map[string]any, uint64, error) {
	s, rev, err := o.look(c)
	return s, rev, c.guard(o.t, err)
}

func (o *applyOp) look(c *call) (map[string]any, uint64, error) {
	live, err := c.c.Dynamic.Resource(o.t.GVR).Namespace(o.obj.GetNamespace()).Get(c.ctx, o.obj.GetName(), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		live, err = nil, nil
	}
	if err != nil {
		return nil, 0, err
	}
	if err := o.resetsHidden(c, live); err != nil {
		return nil, 0, err
	}
	o.pin(c, live)
	dry, err := c.c.Writer.Apply(c.ctx, o.t.GVR, o.obj.GetNamespace(), o.obj.DeepCopy(), true)
	if err != nil {
		return nil, 0, err
	}
	o.diff, o.cut = diffYAML(c.shownPair(live, dry, o.t))
	uid := ""
	if live != nil {
		uid = string(live.GetUID())
	}
	s := map[string]any{"tool": ToolApply, "kind": o.t.Kind, "namespace": o.obj.GetNamespace(), "name": o.obj.GetName(),
		"change": change(live, o.diff), "diff": o.diff, "diffCut": o.cut}
	return s, c.revision(ToolApply, o.t.GVR.String(), o.obj.GetNamespace(), o.obj.GetName(), hashText(o.Manifest), stateHash(live, dry), uid), nil
}

// resetsHidden refuses, while the policy hides values, a manifest that sets
// a hidden value the live object already has: a Secret key under data or
// stringData, a Secret annotation that the masking hides, the last-applied
// annotation of a Secret (it holds the values), or the inline value of an env
// var whose name looks secret. The add-on applies without force, so when
// another field manager owns the value, a wrong guess comes back as a
// conflict and the right one does not: the dry run would tell the model
// whether a guess is right.
func (o *applyOp) resetsHidden(c *call, live *unstructured.Unstructured) error {
	if c.pol.ReadSecretValues || live == nil {
		return nil
	}
	var again []string
	if mask.IsSecret(o.t) {
		have, _ := live.Object["data"].(map[string]any)
		for _, f := range []string{"data", "stringData"} {
			m, _ := o.obj.Object[f].(map[string]any)
			for k := range m {
				if _, ok := have[k]; ok {
					again = append(again, k)
				}
			}
		}
		liveAnn := live.GetAnnotations()
		for k := range o.obj.GetAnnotations() {
			if _, ok := liveAnn[k]; k == mask.LastApplied || (ok && !mask.SafeAnnotation(k)) {
				again = append(again, "the annotation "+k)
			}
		}
	} else {
		have := mask.HiddenEnv(live)
		for k := range mask.HiddenEnv(o.obj) {
			if have[k] {
				again = append(again, k)
			}
		}
	}
	if len(again) == 0 {
		return nil
	}
	slices.Sort(again)
	again = slices.Compact(again)
	return policy.Refuse(policy.RuleReadSecretValues, "the policy hides Secret values, and the manifest sets %s again; an apply of a hidden value that another field manager owns tells whether a guess is right, so it is refused. Leave those values out of the manifest, set them with patch (a strategic or merge patch that replaces the whole value), or ask an administrator", strings.Join(again, ", "))
}

// pin sets the resourceVersion of the manifest to the one of the live object
// read just before, while the policy hides values and the object exists. The
// check of resetsHidden reads the live object, then the dry run and the
// apply write; a change in between (another writer sets a hidden value the
// manifest also sets) would make the outcome depend on the guess again. With
// the resourceVersion pinned, any change in between gives a 409 whatever the
// manifest holds. An ordinary apply, and an apply whose manifest names a
// resourceVersion of its own, keep their usual semantics.
func (o *applyOp) pin(c *call, live *unstructured.Unstructured) {
	if c.pol.ReadSecretValues || live == nil || o.obj.GetResourceVersion() != "" {
		return
	}
	o.obj.SetResourceVersion(live.GetResourceVersion())
}

func (o *applyOp) apply(c *call) (map[string]any, error) {
	if _, err := c.c.Writer.Apply(c.ctx, o.t.GVR, o.obj.GetNamespace(), o.obj.DeepCopy(), false); err != nil {
		return nil, c.guard(o.t, err)
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
	if pt == types.JSONPatchType && !c.pol.ReadSecretValues {
		if err := hiddenSafeOps(data, mask.IsSecret(t)); err != nil {
			return nil, err
		}
	}
	o.t, o.pt, o.data = t, pt, data
	return &o, nil
}

// hidesValues reports whether the policy hides the values of the target.
func (c *call) hidesValues(t kubeapi.Target) bool {
	return !c.pol.ReadSecretValues && mask.IsSecret(t)
}

// hiddenSafeOps refuses a json patch that could bring a hidden value back to
// the model while the policy hides values. It runs on every kind, since an
// env value of a pod template is hidden too. copy and move can put a value
// where the masking does not look, and test answers yes or no about a value,
// so none of them may run. An op that names an env list, or a place under
// one, is refused as well: renaming an env var would show its value. On a
// Secret, an op that names the data, or the whole object, is refused. Keys
// and names are compared without case, so an op the API server would read
// differently is refused too.
func hiddenSafeOps(data []byte, secret bool) error {
	var ops []map[string]json.RawMessage
	if json.Unmarshal(data, &ops) != nil {
		return bad("a json patch is a list of operations")
	}
	refuse := func() error {
		return policy.Refuse(policy.RuleReadSecretValues, "the policy hides Secret values, so a json patch may not copy, move or test, may not name an env list, and on a Secret may not name data or stringData; use a strategic or merge patch, or ask an administrator")
	}
	for _, op := range ops {
		for k, v := range op {
			var s string
			switch strings.ToLower(k) {
			case "op":
				_ = json.Unmarshal(v, &s)
				switch strings.ToLower(s) {
				case "copy", "move", "test":
					return refuse()
				}
			case "path", "from":
				if json.Unmarshal(v, &s) != nil || touchesEnv(s) || (secret && touchesData(s)) {
					return refuse()
				}
			}
		}
	}
	return nil
}

// pointer splits a JSON pointer into its unescaped tokens.
func pointer(p string) []string {
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	for i, t := range parts {
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(t, "~1", "/"), "~0", "~")
	}
	return parts
}

// touchesData reports whether a JSON pointer names the whole object or a
// place under data or stringData.
func touchesData(p string) bool {
	if p == "" || p == "/" {
		return true
	}
	first := pointer(p)[0]
	return strings.EqualFold(first, "data") || strings.EqualFold(first, "stringData")
}

// touchesEnv reports whether a JSON pointer names an env list or a place
// under one, at any depth.
func touchesEnv(p string) bool {
	for _, t := range pointer(p) {
		if strings.EqualFold(t, "env") {
			return true
		}
	}
	return false
}

func (o *patchOp) inspect(c *call) (map[string]any, uint64, error) {
	s, rev, err := o.look(c)
	return s, rev, c.guard(o.t, err)
}

func (o *patchOp) look(c *call) (map[string]any, uint64, error) {
	live, err := c.c.Dynamic.Resource(o.t.GVR).Namespace(o.Namespace).Get(c.ctx, o.Name, metav1.GetOptions{})
	if err != nil {
		return nil, 0, err
	}
	dry, err := c.c.Writer.Patch(c.ctx, o.t.GVR, o.Namespace, o.Name, o.pt, o.data, true)
	if err != nil {
		return nil, 0, err
	}
	o.diff, o.cut = diffYAML(c.shownPair(live, dry, o.t))
	s := map[string]any{"tool": ToolPatch, "kind": o.t.Kind, "namespace": o.Namespace, "name": o.Name, "patchType": o.PatchType,
		"change": change(live, o.diff), "diff": o.diff, "diffCut": o.cut}
	return s, c.revision(ToolPatch, o.t.GVR.String(), o.Namespace, o.Name, o.PatchType, hashText(string(o.data)), stateHash(live, dry), string(live.GetUID())), nil
}

func (o *patchOp) apply(c *call) (map[string]any, error) {
	if _, err := c.c.Writer.Patch(c.ctx, o.t.GVR, o.Namespace, o.Name, o.pt, o.data, false); err != nil {
		return nil, c.guard(o.t, err)
	}
	return map[string]any{"patched": o.t.Kind + "/" + o.Namespace + "/" + o.Name, "diff": o.diff, "diffCut": o.cut}, nil
}
