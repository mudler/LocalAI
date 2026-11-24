// SPDX-License-Identifier: MIT
package studio

import (
	"github.com/mudler/LocalAI/core/services/studio/session"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("execution binding selection policy", func() {
	It("uses oldest install ordering and never substitutes another installed pinned-idle provider", func() {
		defs := []AddonTool{{Name: "addon__new__run", AddonID: "new", Shadows: "bash", Action: true, InstalledAt: "2025-02-01T00:00:00Z"}, {Name: "addon__old__run", AddonID: "old", Shadows: "bash", Action: true, InstalledAt: "2025-01-01T00:00:00Z"}}
		installed := []AddonShadower{{AddonID: "new", Tools: []string{"bash"}}, {AddonID: "old", Tools: []string{"bash"}}}
		rec := session.ChatRecord{}
		selected, ok := SelectExecutionProvider(defs, installed, rec, nil, "")
		Expect(ok).To(BeTrue())
		Expect(selected.AddonID).To(Equal("old"))
		rec.AddonsOff = []string{"old"}
		_, ok = SelectExecutionProvider(defs, installed, rec, nil, "old")
		Expect(ok).To(BeFalse())
		selected, ok = SelectExecutionProvider(defs, installed, rec, nil, "not-installed")
		Expect(ok).To(BeTrue())
		Expect(selected.AddonID).To(Equal("new"))
		_, ok = SelectExecutionProvider(defs[:1], installed, session.ChatRecord{}, nil, "old")
		Expect(ok).To(BeFalse())
	})
})
