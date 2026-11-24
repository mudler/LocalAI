// SPDX-License-Identifier: MIT
package tools_test

import (
	"encoding/json"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

func env(user, action, scope string, args map[string]any) contract.Envelope {
	raw, _ := json.Marshal(args)
	return contract.Envelope{
		Request:   contract.Request{AddonID: "contacts", OperationID: "op-1", Generation: 1, Action: action, Arguments: raw},
		Authority: contract.Authority{Actor: contract.Actor{UserID: user, CredentialID: "c1", Mode: "named-user"}, Scope: scope, ExpiresAt: time.Now().Add(time.Hour)},
	}
}
