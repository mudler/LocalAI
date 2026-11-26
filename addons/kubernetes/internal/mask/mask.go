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

// LastApplied is the annotation kubectl keeps the last applied manifest in.
// On a Secret it holds the values, so the masking drops it.
const LastApplied = "kubectl.kubernetes.io/last-applied-configuration"

// safeAnnotations are annotations of a Secret that Kubernetes writes and that
// name an object instead of holding a value.
var safeAnnotations = map[string]bool{
	"kubernetes.io/service-account.name": true,
	"kubernetes.io/service-account.uid":  true,
}

// SafeAnnotation reports an annotation of a Secret that the masking shows.
func SafeAnnotation(k string) bool { return safeAnnotations[k] }

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
		delete(a, LastApplied)
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
// any container list at any depth, so the pod templates of every workload
// kind (a CronJob's jobTemplate too) and of custom resources are covered.
// args, command and the other fields of a container are not scanned.
func PodEnv(u *unstructured.Unstructured) {
	containers(u.Object, "", func(_ string, c map[string]any) { hideEnv(c, nil) })
}

// EnvPair masks the env of the live object and of the result of a write, and
// marks a hidden value that the write changes, so the card shows a change
// that the masking would otherwise hide. A container is matched by the path
// of its list, its name and how many containers of that name come before it
// in the list; an env var by its name and how many vars of that name come
// before it. So an entry added before another does not mark the other as
// changed, and two vars of one name stay apart.
func EnvPair(live, next *unstructured.Unstructured) {
	old := map[string]any{}
	if live != nil {
		containers(live.Object, "", func(at string, c map[string]any) {
			for k, v := range secretEnv(c) {
				old[at+"\x00"+k] = v
			}
		})
		PodEnv(live)
	}
	if next == nil {
		return
	}
	containers(next.Object, "", func(at string, c map[string]any) {
		changed := map[string]bool{}
		for k, v := range secretEnv(c) {
			if was, ok := old[at+"\x00"+k]; !ok || was != v {
				changed[k] = true
			}
		}
		hideEnv(c, changed)
	})
}

// HiddenEnv returns the env vars whose inline value the masking hides, each
// as the name of its container list, the name of its container and its own
// name. A server-side apply that sets one of them again can tell, by a
// conflict, whether a guess of the value is right. An init container and a
// container of one name are two containers.
func HiddenEnv(u *unstructured.Unstructured) map[string]bool {
	out := map[string]bool{}
	if u == nil {
		return out
	}
	walk(u.Object, "", func(_, list, cname string, _ int, c map[string]any) {
		env, _ := c["env"].([]any)
		for _, e := range env {
			m, ok := e.(map[string]any)
			if !ok {
				continue
			}
			name, _ := m["name"].(string)
			if _, has := m["value"]; has && secretName.MatchString(name) {
				out[list+"/"+cname+"/"+name] = true
			}
		}
	})
	return out
}

// containers calls fn for each container of any container list at any
// depth. at names the place of the container: the path of its list, its name
// and the count of containers of that name before it in the list.
func containers(v any, path string, fn func(at string, c map[string]any)) {
	walk(v, path, func(path, list, name string, nth int, c map[string]any) {
		fn(fmt.Sprintf("%s/%s/%s#%d", path, list, name, nth), c)
	})
}

func walk(v any, path string, fn func(path, list, name string, nth int, c map[string]any)) {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			if list, ok := e.([]any); ok && containerLists[k] {
				seen := map[string]int{}
				for _, c := range list {
					if cm, ok := c.(map[string]any); ok {
						name, _ := cm["name"].(string)
						fn(path, k, name, seen[name], cm)
						seen[name]++
					}
				}
			}
			walk(e, path+"/"+k, fn)
		}
	case []any:
		for i, e := range x {
			walk(e, fmt.Sprintf("%s/%d", path, i), fn)
		}
	}
}

// secretEnv returns the inline values of the env vars whose name looks
// secret, keyed by envKey.
func secretEnv(container map[string]any) map[string]any {
	out := map[string]any{}
	eachSecretEnv(container, func(k string, m map[string]any) { out[k] = m["value"] })
	return out
}

// eachSecretEnv calls fn for each env var whose name looks secret and that
// has an inline value, with its envKey.
func eachSecretEnv(container map[string]any, fn func(k string, m map[string]any)) {
	env, _ := container["env"].([]any)
	seen := map[string]int{}
	for _, e := range env {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		nth := seen[name]
		seen[name]++
		if _, has := m["value"]; has && secretName.MatchString(name) {
			fn(envKey(name, nth), m)
		}
	}
}

// envKey is the name of an env var and how many vars of that name come
// before it in its list.
func envKey(name string, nth int) string { return fmt.Sprintf("%s#%d", name, nth) }

func hideEnv(container map[string]any, changed map[string]bool) {
	eachSecretEnv(container, func(k string, m map[string]any) {
		m["value"] = Hidden
		if changed[k] {
			m["value"] = Hidden + " (new value)"
		}
	})
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
