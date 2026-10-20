// SPDX-License-Identifier: MIT
package application

import (
	"testing"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Without an account database the local owner is the administrator, and no
// other actor is.
func TestTheAdminCheckWithoutAnAccountDatabaseTrustsOnlyTheLocalOwner(t *testing.T) {
	check := addonAdminCheck(nil)
	if !check(contract.Actor{UserID: "local-owner", Mode: "local-owner"}) {
		t.Fatal("the local owner is not the administrator without an account database")
	}
	if check(contract.Actor{UserID: "u1", Mode: "named-user"}) {
		t.Fatal("a named user is an administrator without an account database")
	}
}
