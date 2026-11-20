// SPDX-License-Identifier: MIT
package assetpipeline

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

const MaxAssetBytes int64 = 512 << 20

type ArtifactMIME string

const (
	MIMEPNG  ArtifactMIME = "image/png"
	MIMEJPEG ArtifactMIME = "image/jpeg"
	MIMEGLB  ArtifactMIME = "model/gltf-binary"
)

func (m ArtifactMIME) Valid() bool {
	return m == MIMEPNG || m == MIMEJPEG || m == MIMEGLB
}

type Artifact struct {
	ID     string       `json:"id"`
	Name   string       `json:"name"`
	MIME   ArtifactMIME `json:"mime"`
	Bytes  int64        `json:"bytes"`
	SHA256 string       `json:"sha256"`
}

// PutArtifact streams bytes to owner storage before publishing their reference.
// A branch retains references to these immutable files; temporary server URLs
// and browser-local blobs are never the only copy of a recorded input.
func (s *Store) PutArtifact(ctx context.Context, resource, name string, mime ArtifactMIME, input io.Reader) (Artifact, error) {
	return s.putArtifact(ctx, resource, name, mime, input, "")
}

func (s *Store) putArtifact(ctx context.Context, resource, name string, mime ArtifactMIME, input io.Reader, upload string) (Artifact, error) {
	if _, err := s.Read(ctx, resource); err != nil {
		return Artifact{}, err
	}
	if !mime.Valid() {
		return Artifact{}, errors.New("unsupported artifact type")
	}

	name = filepath.Base(name)
	if len(name) > 160 {
		return Artifact{}, errors.New("artifact name too long")
	}

	f, err := os.CreateTemp(filepath.Join(s.dir, "artifacts"), ".upload-")
	if err != nil {
		return Artifact{}, err
	}

	defer func() {
		_ = f.Close()
		_ = os.Remove(f.Name())
	}()

	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, hash), io.LimitReader(input, MaxAssetBytes+1))
	if err != nil {
		return Artifact{}, err
	}
	if n == 0 || n > MaxAssetBytes {
		return Artifact{}, errors.New("asset must contain 1 byte to 512 MiB")
	}

	if err := f.Sync(); err != nil {
		return Artifact{}, err
	}

	if err := f.Close(); err != nil {
		return Artifact{}, err
	}

	a := Artifact{
		ID:     uuid.NewString(),
		Name:   name,
		MIME:   mime,
		Bytes:  n,
		SHA256: hex.EncodeToString(hash.Sum(nil)),
	}
	path := filepath.Join(s.dir, "artifacts", a.ID)
	if err := os.Rename(f.Name(), path); err != nil {
		return Artifact{}, err
	}

	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return Artifact{}, err
	}

	err = d.Sync()
	_ = d.Close()
	if err != nil {
		return Artifact{}, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Artifact{}, err
	}

	defer func() { _ = tx.Rollback() }()

	var used int64
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(SUM(bytes),0) FROM artifacts WHERE resource = ?", resource).Scan(&used); err != nil {
		return Artifact{}, err
	}
	if used+n > 4<<30 {
		_ = os.Remove(path)
		return Artifact{}, errors.New("pipeline artifact quota of 4 GiB exceeded")
	}

	_, err = tx.ExecContext(ctx, "INSERT INTO artifacts(resource,id,name,mime,bytes,sha) VALUES(?,?,?,?,?,?)", resource, a.ID, a.Name, a.MIME, a.Bytes, a.SHA256)
	if err != nil {
		return Artifact{}, err
	}
	if upload != "" {
		if _, err := tx.ExecContext(ctx, "UPDATE uploads SET result=? WHERE resource=? AND id=?", a.ID, resource, upload); err != nil {
			return Artifact{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return Artifact{}, err
	}

	return a, nil
}

func (s *Store) Artifact(ctx context.Context, resource, id string) (Artifact, error) {
	if _, err := uuid.Parse(id); err != nil {
		return Artifact{}, ErrNotFound
	}

	var a Artifact
	err := s.db.QueryRowContext(ctx, "SELECT id,name,mime,bytes,sha FROM artifacts WHERE resource = ? AND id = ?", resource, id).Scan(&a.ID, &a.Name, &a.MIME, &a.Bytes, &a.SHA256)
	if errors.Is(err, sql.ErrNoRows) {
		return Artifact{}, ErrNotFound
	}

	return a, err
}

func (s *Store) OpenArtifact(ctx context.Context, resource, id string) (*os.File, Artifact, error) {
	a, err := s.Artifact(ctx, resource, id)
	if err != nil {
		return nil, a, err
	}

	f, err := os.Open(filepath.Join(s.dir, "artifacts", a.ID))
	return f, a, err
}
