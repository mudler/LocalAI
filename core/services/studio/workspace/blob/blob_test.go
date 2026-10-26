package blob_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/mudler/LocalAI/core/services/storage"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/workspace/blob"
)

func stores(t *testing.T) map[string]blob.Store {
	t.Helper()
	fs, err := blob.NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	objects, err := storage.NewFilesystemStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return map[string]blob.Store{"fs": fs, "objectstore": blob.NewObjectStore(objects, t.TempDir())}
}

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func TestStores(t *testing.T) {
	ctx := context.Background()
	for name, s := range stores(t) {
		t.Run(name, func(t *testing.T) {
			t.Run("the same bytes are one blob named by their hash", func(t *testing.T) {
				a, size, err := s.Put(ctx, strings.NewReader("hello"), 0)
				if err != nil || a != sum("hello") || size != 5 {
					t.Fatalf("Put = %q, %d, %v", a, size, err)
				}
				b, _, err := s.Put(ctx, strings.NewReader("hello"), 0)
				if err != nil || b != a {
					t.Fatalf("second Put = %q, %v, want the same hash", b, err)
				}
				if ok, err := s.Exists(ctx, a); err != nil || !ok {
					t.Fatalf("Exists = %v, %v", ok, err)
				}
			})
			t.Run("a stored blob reads back and seeks", func(t *testing.T) {
				h, _, _ := s.Put(ctx, strings.NewReader("0123456789"), 0)
				r, err := s.Open(ctx, h)
				if err != nil {
					t.Fatal(err)
				}
				defer r.Close()
				if _, err := r.Seek(4, io.SeekStart); err != nil {
					t.Fatal(err)
				}
				got, _ := io.ReadAll(r)
				if string(got) != "456789" {
					t.Fatalf("after Seek(4) read %q", got)
				}
			})
			t.Run("a reader that fails mid-stream leaves no blob", func(t *testing.T) {
				r := io.MultiReader(strings.NewReader("partial"), iotest.ErrReader(errors.New("cut off")))
				if _, _, err := s.Put(ctx, r, 0); err == nil {
					t.Fatal("Put accepted a cut-off upload")
				}
				if ok, _ := s.Exists(ctx, sum("partial")); ok {
					t.Fatal("a cut-off upload left a blob")
				}
			})
			t.Run("a stream over the limit is refused and leaves nothing", func(t *testing.T) {
				big := bytes.Repeat([]byte("x"), 100)
				_, _, err := s.Put(ctx, bytes.NewReader(big), 10)
				if !errors.Is(err, workspace.ErrTooLarge) {
					t.Fatalf("Put over the limit = %v, want ErrTooLarge", err)
				}
				if ok, _ := s.Exists(ctx, sum(string(big))); ok {
					t.Fatal("an over-limit upload left a blob")
				}
			})
			t.Run("an unknown or malformed hash is not found", func(t *testing.T) {
				for _, h := range []string{sum("never stored"), "../../etc/passwd", "", "zz"} {
					if _, err := s.Open(ctx, h); !errors.Is(err, workspace.ErrNotFound) {
						t.Errorf("Open(%q) = %v, want ErrNotFound", h, err)
					}
					if ok, err := s.Exists(ctx, h); err != nil || ok {
						t.Errorf("Exists(%q) = %v, %v, want false", h, ok, err)
					}
				}
			})
		})
	}
}
