// SPDX-License-Identifier: MIT
package runtime

import (
	"encoding/json"
	"time"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// envelope is the request the client specs send: a named-user authority with
// the given scope. It lives apart from the supervisor specs so the client-only
// specs keep running on the platforms that have no supervisor.
func envelope(scope string) contract.Envelope {
	return contract.Envelope{Request: contract.Request{AddonID: "sample", Generation: 1, OperationID: "operation", Action: "read", Arguments: json.RawMessage(`{"actor":"not-authority"}`)}, Authority: contract.Authority{Actor: contract.Actor{UserID: "alice", CredentialID: "key", Mode: "named-user"}, Scope: scope, ExpiresAt: time.Now().Add(time.Minute)}}
}
