// SPDX-License-Identifier: MIT
package kubeapi_test

import (
	"github.com/mudler/LocalAI/addons/kubernetes/internal/kubeapi"
	"github.com/mudler/LocalAI/addons/kubernetes/kubetest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("resolving a kind", func() {
	m := kubetest.New().Client().Mapper

	DescribeTable("resolves short names, plurals, group-qualified and CRD kinds",
		func(in, group, resource, kind string, namespaced bool) {
			t, err := kubeapi.Resolve(m, in)
			Expect(err).NotTo(HaveOccurred(), in)
			Expect(t.GVR.Group).To(Equal(group), in)
			Expect(t.GVR.Resource).To(Equal(resource), in)
			Expect(t.Kind).To(Equal(kind), in)
			Expect(t.Namespaced).To(Equal(namespaced), in)
		},
		Entry(nil, "pods", "", "pods", "Pod", true),
		Entry(nil, "Pod", "", "pods", "Pod", true),
		Entry(nil, "pod", "", "pods", "Pod", true),
		Entry(nil, "po", "", "pods", "Pod", true),
		Entry(nil, "deploy", "apps", "deployments", "Deployment", true),
		Entry(nil, "deployments.apps", "apps", "deployments", "Deployment", true),
		Entry(nil, "Deployment.apps", "apps", "deployments", "Deployment", true),
		Entry(nil, "apps/v1/Deployment", "apps", "deployments", "Deployment", true),
		Entry(nil, "v1/Secret", "", "secrets", "Secret", true),
		Entry(nil, "secrets.v1.", "", "secrets", "Secret", true),
		Entry(nil, "secret", "", "secrets", "Secret", true),
		Entry(nil, "nodes", "", "nodes", "Node", false),
		Entry(nil, "widgets", "example.com", "widgets", "Widget", true),
		Entry(nil, "widgets.example.com", "example.com", "widgets", "Widget", true),
		Entry(nil, "crd", "apiextensions.k8s.io", "customresourcedefinitions", "CustomResourceDefinition", false),
	)

	It("refuses an unknown kind and names it", func() {
		_, err := kubeapi.Resolve(m, "gizmos")
		Expect(err).To(MatchError(ContainSubstring(`"gizmos"`)))
		_, err = kubeapi.Resolve(m, "")
		Expect(err).To(HaveOccurred())
	})
})
