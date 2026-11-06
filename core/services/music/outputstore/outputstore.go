// SPDX-License-Identifier: MIT
// Package outputstore owns private music artifacts and their durable publication ledger.
package outputstore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/music"
	"github.com/mudler/LocalAI/core/services/storage"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const MaxWAVBytes int64 = 128 << 20
const OutputLifetime = 24 * time.Hour
const TemporaryLifetime = time.Hour
const operationLease = time.Minute

var ErrDenied = errors.New("music storage resource unavailable")
var ErrConfiguration = errors.New("music private durable storage configuration required")

// WAVValidator is a trusted application dependency, never supplied by clients.
// Production wiring MUST validate bounded WAV metadata AND decoded samples
// (including finiteness, duration <=300s and nonempty audio). Tests injecting a
// validator do not establish production WAV/native validation readiness.
type WAVValidator func(context.Context, io.ReadSeeker, int64) error

// SharedReadiness records trusted deployment checks made OUTSIDE this package.
// ObjectStore cannot inspect ACLs or prove database replication. These fields
// are not authorization decisions and must never come from request parameters.
type SharedReadiness struct {
	SharedDatabase  bool
	PrivateS3Bucket bool
}

type Config struct {
	DB          *gorm.DB
	Objects     storage.ObjectStore
	StagingRoot string
	Clock       func() time.Time
	ValidateWAV WAVValidator
	// Nil selects standalone, requiring NewPrivateFilesystem's store. Shared
	// requires an actual S3Store, PostgreSQL, and explicit deployment evidence.
	Shared *SharedReadiness
}

// RequestMetadata contains no source, lyrics, score or worker path.
type RequestMetadata struct {
	ID, Principal, TranscriptionModel, GenerationModel string
	Deadline                                           time.Time
}

type requestRow struct {
	ID                                             string `gorm:"primaryKey;size:256"`
	Principal, TranscriptionModel, GenerationModel string
	Deadline, TemporaryExpiry                      time.Time
	State                                          string `gorm:"index"`
	Fence                                          uint64
}

func (requestRow) TableName() string { return "music_output_requests" }

// artifactRow is a reclamation ledger, not a publication manifest. Reclaimed
// object rows remain deletion tombstones: a remote service may finish a PUT
// after its client process crashes, even after an earlier DELETE succeeded.
type artifactRow struct {
	ID                                   string `gorm:"primaryKey;size:64"`
	RequestID                            string `gorm:"index;size:256"`
	Kind, State, Leaf, ObjectKey, Digest string
	Size                                 int64
	Device, Inode                        uint64
	ExpiresAt, LeaseUntil                time.Time
	Generation                           uint64
	LeaseOwner                           string
	StagingOwner                         string
}

func (artifactRow) TableName() string { return "music_output_artifacts" }

// Manifest is an authorization input, not a grant. A handler must authorize
// owner AND current feature/model access before passing this record to Open.
// Open reloads and compares the entire record and checks expiry again.
type Manifest struct {
	ID                                             string `gorm:"primaryKey;size:64"`
	Principal, TranscriptionModel, GenerationModel string
	ObjectKey                                      string `gorm:"uniqueIndex"`
	Size                                           int64
	ContentType                                    string
	CreatedAt, ExpiresAt                           time.Time
}

func (Manifest) TableName() string { return "music_output_manifests" }

type registration struct{ request, id string }
type waiter struct {
	request  string
	ids      []string
	channels []chan error
	key      string
	all      bool
}
type Store struct {
	cleanupSlots                  chan struct{}
	admitted                      map[string]bool
	reconcileMu                   sync.Mutex
	requestCursor, artifactCursor string
	db                            *gorm.DB
	objects                       storage.ObjectStore
	root                          *os.Root
	clock                         func() time.Time
	validate                      WAVValidator
	issuer                        *music.CapabilityIssuer
	instance                      string
	stagingOwner                  string
	mu                            sync.Mutex
	inputs                        map[music.OwnedInput]registration
	candidates                    map[music.Candidate]registration
	prepared                      map[music.PreparedOutput]registration
	waiters                       []*waiter
}

var _ music.Storage = (*Store)(nil)
var _ music.Publisher = (*Store)(nil)

func nilValue(v any) bool {
	if v == nil {
		return true
	}
	r := reflect.ValueOf(v)
	return (r.Kind() == reflect.Pointer || r.Kind() == reflect.Interface) && r.IsNil()
}

