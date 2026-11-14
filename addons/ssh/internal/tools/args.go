// SPDX-License-Identifier: MIT
package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/pkg/addons/sdk/protocol"
)

// bad is a refusal of the request with a short, fixed reason. The reason
// reaches the model, so it never holds the credential, the text of a command
// or text the remote machine sent.
func bad(format string, a ...any) error {
	return fmt.Errorf("%w: %s", protocol.ErrBadRequest, fmt.Sprintf(format, a...))
}

// decodeArgs reads the arguments of a tool, less the tool name, into v. An
// unknown field is a refusal.
func decodeArgs(raw map[string]json.RawMessage, v any) error {
	b, _ := json.Marshal(raw)
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return bad("the arguments do not match the tool")
	}
	return nil
}

func short(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
