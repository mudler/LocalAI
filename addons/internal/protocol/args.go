// SPDX-License-Identifier: MIT
package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Payload encodes v as the payload of a result. It is for values the add-on
// builds itself, which always encode, so it drops the error.
func Payload(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

// ToolOf returns the tool the host named in the arguments of the request. A
// request with no tool is a bad request.
func ToolOf(env contract.Envelope) (string, error) {
	var a struct {
		Tool string `json:"tool"`
	}
	if json.Unmarshal(env.Request.Arguments, &a) != nil || a.Tool == "" {
		return "", ErrBadRequest
	}
	return a.Tool, nil
}

// DecodeArgs reads the arguments into a closed struct: an unknown field or
// trailing data is ErrBadRequest. No arguments decode as an empty object.
func DecodeArgs(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return ErrBadRequest
	}
	if _, err := dec.Token(); err != io.EOF {
		return ErrBadRequest
	}
	return nil
}

// FromHostError turns the refusal of a host operation into the protocol error
// that tells the owner what happened. A failure that does not say the request
// was wrong stays as it is, and the owner reads it as uncertain.
func FromHostError(err error) error {
	var he *contract.HostError
	if !errors.As(err, &he) {
		return err
	}
	switch he.Code {
	case contract.HostBadRequest, contract.HostTooLarge:
		return fmt.Errorf("%w: %s", ErrBadRequest, he.Message)
	case contract.HostNotFound:
		return fmt.Errorf("%w: %s", ErrNotFound, he.Message)
	case contract.HostConflict:
		return fmt.Errorf("%w: %s", ErrConflict, he.Message)
	case contract.HostFeature, contract.HostScope:
		return fmt.Errorf("%w: %s", ErrForbidden, he.Message)
	}
	return err
}
