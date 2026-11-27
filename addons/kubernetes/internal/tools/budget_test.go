// SPDX-License-Identifier: MIT
package tools_test

import (
	"time"

	"github.com/mudler/LocalAI/addons/kubernetes/internal/policy"
	"github.com/mudler/LocalAI/addons/kubernetes/internal/tools"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"
)

var _ = Describe("the time and size of a read", func() {
	It("a list across many namespaces stops at the read budget and says it is partial", func() {
		g := newRig(pod("team-a", "a", "n", time.Hour), pod("team-e", "e", "n", time.Hour))
		g.setPolicy(policy.Policy{Namespaces: []string{"team-a", "team-b", "team-c", "team-d", "team-e"}})
		// Each list call takes ten seconds.
		g.k.Prepend("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
			g.now = g.now.Add(10 * time.Second)
			return false, nil, nil
		})
		d := g.mustRead(tools.ToolList, map[string]any{"kind": "pods"})
		Expect(d["partial"]).To(BeTrue())
		Expect(d["notRead"]).To(ContainElement("team-e"))
		Expect(d["notRead"]).NotTo(ContainElement("team-a"))
		Expect(d["note"]).To(ContainSubstring("time"))
	})

	It("a list that reads every namespace in time is not partial", func() {
		g := newRig(pod("team-a", "a", "n", time.Hour))
		g.setPolicy(policy.Policy{Namespaces: []string{"team-a", "team-b"}})
		d := g.mustRead(tools.ToolList, map[string]any{"kind": "pods"})
		Expect(d).NotTo(HaveKey("partial"))
	})

	It("describe looks up the replica sets of a deployment by its selector, with a cap", func() {
		d := deployment("team-a", "web", 1)
		g := newRig(d, replicaSet(d, "web-1", "1", "example/app:1"))
		g.mustRead(tools.ToolDescribe, map[string]any{"kind": "deploy", "namespace": "team-a", "name": "web"})
		found := false
		for _, a := range g.k.Dyn.Actions() {
			l, ok := a.(k8stesting.ListActionImpl)
			if !ok || a.GetResource().Resource != "replicasets" {
				continue
			}
			found = true
			Expect(l.GetListOptions().LabelSelector).To(Equal("app=web"))
			Expect(l.GetListOptions().Limit).To(BeNumerically(">", 0))
		}
		Expect(found).To(BeTrue())
	})
})