func New(c Config) (*Store, error) {
	if c.DB == nil || nilValue(c.Objects) || c.StagingRoot == "" || c.Clock == nil || c.ValidateWAV == nil {
		return nil, ErrConfiguration
	}
	dialect := c.DB.Dialector.Name()
	if dialect != "sqlite" && dialect != "postgres" {
		return nil, fmt.Errorf("%w: unsupported SQL dialect", ErrConfiguration)
	}
	if c.Shared != nil {
		if _, ok := c.Objects.(*storage.S3Store); !ok || dialect != "postgres" || !c.Shared.SharedDatabase || !c.Shared.PrivateS3Bucket {
			return nil, ErrConfiguration
		}
	} else {
		if _, ok := c.Objects.(interface{ privateFilesystem() }); !ok {
			return nil, fmt.Errorf("%w: standalone requires private filesystem", ErrConfiguration)
		}
	}
	root, err := privateRoot(c.StagingRoot)
	if err != nil {
		return nil, err
	}
	fail := true
	defer func() {
		if fail {
			_ = root.Close()
		}
	}()
	if err = c.DB.AutoMigrate(&requestRow{}, &artifactRow{}, &Manifest{}); err != nil {
		return nil, err
	}
	id, err := randomID()
	if err != nil {
		return nil, err
	}
	// Persistent random namespace distinguishes identical paths/device/inode
	// numbers on different hosts and survives a restart of this exact root.
	namespace, err := stagingNamespace(root)
	if err != nil {
		return nil, err
	}

	s := &Store{cleanupSlots: make(chan struct{}, 64), admitted: map[string]bool{}, stagingOwner: namespace, db: c.DB, objects: c.Objects, root: root, clock: c.Clock, validate: c.ValidateWAV, issuer: music.NewCapabilityIssuer(), instance: id, inputs: map[music.OwnedInput]registration{}, candidates: map[music.Candidate]registration{}, prepared: map[music.PreparedOutput]registration{}}
	fail = false
	return s, nil
}

// Close releases the staging root, not resources. Reconcile must be scheduled
// by application wiring on startup and periodically; Close is not a cleanup ACK.
func (s *Store) Close() error                    { return s.root.Close() }
func (s *Store) Issuer() *music.CapabilityIssuer { return s.issuer }
func randomID() (string, error) {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return hex.EncodeToString(b), nil
}

func (s *Store) RegisterRequest(ctx context.Context, m RequestMetadata) error {
	s.mu.Lock()
	if len(s.admitted) >= 64 || len(s.cleanupSlots) == cap(s.cleanupSlots) {
		s.mu.Unlock()
		return ErrDenied
	}
	if s.admitted[m.ID] {
		s.mu.Unlock()
		return ErrDenied
	}
	s.admitted[m.ID] = true
	s.mu.Unlock()
	success := false
	defer func() {
		if !success {
			s.mu.Lock()
			delete(s.admitted, m.ID)
			s.mu.Unlock()
		}
	}()
	now := s.clock().UTC()
	if m.ID == "" || len(m.ID) > 256 || m.Principal == "" || !m.Deadline.After(now) || m.Deadline.After(now.Add(900*time.Second)) {
		return ErrDenied
	}
	r := requestRow{ID: m.ID, Principal: m.Principal, TranscriptionModel: m.TranscriptionModel, GenerationModel: m.GenerationModel, Deadline: m.Deadline.UTC(), TemporaryExpiry: m.Deadline.UTC().Add(TemporaryLifetime), State: "active"}
	// Never overwrite an existing tombstone or reuse a request ID.
	err := s.db.WithContext(ctx).Create(&r).Error
	success = err == nil
	return err
}

// lockRequest takes a real database write lock, including on SQLite. Every
// publication/reclamation transition obtains this lock before artifact rows.
// In-flight object I/O holds it until that synchronous operation returns; lease
// expiry alone cannot authorize a concurrent reclaimer to race a late PUT.
func (s *Store) lockRequest(tx *gorm.DB, id string, active bool) (requestRow, error) {
	q := tx.Model(&requestRow{}).Where("id = ?", id)
	if active {
		q = q.Where("state = ? AND deadline > ?", "active", s.clock().UTC())
	}
	u := q.UpdateColumn("fence", gorm.Expr("fence + 1"))
	if u.Error != nil {
		return requestRow{}, u.Error
	}
	if u.RowsAffected != 1 {
		return requestRow{}, ErrDenied
	}
	var r requestRow
	err := tx.First(&r, "id = ?", id).Error
	return r, err
}

