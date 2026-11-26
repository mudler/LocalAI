// SPDX-License-Identifier: MIT
package tools

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/pkg/addons/sdk/protocol"
)

// bad is a refusal of the request with a short reason. The reason reaches
// the model, so it never holds a credential.
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

func plainJSON(v any) json.RawMessage {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	_ = e.Encode(v)
	return bytes.TrimSpace(b.Bytes())
}

// sum64 hashes the parts of a revision; each part is length-prefixed so two
// lists never hash alike.
func sum64(parts ...string) uint64 {
	h := sha256.New()
	for _, p := range parts {
		var l [4]byte
		binary.BigEndian.PutUint32(l[:], uint32(len(p)))
		h.Write(l[:])
		h.Write([]byte(p))
	}
	return binary.BigEndian.Uint64(h.Sum(nil)[:8]) | 1
}
