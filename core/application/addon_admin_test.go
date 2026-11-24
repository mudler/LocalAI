// SPDX-License-Identifier: MIT
package application

import (
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Without an account database the local owner is the administrator, and no
// other actor is.
var _ = Describe("the admin check without an account database", func() {
	It("trusts only the local owner", func() {
		check := addonAdminCheck(nil)
		Expect(check(contract.Actor{UserID: "local-owner", Mode: "local-owner"})).To(BeTrue(),
			"the local owner is not the administrator without an account database")
		Expect(check(contract.Actor{UserID: "u1", Mode: "named-user"})).To(BeFalse(),
			"a named user is an administrator without an account database")
	})
})
