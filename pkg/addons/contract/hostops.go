// SPDX-License-Identifier: MIT
package contract

import (
	"encoding/json"
	"regexp"
)

// The host operation channel is how an add-on process asks the host to do
// something it cannot do itself. These are the wire types of that channel.
// Every operation is registered by the host, allowed per add-on by a host
// setting, and checked against the authority of a request the host is
// serving for that add-on. The default is refuse.

// HostRequest names one host operation. OperationID is the ID of the request
// the host is serving for the add-on; it never names a user.
type HostRequest struct {
	OperationID string          `json:"operationId"`
	Op          string          `json:"op"`
	Arguments   json.RawMessage `json:"arguments,omitempty" swaggertype:"object"`
}

// HostResponse carries a result or an error, never both.
type HostResponse struct {
	Result json.RawMessage `json:"result,omitempty" swaggertype:"object"`
	Error  *HostError      `json:"error,omitempty"`
}

// HostError is a refusal or a failure of a host operation. Code is one of the
// Host* constants below.
type HostError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *HostError) Error() string { return "host operation refused (" + e.Code + "): " + e.Message }

const (
	HostNoLiveOperation = "no_live_operation"
	HostScope           = "scope"
	HostFeature         = "feature"
	HostUnknownOp       = "unknown_op"
	HostBadRequest      = "bad_request"
	HostTooLarge        = "too_large"
	HostNotFound        = "not_found"
	HostConflict        = "conflict"
	HostUnavailable     = "unavailable"
)

// HostCredentialHeader carries the host credential of the incarnation.
// #nosec G101 -- this is a header name, not a credential value.
const HostCredentialHeader = "X-Addon-Host-Credential"

// hostOpName is the brief's shape ^[a-z]+\.[a-z.]+$ without an empty word:
// no trailing dot and no double dot.
var hostOpName = regexp.MustCompile(`^[a-z]+(\.[a-z]+)+$`)

// ValidHostOp reports whether op has the shape of a host operation name:
// lower-case words joined by dots, such as finetune.get.
func ValidHostOp(op string) bool { return len(op) <= 64 && hostOpName.MatchString(op) }

// DecodeHostRequest decodes one request with the strict decoder: no unknown
// field, no duplicate key, no trailing data, bounded size.
func DecodeHostRequest(b []byte, max int64) (HostRequest, error) {
	var r HostRequest
	if err := strictJSON(b, &r, max); err != nil {
		return HostRequest{}, err
	}
	return r, nil
}
