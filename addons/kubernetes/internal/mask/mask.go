// SPDX-License-Identifier: MIT

// Package mask hides Secret values from what the tools return, unless the
// policy of the cluster allows reading them. It runs on every object before
// the object becomes text: YAML, table rows, both sides of a diff.
//
// A Secret keeps its keys, its type and the length of each value. Its
// annotations show only their length too, except a few that Kubernetes sets
// and that hold no value: a write could have put a value there. An
// environment variable of a container whose name looks secret (TOKEN,
// PASSWORD, SECRET or KEY in any case) shows *** instead of an inline value.
// A secretKeyRef stays as it is: it names a Secret and a key, and holds no
// value.
package mask

import (
	"encoding/base64"
	"fmt"
	"regexp"

	"github.com/mudler/LocalAI/addons/kubernetes/internal/kubeapi"
	"github.com/mudler/LocalAI/addons/kubernetes/internal/policy"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Hidden replaces an inline env value.
const Hidden = "***"

const lastApplied = "kubectl.kubernetes.io/last-applied-configuration"

// safeAnnotations are annotations of a Secret that Kubernetes writes and that
// name an object instead of holding a value.
var safeAnnotations = map[string]bool{
	"kubernetes.io/service-account.name": true,
	"kubernetes.io/service-account.uid":  true,
}

var secretName = regexp.MustCompile(`(?i)(TOKEN|PASSWORD|SECRET|KEY)`)

// IsSecret reports whether a target is the core Secret kind.
func IsSecret(t kubeapi.Target) bool {
	return t.GVR.Group == "" && t.GVR.Resource == "secrets"
}

func size(v any, encoded bool) int {
	s, _ := v.(string)
	if encoded {
		if b, err := base64.StdEncoding.DecodeString(s); err == nil {
			return len(b)
		}
	}
	return len(s)
}

func describe(n int) string { return fmt.Sprintf("<%d bytes>", n) }

// Secret replaces the values of a Secret with their length.
func Secret(u *unstructured.Unstructured) {
	for _, f := range []string{"data", "stringData"} {
		m, ok := u.Object[f].(map[string]any)
		if !ok {
			continue
		}
		out := make(map[string]any, len(m))
		for k, v := range m {
			out[k] = describe(size(v, f == "data"))
		}
		u.Object[f] = out
	}
	if a := u.GetAnnotations(); a != nil {
		delete(a, lastApplied)
		for k, v := range a {
			if !safeAnnotations[k] {
				a[k] = describe(len(v))
			}
		}
		u.SetAnnotations(a)
	}
}

// SecretPair masks the live object and the result of a write of one Secret,
// and marks the keys whose value the write changes, so a diff shows a change
// without the values.
func SecretPair(live, next *unstructured.Unstructured) {
	changed := map[string]bool{}
	if next != nil {
		var la map[string]string
		if live != nil {
			la = live.GetAnnotations()
		}
		for k, v := range next.GetAnnotations() {
			if old, ok := la[k]; !ok || old != v {
				changed["annotations/"+k] = true
			}
		}
		for _, f := range []string{"data", "stringData"} {
			nm, _ := next.Object[f].(map[string]any)
			var lm map[string]any
			if live != nil {
				lm, _ = live.Object[f].(map[string]any)
			}
			for k, v := range nm {
				if lm == nil || lm[k] != v {
					changed[f+"/"+k] = true
				}
			}
		}
	}
	if live != nil {
		Secret(live)
	}
	if next == nil {
		return
	}
	Secret(next)
	if a := next.GetAnnotations(); a != nil {
		for k, v := range a {
			if changed["annotations/"+k] && !safeAnnotations[k] {
				a[k] = v + " (new value)"
			}
		}
		next.SetAnnotations(a)
	}
	for _, f := range []string{"data", "stringData"} {
		m, _ := next.Object[f].(map[string]any)
		for k, v := range m {
			if changed[f+"/"+k] {
				m[k] = fmt.Sprintf("%v (new value)", v)
			}
		}
	}
}

var containerLists = map[string]bool{"containers": true, "initContainers": true, "ephemeralContainers": true}

// PodEnv hides the inline value of every env var whose name looks secret, in
// any container list at any depth, so the pod templates of custom resources
// are covered too.
func PodEnv(u *unstructured.Unstructured) { walk(u.Object) }

func walk(v any) {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			if list, ok := e.([]any); ok && containerLists[k] {
				for _, c := range list {
					if cm, ok := c.(map[string]any); ok {
						hideEnv(cm)
					}
				}
			}
			walk(e)
		}
	case []any:
		for _, e := range x {
			walk(e)
		}
	}
}

func hideEnv(container map[string]any) {
	env, _ := container["env"].([]any)
	for _, e := range env {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		if _, has := m["value"]; has && secretName.MatchString(name) {
			m["value"] = Hidden
		}
	}
}

// Apply masks an object as the policy says.
func Apply(u *unstructured.Unstructured, t kubeapi.Target, pol policy.Policy) {
	if pol.ReadSecretValues {
		return
	}
	if IsSecret(t) {
		Secret(u)
		return
	}
	PodEnv(u)
}
