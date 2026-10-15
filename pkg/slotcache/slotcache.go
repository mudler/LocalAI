// Package slotcache holds the checks for the slot prompt-cache save and
// restore API. The backend issues an opaque ID on save and the client sends it
// back on restore. This package decides whether an ID has the right shape
// before a request reaches a backend.
package slotcache

import (
	"errors"
	"strings"
)

// OptionName is the llama-cpp model option that sets the save directory.
const OptionName = "slot_save_path"

// idLen is the length of an ID: 128 random bits as lowercase hex.
const idLen = 32

// ErrInvalidID is returned for an ID that is not 32 lowercase hex characters.
var ErrInvalidID = errors.New("invalid id: expected 32 lowercase hex characters")

// ValidateID accepts exactly 32 lowercase hex characters, the format the
// backend issues. Anything else cannot be an ID the backend issued, and cannot
// contain a separator, a dot or a NUL byte. The backend checks again and also
// requires the ID to be in the model's manifest.
func ValidateID(id string) error {
	if len(id) != idLen {
		return ErrInvalidID
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return ErrInvalidID
		}
	}
	return nil
}

// DirFromOptions returns the slot_save_path value from a model's options, or
// "" when it is not set. Options have the form name:value. Everything after
// the first colon is the value, so a path that contains a colon is kept whole.
func DirFromOptions(options []string) string {
	dir := ""
	for _, o := range options {
		if k, v, ok := strings.Cut(o, ":"); ok && k == OptionName {
			dir = strings.TrimSpace(v)
		}
	}
	return dir
}
