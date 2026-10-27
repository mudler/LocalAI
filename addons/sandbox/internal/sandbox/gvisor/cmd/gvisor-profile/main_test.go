package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfileNamesTheBinaryByItsAbsolutePath(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runsc := filepath.Join(dir, "runsc")
	if err := os.WriteFile(runsc, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := profile(runsc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `"`+runsc+`" flags=(unconfined)`) || !strings.Contains(got, "userns,") {
		t.Fatalf("profile:\n%s", got)
	}
}

func TestProfileRefusesWhatItCannotName(t *testing.T) {
	dir := t.TempDir()
	if _, err := profile(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("a missing file was accepted")
	}
	if _, err := profile(dir); err == nil {
		t.Fatal("a folder was accepted")
	}
	odd := filepath.Join(dir, `a"b {`)
	if err := os.WriteFile(odd, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := profile(odd); err == nil {
		t.Fatal("a path with a quote was accepted")
	}
}

// New resolves symlinks before it checks the host, and AppArmor attaches a
// profile by the real path, so the profile must name the file behind a link.
func TestProfileNamesTheFileBehindASymlink(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(dir, "runsc-real")
	link := filepath.Join(dir, "runsc")
	if err := os.WriteFile(real, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	got, err := profile(link)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `"`+real+`" flags=(unconfined)`) || strings.Contains(got, `"`+link+`"`) {
		t.Fatalf("profile:\n%s", got)
	}
}