func (s *Store) tokenCandidate(req string, c music.Candidate) (registration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.candidates[c]
	return r, ok && r.request == req
}
func (s *Store) tokenPrepared(req string, p music.PreparedOutput) (registration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.prepared[p]
	return r, ok && r.request == req
}
func (s *Store) tokenInput(req string, p music.OwnedInput) (registration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.inputs[p]
	return r, ok && r.request == req
}

// ImportCandidate consumes already-fetched local bytes. It performs no network
// fetch and accepts no path or key. The caller/stager owns any reader it passes.
// A durable allocation precedes file creation and capability handoff.
func (s *Store) ImportCandidate(ctx context.Context, req string, src io.Reader) (music.Candidate, error) {
	a, err := s.importBytes(ctx, req, "candidate", src, MaxWAVBytes)
	if err != nil {
		return music.Candidate{}, err
	}
	c := s.issuer.Candidate(req, music.GenerationStage, a.ID)
	s.mu.Lock()
	s.candidates[c] = registration{req, a.ID}
	s.mu.Unlock()
	return c, nil
}
func (s *Store) ImportInput(ctx context.Context, req string, src io.Reader) (music.OwnedInput, error) {
	a, err := s.importBytes(ctx, req, "input", src, 25<<20)
	if err != nil {
		return music.OwnedInput{}, err
	}
	c := s.issuer.Input(req, a.ID)
	s.mu.Lock()
	s.inputs[c] = registration{req, a.ID}
	s.mu.Unlock()
	return c, nil
}

func (s *Store) importBytes(ctx context.Context, req, kind string, src io.Reader, limit int64) (artifactRow, error) {
	s.mu.Lock()
	admitted := s.admitted[req]
	s.mu.Unlock()
	if !admitted || len(s.cleanupSlots) == cap(s.cleanupSlots) {
		return artifactRow{}, ErrDenied
	}
	if nilValue(src) {
		return artifactRow{}, ErrDenied
	}
	id, e := randomID()
	if e != nil {
		return artifactRow{}, e
	}
	a := artifactRow{ID: id, RequestID: req, Kind: kind, State: "allocated", StagingOwner: s.stagingOwner, Leaf: id + "/bytes"}
	e = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		r, err := s.lockRequest(tx, req, true)
		if err != nil {
			return err
		}
		var count int64
		if err = tx.Model(&artifactRow{}).Where("request_id = ?", req).Count(&count).Error; err != nil {
			return err
		}
		if count >= 16 {
			return ErrDenied
		}
		a.ExpiresAt = r.TemporaryExpiry
		return tx.Create(&a).Error
	})
	if e != nil {
		return a, e
	}
	e = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := s.lockRequest(tx, req, true); err != nil {
			return err
		}
		var current artifactRow
		if err := tx.First(&current, "id = ?", id).Error; err != nil {
			return err
		}
		if current.State != "allocated" {
			return ErrDenied
		}
		if err := s.root.Mkdir(id, 0700); err != nil {
			return err
		}
		f, err := s.root.OpenFile(a.Leaf, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		n, err := io.Copy(f, io.LimitReader(&contextReader{ctx, src}, limit+1))
		if err == nil && n > limit {
			err = ErrDenied
		}
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if err != nil {
			return err
		}
		read, err := s.openLocal(a)
		if err != nil {
			return err
		}
		info, err := read.Stat()
		_ = read.Close()
		if err != nil {
			return err
		}
		a.Size = n
		a.Device, a.Inode = fileIdentity(info)
		a.State = "local"
		return tx.Save(&a).Error
	})
	// Failed/partial writes stay in the ledger for explicit cleanup/reclamation.
	return a, e
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.r.Read(p)
}

func (s *Store) FetchValidate(ctx context.Context, req string, c music.Candidate) (music.Candidate, error) {
	reg, ok := s.tokenCandidate(req, c)
	if !ok {
		return music.Candidate{}, ErrDenied
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, e := s.lockRequest(tx, req, true); e != nil {
			return e
		}
		var a artifactRow
		if e := tx.First(&a, "id = ? AND request_id = ?", reg.id, req).Error; e != nil {
			return e
		}
		if a.State != "local" && a.State != "validated" {
			return ErrDenied
		}
		digest, e := s.validateArtifact(ctx, a)
		if e != nil {
			return e
		}
		a.State = "validated"
		a.Digest = digest
		return tx.Save(&a).Error
	})
	if err != nil {
		return music.Candidate{}, err
	}
	return c, nil
}

