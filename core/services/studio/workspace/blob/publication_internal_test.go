// SPDX-License-Identifier: MIT
package blob

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSharedFilesystemPublicationFailures(t *testing.T) {
	for _, stage := range []string{"write", "file sync", "rename", "directory sync"} {
		t.Run(stage, func(t *testing.T) {
			root := t.TempDir()
			st, err := NewFS(root)
			if err != nil {
				t.Fatal(err)
			}
			s := st.(*fsStore)
			content := "complete"
			seed, err := NewFS(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			sha, _, err := seed.Put(context.Background(), strings.NewReader(content), 0)
			if err != nil {
				t.Fatal(err)
			}
			// Pre-create shard/tmp so directory-sync injection tests post-publication.
			if err = os.MkdirAll(filepath.Dir(shaPath(root, sha)), 0o700); err != nil {
				t.Fatal(err)
			}
			if err = os.Mkdir(filepath.Join(root, "tmp"), 0o700); err != nil {
				t.Fatal(err)
			}
			injected := errors.New("injected " + stage)
			s.fault = func(at string) error {
				if at == stage {
					return injected
				}
				return nil
			}
			_, _, err = s.Put(context.Background(), strings.NewReader(content), 0)
			if !errors.Is(err, injected) {
				t.Fatalf("want injection, got %v", err)
			}
			entries, err := os.ReadDir(filepath.Join(root, "tmp"))
			if err != nil || len(entries) != 0 {
				t.Fatalf("temp leak: %v %v", entries, err)
			}
			s.fault = nil
			f, err := s.Open(context.Background(), sha)
			if stage == "directory sync" {
				if err != nil {
					t.Fatal(err)
				}
				got, _ := io.ReadAll(f)
				f.Close()
				if string(got) != content {
					t.Fatal("partial publication")
				}
			} else if err == nil {
				f.Close()
				t.Fatal("published failed upload")
			}
		})
	}
}

func TestSharedFilesystemSyncsCreatedAncestors(t *testing.T) {
	st, err := NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := st.(*fsStore)
	count := 0
	s.fault = func(at string) error {
		if at == "directory sync" {
			count++
		}
		return nil
	}
	if _, _, err = s.Put(context.Background(), strings.NewReader("new shards"), 0); err != nil {
		t.Fatal(err)
	}
	// Creation of tmp/two shard parents, final/ancestor directories and tmp.
	if count != 7 {
		t.Fatalf("want 7 directory syncs, got %d", count)
	}
}

func TestSharedFilesystemFailedSyncPreservesConcurrentPublication(t *testing.T) {
	root := t.TempDir()
	first, err := NewFS(root)
	if err != nil {
		t.Fatal(err)
	}
	other, err := NewFS(root)
	if err != nil {
		t.Fatal(err)
	}
	content := "concurrent publication"
	sha, _, err := other.Put(context.Background(), strings.NewReader(content), 0)
	if err != nil {
		t.Fatal(err)
	}
	injected := errors.New("post-rename sync")
	first.(*fsStore).fault = func(at string) error {
		if at != "directory sync" {
			return nil
		}
		// Another instance publishes the same hash after this writer's rename.
		if _, _, err := other.Put(context.Background(), strings.NewReader(content), 0); err != nil {
			t.Fatal(err)
		}
		return injected
	}
	if _, _, err = first.Put(context.Background(), strings.NewReader(content), 0); !errors.Is(err, injected) {
		t.Fatalf("want sync error, got %v", err)
	}
	f, err := other.Open(context.Background(), sha)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got, err := io.ReadAll(f)
	if err != nil || string(got) != content {
		t.Fatalf("lost complete publication: %q %v", got, err)
	}
}

type cancelAtEOF struct{ cancel context.CancelFunc }

func (r cancelAtEOF) Read([]byte) (int, error) { r.cancel(); return 0, io.EOF }
func TestSharedFilesystemBoundedUploadCancellation(t *testing.T) {
	s, err := NewFS(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, _, err = s.Put(ctx, cancelAtEOF{cancel}, 128); !errors.Is(err, context.Canceled) {
		t.Fatalf("want canceled bounded ingest, got %v", err)
	}
}
