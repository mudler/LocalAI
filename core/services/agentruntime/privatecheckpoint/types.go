// SPDX-License-Identifier: MIT
// Package privatecheckpoint is an isolated private codec, not a production key
// provider or atomic ACL/lease/persistence boundary. Host adapters must supply
// complete provenance, current authority, synchronized revocation, key lifecycle
// and durable scoped cleanup CAS. Seal never commits a store transaction.
package privatecheckpoint

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/mudler/LocalAI/core/services/agentruntime/nibadapter"
	"github.com/mudler/LocalAI/core/services/agentruntime/ownership"
	"github.com/mudler/LocalAI/core/services/agentruntime/store"
	"reflect"
	"time"
)

var ErrDenied = errors.New("private checkpoint unavailable")

const (
	MaxPlaintext = 16 << 20
	MaxEnvelope  = 24 << 20
)

type Scope struct {
	Principal, Conversation, Turn string
	Attempt, Epoch                uint64
}
type Resource struct {
	CollectionID string
	Revision     uint64
}
type Binding struct {
	Source                                         Scope
	SourceRevision                                 uint64
	Resources                                      []Resource
	AuthRevision, TaintRevision, RetentionRevision uint64
	ExpiresAt                                      time.Time
}
type Operation string

const (
	Save     Operation = "save"
	Restore  Operation = "restore"
	Export   Operation = "export"
	Download Operation = "download"
	Delete   Operation = "delete"
	Cleanup  Operation = "cleanup"
)

type SaveRequest struct {
	Target    Scope
	Lease     ownership.Lease
	Revision  uint64
	Binding   Binding
	Quiescent bool
}
type ReadRequest struct {
	Target         Scope
	Lease          ownership.Lease
	Revision       uint64
	Hash           [32]byte
	SourceRevision uint64
}
type DownloadRequest struct {
	ReadRequest
	ArtifactID int64
}
type AuthorizationRequest struct {
	Operation  Operation
	Target     Scope
	Lease      ownership.Lease
	Revision   uint64
	Hash       [32]byte
	Binding    Binding
	ArtifactID int64
}

// Authorization must echo the exact request. Flags attest separate grants for
// ALL manifest resources and the explicit old-source/current-target relation.
type Authorization struct {
	Request                                            AuthorizationRequest
	ReadUse, IndirectRetain, Export, Download, Cleanup bool
	ExpiresAt                                          time.Time
}
type KeyScope struct{ Principal, Conversation string }
type KeyVersion struct {
	ID      string
	Version uint64
}
type KeyStatus uint8

const (
	KeyActive KeyStatus = iota + 1
	KeyDecryptOnly
	KeyRevoked
	KeyDestroyed
)

type KeyMaterial struct {
	key    []byte
	status KeyStatus
}

func NewKeyMaterial(k []byte, s KeyStatus) KeyMaterial { return KeyMaterial{bytes.Clone(k), s} }

type Clock interface{ Now() time.Time }
type Authorizer interface {
	Check(context.Context, AuthorizationRequest) (Authorization, error)
}
type Reader interface {
	Get(context.Context, string, string) (store.Snapshot, error)
}
type Leases interface {
	Check(context.Context, ownership.Lease, time.Time) error
}
type Keyring interface {
	Active(context.Context, KeyScope) (KeyVersion, error)
	Lookup(context.Context, KeyScope, KeyVersion) (KeyMaterial, error)
}
type Encryptor interface {
	Seal(key, plaintext, aad []byte) (nonce, ciphertext []byte, err error)
	Open(key, nonce, ciphertext, aad []byte) ([]byte, error)
}
type RetentionPolicy interface {
	Check(context.Context, Binding, time.Time) error
}
type CleanupBinding struct {
	Principal, Conversation  string
	Hash                     [32]byte
	Revision, PolicyRevision uint64
}

// Implementations must CAS ALL fields, tombstone durably before purge, deny
// stale bindings, and make retries idempotent. No production interpreter exists
// here; WAL, backups and external ciphertext copies are not erased by this API.
type CleanupStore interface {
	State(context.Context, CleanupBinding) (CleanupState, error)
	Tombstone(context.Context, CleanupBinding) error
	Purge(context.Context, CleanupBinding) error
}
type Dependencies struct {
	Clock      Clock
	Authorizer Authorizer
	Reader     Reader
	Leases     Leases
	Keyring    Keyring
	Encryptor  Encryptor
	Retention  RetentionPolicy
	Cleanup    CleanupStore
}
type Service struct{ d Dependencies }

func New(d Dependencies) (*Service, error) {
	for _, v := range []any{d.Clock, d.Authorizer, d.Reader, d.Leases, d.Keyring, d.Encryptor, d.Retention, d.Cleanup} {
		if v == nil {
			return nil, ErrDenied
		}
		r := reflect.ValueOf(v)
		switch r.Kind() {
		case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func:
			if r.IsNil() {
				return nil, ErrDenied
			}
		}
	}
	return &Service{d}, nil
}

type PrivatePayload struct{ native, cursor []byte }
type payloadWire struct{ Native, Cursor []byte }

func NewPrivatePayload(cp nibadapter.Checkpoint, cursor []byte) (PrivatePayload, error) {
	b, e := nibadapter.EncodeCheckpoint(cp)
	if e != nil || len(b)+len(cursor) > MaxPlaintext {
		clear(b)
		return PrivatePayload{}, ErrDenied
	}
	return PrivatePayload{b, bytes.Clone(cursor)}, nil
}
func (p PrivatePayload) NativeCheckpoint() nibadapter.Checkpoint {
	cp, _ := nibadapter.DecodeCheckpoint(p.native)
	return cp
}
func (p PrivatePayload) Cursor() []byte { return bytes.Clone(p.cursor) }

type PrivateArtifact struct{ body []byte }

func (p PrivateArtifact) Bytes() []byte { return bytes.Clone(p.body) }

type SealedCheckpoint struct{ data []byte }

func (p SealedCheckpoint) StoreCheckpoint() store.Checkpoint {
	return store.Checkpoint{Version: 1, Context: bytes.Clone(p.data)}
}
func encodePayload(p PrivatePayload) ([]byte, error) {
	b, e := json.Marshal(payloadWire{p.native, p.cursor})
	if e != nil || len(b) > MaxPlaintext {
		clear(b)
		return nil, ErrDenied
	}
	return b, nil
}
