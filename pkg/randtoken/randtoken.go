// SPDX-License-Identifier: MIT

// Package randtoken makes random strings from the system source, for states,
// nonces, verifiers and identifiers.
package randtoken

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
)

// Base64URL returns n random bytes as unpadded base64url text.
func Base64URL(n int) (string, error) {
	b, err := bytes(n)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Hex returns n random bytes as lower-case hex text.
func Hex(n int) (string, error) {
	b, err := bytes(n)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func bytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}