func (s *Store) Prepare(ctx context.Context, req string, c music.Candidate) (music.PreparedOutput, error) {
	if len(s.cleanupSlots) == cap(s.cleanupSlots) {
		return music.PreparedOutput{}, ErrDenied
	}
	reg, ok := s.tokenCandidate(req, c)
	if !ok {
		return music.PreparedOutput{}, ErrDenied
	}
	var out artifactRow
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, e := s.lockRequest(tx, req, true); e != nil {
			return e
		}
		var a artifactRow
		if e := tx.First(&a, "id = ? AND request_id = ?", reg.id, req).Error; e != nil {
			return e
		}
		if a.State != "validated" {
			return ErrDenied
		}
		digest, e := s.validateArtifact(ctx, a)
		if e != nil {
			return e
		}
		if digest != a.Digest {
			return ErrDenied
		}
		id, e := randomID()
		if e != nil {
			return e
		}
		out = artifactRow{ID: id, RequestID: req, Kind: "object", State: "pending", ObjectKey: "music/" + id, Leaf: a.Leaf, Size: a.Size, Digest: a.Digest, Device: a.Device, Inode: a.Inode, ExpiresAt: a.ExpiresAt, Generation: 1, LeaseOwner: s.instance, LeaseUntil: s.clock().UTC().Add(operationLease)}
		var count int64
		if e = tx.Model(&artifactRow{}).Where("request_id = ?", req).Count(&count).Error; e != nil {
			return e
		}
		if count >= 16 {
			return ErrDenied
		}
		return tx.Create(&out).Error
	})
	if err != nil {
		return music.PreparedOutput{}, err
	}
	// The pending object intent above commits BEFORE any Put. The second
	// transaction fences upload against cancellation and reclamation.
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, e := s.lockRequest(tx, req, true); e != nil {
			return e
		}
		var a artifactRow
		if e := tx.First(&a, "id = ?", out.ID).Error; e != nil {
			return e
		}
		if a.State != "pending" || a.Generation != out.Generation || a.LeaseOwner != s.instance || !a.LeaseUntil.After(s.clock().UTC()) {
			return ErrDenied
		}
		digest, e := s.validateArtifact(ctx, a)
		if e != nil {
			return e
		}
		if digest != a.Digest {
			return ErrDenied
		}
		f, e := s.openLocal(a)
		if e != nil {
			return e
		}
		defer f.Close()
		// No async upload is permitted by the ObjectStore contract.
		if e = s.objects.Put(ctx, a.ObjectKey, &contextReader{ctx, io.LimitReader(f, MaxWAVBytes+1)}); e != nil {
			return e
		}
		if e = ctx.Err(); e != nil {
			return e
		}
		return tx.Model(&artifactRow{}).Where("id = ? AND generation = ? AND state = ?", a.ID, a.Generation, "pending").Updates(map[string]any{"state": "prepared", "lease_owner": "", "lease_until": time.Time{}}).Error
	})
	if err != nil {
		return music.PreparedOutput{}, err
	}
	p := s.issuer.Prepared(req, out.ID)
	s.mu.Lock()
	s.prepared[p] = registration{req, out.ID}
	s.mu.Unlock()
	return p, nil
}

func (s *Store) Commit(ctx context.Context, req string, p music.PreparedOutput) (string, error) {
	reg, ok := s.tokenPrepared(req, p)
	if !ok {
		return "", ErrDenied
	}
	id, e := randomID()
	if e != nil {
		return "", e
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		r, e := s.lockRequest(tx, req, true)
		if e != nil {
			return e
		}
		var a artifactRow
		if e = tx.First(&a, "id = ? AND request_id = ?", reg.id, req).Error; e != nil {
			return e
		}
		if a.State != "prepared" {
			return ErrDenied
		}
		meta, e := s.objects.Head(ctx, a.ObjectKey)
		if e != nil {
			return e
		}
		if meta == nil || meta.Size != a.Size {
			return ErrDenied
		}
		now := s.clock().UTC()
		m := Manifest{ID: id, Principal: r.Principal, TranscriptionModel: r.TranscriptionModel, GenerationModel: r.GenerationModel, ObjectKey: a.ObjectKey, Size: a.Size, ContentType: "audio/wav", CreatedAt: now, ExpiresAt: now.Add(OutputLifetime)}
		if e = tx.Create(&m).Error; e != nil {
			return e
		}
		if e = tx.Model(&artifactRow{}).Where("id = ?", a.ID).Updates(map[string]any{"state": "committed", "expires_at": m.ExpiresAt, "generation": gorm.Expr("generation + 1")}).Error; e != nil {
			return e
		}
		return tx.Model(&requestRow{}).Where("id = ? AND state = ?", req, "active").Update("state", "committed").Error
	})
	if err != nil {
		return "", err
	}
	// Success transfers the object only. Local bytes remain durably registered
	// until actually removed; failure here does not undo a committed publication.
	_ = s.cleanCommittedLocals(ctx, req)
	s.retireResolved(ctx, req)
	return id, nil
}

