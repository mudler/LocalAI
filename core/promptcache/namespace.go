// Package promptcache carries the namespace of the disk prompt cache of a
// request and derives the salt the backend gets from it.
//
// The llama.cpp backend can keep the KV state of a long prompt on disk, so a
// later request with the same prefix does not compute it again. Such a file
// holds the prompt tokens in clear, so the entries of one user must never be
// read by another. The backend mixes the salt into the key of every entry and
// into the name of its file: two namespaces never share an entry.
//
// The namespace comes from trusted state only (the authenticated principal of
// the request), never from request Metadata, which is the caller's to write.
package promptcache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
)

// Namespaces. A user (session or user API key) is "user:<id>", a paired
// client key is "client:<key id>", and a request with no principal (auth off)
// is Local. Shared is the namespace every request uses when the
// administrator turns on the shared cache: its salt is empty.
const (
	Local  = "local"
	Shared = ""

	userPrefix   = "user:"
	clientPrefix = "client:"

	// LegacyAPIKeyUserID is the ID of the synthetic user of a legacy API key
	// (an API key from the environment).
	LegacyAPIKeyUserID = "legacy-api-key"

	// SharedNSID is the namespace id of the shared namespace.
	SharedNSID = "0000000000000000"

	saltDomain = "localai/prompt-cache/v1\x00"
)

// UserNamespace is the namespace of a user.
func UserNamespace(userID string) string { return userPrefix + userID }

// ClientNamespace is the namespace of a paired client key.
func ClientNamespace(keyID string) string { return clientPrefix + keyID }

type ctxKey struct{}

// WithNamespace returns ctx carrying the namespace ns.
func WithNamespace(ctx context.Context, ns string) context.Context {
	return context.WithValue(ctx, ctxKey{}, ns)
}

// NamespaceFrom returns the namespace ctx carries, Local when none.
func NamespaceFrom(ctx context.Context) string {
	if ctx == nil {
		return Local
	}
	if ns, ok := ctx.Value(ctxKey{}).(string); ok && ns != "" {
		return ns
	}
	return Local
}

// Salt is the salt of a namespace: the lowercase hex of
// SHA-256("localai/prompt-cache/v1\x00" + ns), 64 characters. The shared
// namespace has the empty salt.
func Salt(ns string) string {
	if ns == Shared {
		return ""
	}
	sum := sha256.Sum256([]byte(saltDomain + ns))
	return hex.EncodeToString(sum[:])
}

// NSID is the namespace id that names the files of a namespace on disk: the
// first 16 hex characters of the salt, or SharedNSID for the empty salt.
func NSID(salt string) string {
	if len(salt) < 16 {
		return SharedNSID
	}
	return salt[:16]
}

// ModelID is the model id that names the directory of a model on disk: the
// first 16 hex characters of SHA-256 of the model string the backend loads
// (ModelOptions.Model, the model file of the configuration).
func ModelID(model string) string {
	sum := sha256.Sum256([]byte(model))
	return hex.EncodeToString(sum[:])[:16]
}
