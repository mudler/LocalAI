// SPDX-License-Identifier: MIT
package application

import (
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("add-on tools offered to a chat by the features of the person", func() {
	hostOps := map[string][]string{"fine-tuning": {"finetune."}, "quantization": {"quantize."}, "sandbox": {"workspace."}}
	all := []studio.AddonTool{{Name: "addon__fine-tuning__fine-tune-list", AddonID: "fine-tuning"}, {Name: "addon__quantization__quantize-list", AddonID: "quantization"},
		{Name: "addon__sandbox__bash", AddonID: "sandbox"}, {Name: "addon__tables__tables-create", AddonID: "tables"}}
	names := func(in []studio.AddonTool) []string {
		var out []string
		for _, t := range in {
			out = append(out, t.AddonID)
		}
		return out
	}
	named := contract.Actor{UserID: "member", Mode: auth.AddonModeNamedUser}
	holds := func(features ...string) func(string, string) bool {
		return func(_, f string) bool {
			for _, x := range features {
				if x == f {
					return true
				}
			}
			return false
		}
	}

	It("hides the fine-tuning and quantization tools from a person without those permissions", func() {
		Expect(names(filterByFeature(all, hostOps, holds(), named))).To(Equal([]string{"sandbox", "tables"}))
	})
	It("keeps the tools of the permissions the person holds", func() {
		Expect(names(filterByFeature(all, hostOps, holds("quantization"), named))).To(Equal([]string{"quantization", "sandbox", "tables"}))
		Expect(names(filterByFeature(all, hostOps, holds("fine_tuning", "quantization"), named))).To(HaveLen(4))
	})
	It("offers everything in local-owner mode, which has no accounts", func() {
		owner := contract.Actor{UserID: auth.AddonLocalOwnerID, Mode: auth.AddonModeLocalOwner}
		Expect(filterByFeature(all, hostOps, holds(), owner)).To(HaveLen(4))
	})
	It("offers everything when no check is wired", func() {
		Expect(filterByFeature(all, hostOps, nil, named)).To(HaveLen(4))
	})
})
