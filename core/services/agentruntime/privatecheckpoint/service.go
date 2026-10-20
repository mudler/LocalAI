// SPDX-License-Identifier: MIT
package privatecheckpoint

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/mudler/LocalAI/core/services/agentruntime/nibadapter"
	"github.com/mudler/LocalAI/core/services/agentruntime/store"
	"reflect"
	"sort"
	"strings"
	"unicode/utf8"
)

func id(s string) bool {
	return len(s) > 0 && len(s) <= 1024 && utf8.ValidString(s) && strings.TrimSpace(s) != ""
}
func scopeOK(s Scope) bool {
	return id(s.Principal) && id(s.Conversation) && id(s.Turn) && s.Attempt > 0 && s.Epoch > 0
}
func bindingCopy(b Binding) (Binding, error) {
	b.Resources = append([]Resource(nil), b.Resources...)
	if !scopeOK(b.Source) || b.SourceRevision == 0 || b.AuthRevision == 0 || b.TaintRevision == 0 || b.RetentionRevision == 0 || b.ExpiresAt.IsZero() || len(b.Resources) > 256 {
		return Binding{}, ErrDenied
	}
	sort.Slice(b.Resources, func(i, j int) bool { return b.Resources[i].CollectionID < b.Resources[j].CollectionID })
	for i, r := range b.Resources {
		if !id(r.CollectionID) || r.Revision == 0 || (i > 0 && r.CollectionID == b.Resources[i-1].CollectionID) {
			return Binding{}, ErrDenied
		}
	}
	return b, nil
}
func authCopy(r AuthorizationRequest) AuthorizationRequest {
	r.Binding.Resources = append([]Resource(nil), r.Binding.Resources...)
	return r
}
func cleanupBinding(r AuthorizationRequest) CleanupBinding {
	return CleanupBinding{r.Target.Principal, r.Target.Conversation, r.Hash, r.Revision, r.Binding.RetentionRevision}
}

