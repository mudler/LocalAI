// SPDX-License-Identifier: MIT
package application

import (
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the files principal of an add-on actor", func() {
	local := contract.Actor{UserID: auth.AddonLocalOwnerID, Mode: auth.AddonModeLocalOwner}

	It("is the anonymous user of the chats for local-owner mode without accounts", func() {
		Expect(addonWorkspacePrincipal(false)(local)).To(Equal(studio.AnonymousPrincipal))
		Expect(addonWorkspacePrincipal(false)(contract.Actor{})).To(Equal(studio.AnonymousPrincipal))
	})

	It("is the account for a named user", func() {
		Expect(addonWorkspacePrincipal(true)(contract.Actor{UserID: "alice"})).To(Equal("alice"))
		Expect(addonWorkspacePrincipal(false)(contract.Actor{UserID: "alice"})).To(Equal("alice"))
	})

	It("is refused for local-owner mode when accounts exist", func() {
		Expect(addonWorkspacePrincipal(true)(local)).To(BeEmpty())
		Expect(addonWorkspacePrincipal(true)(contract.Actor{})).To(BeEmpty())
	})
})
