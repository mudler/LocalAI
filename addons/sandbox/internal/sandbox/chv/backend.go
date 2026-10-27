//go:build linux

package chv

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
)

var _ sandbox.Backend = (*Backend)(nil)

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Create materializes the view, starts a guest and waits for its agent.
func (b *Backend) Create(ctx context.Context, spec sandbox.Spec) (sandbox.Sandbox, error) {
	spec = spec.WithDefaults()
	if err := spec.Check(); err != nil {
		return nil, err
	}
	img, err := b.image(ctx, spec.Image)
	if err != nil {
		return nil, err
	}
	id := newID()
	dir := filepath.Join(b.opts.StateDir, id)
	if err := os.Mkdir(dir, 0o700); err != nil {
		return nil, err
	}
	s := &sbx{b: b, id: id, dir: dir, img: img, env: spec.Env}
	fail := func(err error) (sandbox.Sandbox, error) {
		s.ports.close()
		_ = os.RemoveAll(dir)
		return nil, err
	}
	base, err := spec.Store.Materialize(ctx, spec.View, filepath.Join(dir, "lower"))
	if err != nil {
		return fail(err)
	}
	if err := b.mkUpper(ctx, filepath.Join(dir, "upper.ext4"), spec.Disk); err != nil {
		return fail(err)
	}
	s.st = state{View: spec.View, Actor: spec.Actor, Base: base, Index: sandbox.UpperIndex{}, Image: img.Ref,
		CPUs: spec.CPUs, Memory: spec.Memory, Disk: spec.Disk, Ports: portList(spec.Ports)}
	if err := s.st.save(dir); err != nil {
		return fail(err)
	}
	if s.ports, err = servePorts(dir, spec.Ports); err != nil {
		return fail(err)
	}
	if err := s.boot(ctx); err != nil {
		return fail(err)
	}
	return s, nil
}