func validID(id string) bool {
	if len(id) != 64 {
		return false
	}
	_, e := hex.DecodeString(id)
	return e == nil
}
func (s *Store) Lookup(ctx context.Context, id string) (Manifest, error) {
	if !validID(id) {
		return Manifest{}, ErrDenied
	}
	var m Manifest
	e := s.db.WithContext(ctx).Where("id = ? AND expires_at > ?", id, s.clock().UTC()).First(&m).Error
	if e != nil {
		return Manifest{}, ErrDenied
	}
	return m, nil
}
func (s *Store) Open(ctx context.Context, authorized Manifest) (io.ReadCloser, error) {
	m, e := s.Lookup(ctx, authorized.ID)
	if e != nil || m != authorized {
		return nil, ErrDenied
	}
	r, e := s.objects.Get(ctx, m.ObjectKey)
	if e != nil {
		return nil, e
	}
	// Recheck after potentially slow storage open; no stream is handed out at or
	// after expiry. Existing streams are not revocable by this storage API.
	again, e := s.Lookup(ctx, m.ID)
	if e != nil || again != m {
		_ = r.Close()
		return nil, ErrDenied
	}
	return &boundedObject{ReadCloser: r, remaining: m.Size}, nil
}

type boundedObject struct {
	io.ReadCloser
	remaining int64
}

func (r *boundedObject) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, e := r.ReadCloser.Read(p)
	r.remaining -= int64(n)
	if e == io.EOF && r.remaining > 0 {
		e = io.ErrUnexpectedEOF
	}
	return n, e
}

// Revoke is terminal only for active requests. A successful publication survives
// late cancellation and disconnects. Unknown IDs receive durable tombstones.
func (s *Store) revoke(ctx context.Context, req string) error {
	if req == "" || len(req) > 256 {
		return ErrDenied
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tomb := requestRow{ID: req, State: "revoked"}
		if e := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&tomb).Error; e != nil {
			return e
		}
		r, e := s.lockRequest(tx, req, false)
		if e != nil {
			return e
		}
		if r.State == "committed" {
			return nil
		}
		return tx.Model(&requestRow{}).Where("id = ? AND state = ?", req, "active").Update("state", "revoked").Error
	})
}

// Staging root must be dedicated and outside every static/public mount. This
// package can enforce containment/permissions, not inspect HTTP mount wiring.
func privateRoot(path string) (*os.Root, error) {
	if !filepath.IsAbs(path) {
		return nil, ErrConfiguration
	}
	if e := os.MkdirAll(path, 0700); e != nil {
		return nil, e
	}
	st, e := os.Lstat(path)
	if e != nil {
		return nil, e
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 || st.Mode().Perm()&0077 != 0 {
		return nil, ErrConfiguration
	}
	return os.OpenRoot(path)
}

func stagingNamespace(root *os.Root) (string, error) {
	id, e := randomID()
	if e != nil {
		return "", e
	}
	f, e := root.OpenFile(".music-staging-namespace", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e == nil {
		_, writeErr := io.WriteString(f, id)
		if writeErr == nil {
			writeErr = f.Sync()
		}
		if e = errors.Join(writeErr, f.Close()); e != nil {
			return "", e
		}
	} else if !os.IsExist(e) {
		return "", e
	}
	f, e = openRegular(root, ".music-staging-namespace", 0, 0)
	if e != nil {
		return "", e
	}
	defer f.Close()
	data, e := io.ReadAll(io.LimitReader(f, 65))
	if e != nil {
		return "", e
	}
	if !validID(string(data)) {
		return "", ErrConfiguration
	}
	return string(data), nil
}
