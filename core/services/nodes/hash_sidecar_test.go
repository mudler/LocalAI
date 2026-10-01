package nodes

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHashSidecarCannotEscapeFileDirectory(t *testing.T) {
	for _, mode := range []string{"server", "stager"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "model.bin")
			outside := filepath.Join(t.TempDir(), "outside")
			content := []byte("model contents")
			forged := strings.Repeat("a", 64)
			if err := os.WriteFile(path, content, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(outside, []byte(forged), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, path+hashSidecarSuffix); err != nil {
				t.Fatal(err)
			}
			var got string
			var err error
			if mode == "server" {
				got, err = computeAndCacheHash(path)
			} else {
				got, err = hashLocalCached(context.Background(), path)
			}
			if err != nil {
				t.Fatal(err)
			}
			if got == forged {
				t.Fatal("trusted hash from sidecar outside the model directory")
			}
			after, err := os.ReadFile(outside)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != forged {
				t.Fatal("overwrote file outside the model directory")
			}
		})
	}
}

func TestHashSidecarWriteDoesNotFollowSymlinks(t *testing.T) {
	for _, external := range []bool{false, true} {
		dir := t.TempDir()
		targetDir := dir
		if external {
			targetDir = t.TempDir()
		}
		target := filepath.Join(targetDir, "original")
		path := filepath.Join(dir, "model.bin.sha256")
		if err := os.WriteFile(target, []byte("do not overwrite"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
		if _, err := readHashSidecar(path); err == nil {
			t.Fatal("read symlinked sidecar")
		}
		hash := strings.Repeat("b", 64)
		if err := writeHashSidecar(path, hash); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(target)
		if err != nil || string(data) != "do not overwrite" {
			t.Fatalf("target changed: %q, %v", data, err)
		}
		data, err = readHashSidecar(path)
		if err != nil || string(data) != hash {
			t.Fatalf("incorrect sidecar: %q, %v", data, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("sidecar permissions: %v", info.Mode())
		}
	}
}