// Each phase rechecks independent host authorities. These checks do not make
// external ACL revocation atomic; host synchronization remains an integration gate.
func (s *Service) check(ctx context.Context, r AuthorizationRequest, cp *store.Checkpoint) error {
	if ctx.Err() != nil || !scopeOK(r.Target) || r.Target.Principal != r.Binding.Source.Principal || r.Target.Conversation != r.Binding.Source.Conversation || r.Lease.Principal != r.Target.Principal || r.Lease.Conversation != r.Target.Conversation || r.Lease.Epoch != r.Target.Epoch || !id(r.Lease.Owner) {
		return ErrDenied
	}
	deadline := func() bool {
		n := s.d.Clock.Now()
		return ctx.Err() == nil && n.Before(r.Binding.ExpiresAt) && n.Before(r.Lease.ExpiresAt)
	}
	if !deadline() {
		return ErrDenied
	}
	// Reader can block or revoke grants; authority is always checked AFTER it.
	snap, e := s.d.Reader.Get(ctx, r.Target.Principal, r.Target.Conversation)
	if e != nil || !deadline() || snap.Deleted || snap.Revision != r.Revision || snap.Running == nil || snap.Running.InputID != r.Target.Turn {
		return ErrDenied
	}
	if cp != nil && (snap.Checkpoint.Version != 1 || !bytes.Equal(snap.Checkpoint.Context, cp.Context) || len(snap.Checkpoint.Artifacts)+len(snap.Checkpoint.Pruning)+len(snap.Checkpoint.TranscriptCursor) != 0) {
		return ErrDenied
	}
	state, e := s.d.Cleanup.State(ctx, cleanupBinding(r))
	if e != nil || !deadline() || state != SealedPresent {
		return ErrDenied
	}
	if s.d.Leases.Check(ctx, r.Lease, s.d.Clock.Now()) != nil || !deadline() {
		return ErrDenied
	}
	if s.d.Retention.Check(ctx, authCopy(r).Binding, s.d.Clock.Now()) != nil || !deadline() {
		return ErrDenied
	}
	a, e := s.d.Authorizer.Check(ctx, authCopy(r))
	if e != nil || !deadline() || !s.d.Clock.Now().Before(a.ExpiresAt) || !reflect.DeepEqual(r, a.Request) {
		return ErrDenied
	}
	switch r.Operation {
	case Save:
		if !a.IndirectRetain {
			return ErrDenied
		}
	case Restore:
		if !a.ReadUse {
			return ErrDenied
		}
	case Export:
		if !a.ReadUse || !a.Export {
			return ErrDenied
		}
	case Download:
		if !a.ReadUse || !a.Download {
			return ErrDenied
		}
	default:
		return ErrDenied
	}
	return nil
}
func keyScope(r AuthorizationRequest) KeyScope {
	return KeyScope{r.Binding.Source.Principal, r.Binding.Source.Conversation}
}
func (s *Service) key(ctx context.Context, r AuthorizationRequest, v KeyVersion, save bool) ([]byte, error) {
	if ctx.Err() != nil || !id(v.ID) || v.Version == 0 {
		return nil, ErrDenied
	}
	m, e := s.d.Keyring.Lookup(ctx, keyScope(r), v)
	k := bytes.Clone(m.key)
	if ctx.Err() != nil || e != nil || len(k) != 32 || (m.status != KeyActive && (save || m.status != KeyDecryptOnly)) {
		clear(k)
		return nil, ErrDenied
	}
	return k, nil
}
func (s *Service) Seal(ctx context.Context, r SaveRequest, p PrivatePayload) (SealedCheckpoint, error) {
	b, e := bindingCopy(r.Binding)
	if e != nil || !r.Quiescent {
		return SealedCheckpoint{}, ErrDenied
	}
	// Revalidate caller-created or zero payload values and take independent bytes.
	cp, e := nibadapter.DecodeCheckpoint(bytes.Clone(p.native))
	if e != nil {
		return SealedCheckpoint{}, ErrDenied
	}
	owned, e := NewPrivatePayload(cp, p.cursor)
	if e != nil {
		return SealedCheckpoint{}, ErrDenied
	}
	defer clear(owned.native)
	defer clear(owned.cursor)
	ar := AuthorizationRequest{Operation: Save, Target: r.Target, Lease: r.Lease, Revision: r.Revision, Binding: b}
	if s.check(ctx, ar, nil) != nil {
		return SealedCheckpoint{}, ErrDenied
	}
	v, e := s.d.Keyring.Active(ctx, keyScope(ar))
	if ctx.Err() != nil || e != nil {
		return SealedCheckpoint{}, ErrDenied
	}
	k, e := s.key(ctx, ar, v, true)
	if e != nil {
		return SealedCheckpoint{}, ErrDenied
	}
	defer clear(k)
	if s.check(ctx, ar, nil) != nil {
		return SealedCheckpoint{}, ErrDenied
	}
	plain, e := encodePayload(owned)
	if e != nil {
		return SealedCheckpoint{}, ErrDenied
	}
	defer clear(plain)
	h := header{1, "AES-256-GCM", v, nibadapter.CheckpointVersion, b}
	aad := wire(h)
	if ctx.Err() != nil {
		return SealedCheckpoint{}, ErrDenied
	}
	n, c, e := s.d.Encryptor.Seal(bytes.Clone(k), bytes.Clone(plain), bytes.Clone(aad))
	if ctx.Err() != nil || e != nil || len(n) != 12 || len(c) != len(plain)+16 {
		return SealedCheckpoint{}, ErrDenied
	}
	out := wire(envelope{1, h, n, c})
	if len(out) > MaxEnvelope {
		return SealedCheckpoint{}, ErrDenied
	}
	// Fresh key status and authority after crypto before releasing ciphertext.
	fresh, e := s.key(ctx, ar, v, true)
	if e != nil {
		return SealedCheckpoint{}, ErrDenied
	}
	same := bytes.Equal(k, fresh)
	clear(fresh)
	if !same || s.check(ctx, ar, nil) != nil || ctx.Err() != nil {
		return SealedCheckpoint{}, ErrDenied
	}
	return SealedCheckpoint{out}, nil
}
func (s *Service) Restore(ctx context.Context, r ReadRequest, cp store.Checkpoint) (PrivatePayload, error) {
	return s.read(ctx, r, cp, Restore, 0)
}
func (s *Service) Export(ctx context.Context, r ReadRequest, cp store.Checkpoint) (PrivatePayload, error) {
	return s.read(ctx, r, cp, Export, 0)
}
func (s *Service) read(ctx context.Context, r ReadRequest, cp store.Checkpoint, op Operation, artifact int64) (PrivatePayload, error) {
	cp.Context = bytes.Clone(cp.Context)
	if cp.Version != 1 || len(cp.Artifacts)+len(cp.Pruning)+len(cp.TranscriptCursor) != 0 || len(cp.Context) > MaxEnvelope || EnvelopeHash(cp) != r.Hash {
		return PrivatePayload{}, ErrDenied
	}
	var env envelope
	if strict(cp.Context, &env) != nil || env.Version != 1 || env.Header.Version != 1 || env.Header.Algorithm != "AES-256-GCM" || env.Header.Codec != nibadapter.CheckpointVersion || len(env.Nonce) != 12 || len(env.Ciphertext) < 16 || len(env.Ciphertext) > MaxPlaintext+16 {
		return PrivatePayload{}, ErrDenied
	}
	b, e := bindingCopy(env.Header.Binding)
	if e != nil || !reflect.DeepEqual(b, env.Header.Binding) || b.SourceRevision != r.SourceRevision {
		return PrivatePayload{}, ErrDenied
	}
	ar := AuthorizationRequest{op, r.Target, r.Lease, r.Revision, r.Hash, b, artifact}
	if s.check(ctx, ar, &cp) != nil {
		return PrivatePayload{}, ErrDenied
	}
	k, e := s.key(ctx, ar, env.Header.Key, false)
	if e != nil {
		return PrivatePayload{}, ErrDenied
	}
	defer clear(k)
	if s.check(ctx, ar, &cp) != nil {
		return PrivatePayload{}, ErrDenied
	}
	if ctx.Err() != nil {
		return PrivatePayload{}, ErrDenied
	}
	plain, e := s.d.Encryptor.Open(bytes.Clone(k), bytes.Clone(env.Nonce), bytes.Clone(env.Ciphertext), wire(env.Header))
	if ctx.Err() != nil || e != nil || len(plain) > MaxPlaintext {
		clear(plain)
		return PrivatePayload{}, ErrDenied
	}
	defer clear(plain)
	var pw payloadWire
	if strict(plain, &pw) != nil {
		return PrivatePayload{}, ErrDenied
	}
	defer clear(pw.Native)
	defer clear(pw.Cursor)
	native, e := nibadapter.DecodeCheckpoint(pw.Native)
	if e != nil {
		return PrivatePayload{}, ErrDenied
	}
	p, e := NewPrivatePayload(native, pw.Cursor)
	if e != nil {
		return PrivatePayload{}, ErrDenied
	}
	fresh, e := s.key(ctx, ar, env.Header.Key, false)
	same := bytes.Equal(k, fresh)
	clear(fresh)
	if e != nil || !same || s.check(ctx, ar, &cp) != nil || ctx.Err() != nil {
		clear(p.native)
		clear(p.cursor)
		return PrivatePayload{}, ErrDenied
	}
	return p, nil
}
func (s *Service) DownloadArtifact(ctx context.Context, r DownloadRequest, cp store.Checkpoint) (PrivateArtifact, error) {
	if r.ArtifactID <= 0 {
		return PrivateArtifact{}, ErrDenied
	}
	p, e := s.read(ctx, r.ReadRequest, cp, Download, r.ArtifactID)
	if e != nil {
		return PrivateArtifact{}, ErrDenied
	}
	defer clear(p.native)
	defer clear(p.cursor)
	var artifacts []struct {
		ID      int64
		Content string
	}
	if json.Unmarshal(p.NativeCheckpoint().Artifacts, &artifacts) != nil {
		return PrivateArtifact{}, ErrDenied
	}
	for _, a := range artifacts {
		if a.ID == r.ArtifactID && ctx.Err() == nil {
			return PrivateArtifact{[]byte(a.Content)}, nil
		}
	}
	return PrivateArtifact{}, ErrDenied
}
