// SPDX-License-Identifier: MIT
package kubeapi_test

import (
	"context"

	"github.com/mudler/LocalAI/addons/kubernetes/internal/kubeapi"
	"github.com/mudler/LocalAI/addons/kubernetes/kubetest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	authv1 "k8s.io/api/authorization/v1"
)

var _ = Describe("the check of a connection", func() {
	It("reports the version and the rules of the default namespace", func() {
		k := kubetest.New()
		k.SetRules([]authv1.ResourceRule{{Verbs: []string{"get", "list"}, APIGroups: []string{""}, Resources: []string{"pods"}}})
		r, err := kubeapi.Check(context.Background(), k.Client(), "")
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Version).To(Equal("v1.37.0"))
		Expect(r.Namespace).To(Equal("default"))
		Expect(r.Rules).To(Equal([]kubeapi.Rule{{Verbs: []string{"get", "list"}, APIGroups: []string{""}, Resources: []string{"pods"}}}))
	})

	It("cuts the rules at the cap and says so", func() {
		k := kubetest.New()
		many := make([]authv1.ResourceRule, kubeapi.MaxRules+5)
		for i := range many {
			many[i] = authv1.ResourceRule{Verbs: []string{"get"}}
		}
		k.SetRules(many)
		r, err := kubeapi.Check(context.Background(), k.Client(), "team-a")
		Expect(err).NotTo(HaveOccurred())
		Expect(r.Rules).To(HaveLen(kubeapi.MaxRules))
		Expect(r.RulesCut).To(BeTrue())
		Expect(r.Namespace).To(Equal("team-a"))
	})
})
