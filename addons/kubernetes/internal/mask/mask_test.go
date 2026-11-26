// SPDX-License-Identifier: MIT
package mask_test

import (
	"github.com/mudler/LocalAI/addons/kubernetes/internal/kubeapi"
	"github.com/mudler/LocalAI/addons/kubernetes/internal/mask"
	"github.com/mudler/LocalAI/addons/kubernetes/internal/policy"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	secrets = kubeapi.Target{GVR: schema.GroupVersionResource{Version: "v1", Resource: "secrets"}, Kind: "Secret", Namespaced: true}
	widgets = kubeapi.Target{GVR: schema.GroupVersionResource{Group: "example.com", Version: "v1", Resource: "widgets"}, Kind: "Widget", Namespaced: true}
)

func secret() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Secret", "type": "Opaque",
		"metadata":   map[string]any{"name": "db", "annotations": map[string]any{"kubectl.kubernetes.io/last-applied-configuration": `{"data":{"password":"c2VjcmV0LXZhbHVl"}}`, "team": "a"}},
		"data":       map[string]any{"password": "c2VjcmV0LXZhbHVl"},
		"stringData": map[string]any{"user": "admin"}}}
}

func env(name string, value any) map[string]any {
	return map[string]any{"name": name, "value": value}
}

// widget nests a pod template deep inside a custom resource.
func widget() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{"spec": map[string]any{"jobs": []any{map[string]any{"template": map[string]any{"spec": map[string]any{
		"initContainers": []any{map[string]any{"name": "init", "env": []any{env("github_token", "ghp-1"), env("MODE", "x")}}},
		"containers": []any{map[string]any{"name": "app", "env": []any{
			env("DB_PASSWORD", "pw"), env("Api_Key", "k"), env("CLIENT_SECRET", "s"), env("MODE", "prod"),
			map[string]any{"name": "REF_PASSWORD", "valueFrom": map[string]any{"secretKeyRef": map[string]any{"name": "db", "key": "password"}}},
		}}},
	}}}}}}}
}

var _ = Describe("masking", func() {
	It("hides Secret data and stringData, keeps keys, type and byte length", func() {
		s := secret()
		mask.Apply(s, secrets, policy.Defaults())
		Expect(s.Object["data"]).To(Equal(map[string]any{"password": "<12 bytes>"}))
		Expect(s.Object["stringData"]).To(Equal(map[string]any{"user": "<5 bytes>"}))
		Expect(s.Object["type"]).To(Equal("Opaque"))
	})

	It("removes the last-applied annotation of a Secret", func() {
		s := secret()
		mask.Apply(s, secrets, policy.Defaults())
		Expect(s.GetAnnotations()).To(Equal(map[string]string{"team": "a"}))
	})

	It("masks inline env values by name, case-insensitive, at any depth", func() {
		w := widget()
		mask.Apply(w, widgets, policy.Defaults())
		y := w.Object["spec"].(map[string]any)["jobs"].([]any)[0].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
		app := y["containers"].([]any)[0].(map[string]any)["env"].([]any)
		for i := range 3 {
			Expect(app[i].(map[string]any)["value"]).To(Equal(mask.Hidden))
		}
		init := y["initContainers"].([]any)[0].(map[string]any)["env"].([]any)
		Expect(init[0].(map[string]any)["value"]).To(Equal(mask.Hidden))
	})

	It("leaves non-secret env and secretKeyRef refs alone", func() {
		w := widget()
		mask.Apply(w, widgets, policy.Defaults())
		y := w.Object["spec"].(map[string]any)["jobs"].([]any)[0].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
		app := y["containers"].([]any)[0].(map[string]any)["env"].([]any)
		Expect(app[3].(map[string]any)["value"]).To(Equal("prod"))
		Expect(app[4].(map[string]any)["valueFrom"]).To(HaveKey("secretKeyRef"))
		Expect(app[4].(map[string]any)).NotTo(HaveKey("value"))
	})

	It("does nothing when readSecretValues is true", func() {
		p := policy.Defaults()
		p.ReadSecretValues = true
		s := secret()
		mask.Apply(s, secrets, p)
		Expect(s.Object["data"]).To(Equal(map[string]any{"password": "c2VjcmV0LXZhbHVl"}))
		w := widget()
		mask.Apply(w, widgets, p)
		Expect(w.Object).To(Equal(widget().Object))
	})

	It("marks the keys a write changes without showing either value", func() {
		live, next := secret(), secret()
		next.Object["data"] = map[string]any{"password": "bmV3LXNlY3JldC12YWx1", "extra": "eA=="}
		mask.SecretPair(live, next)
		Expect(live.Object["data"]).To(Equal(map[string]any{"password": "<12 bytes>"}))
		Expect(next.Object["data"]).To(Equal(map[string]any{"password": "<15 bytes> (new value)", "extra": "<1 bytes> (new value)"}))
		Expect(next.Object["stringData"]).To(Equal(map[string]any{"user": "<5 bytes>"}))
	})
})
