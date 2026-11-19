//go:build !windows

package fsperm

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return st.Mode().Perm()
}

func TestRestrictRemovesGroupAndOtherBits(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Restrict(file); err != nil {
		t.Fatal(err)
	}
	if got := mode(t, file); got != 0o700 {
		t.Fatalf("file mode = %04o, want 0700", got)
	}
	sub := filepath.Join(dir, "d")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Restrict(sub); err != nil {
		t.Fatal(err)
	}
	if got := mode(t, sub); got != 0o700 {
		t.Fatalf("folder mode = %04o, want 0700", got)
	}
	if err := Restrict(filepath.Join(dir, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Restrict(missing) = %v, want not exist", err)
	}
}

func TestCheck(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "key")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Check(file); err != nil {
		t.Fatalf("Check(0600) = %v", err)
	}
	if err := os.Chmod(file, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := Check(file); !errors.Is(err, ErrNotPrivate) {
		t.Fatalf("Check(0640) = %v, want ErrNotPrivate", err)
	}
	if err := Check(filepath.Join(dir, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Check(missing) = %v, want not exist", err)
	}
}

func TestCheckOrRestrictKeepsTheModeOnUnix(t *testing.T) {
	file := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, 0o644); err != nil {
		t.Fatal(err)
	}
	restricted, err := CheckOrRestrict(file)
	if restricted || !errors.Is(err, ErrNotPrivate) {
		t.Fatalf("CheckOrRestrict = %v, %v; want false, ErrNotPrivate", restricted, err)
	}
	if got := mode(t, file); got != 0o644 {
		t.Fatalf("mode changed to %04o", got)
	}
	if err := os.Chmod(file, 0o600); err != nil {
		t.Fatal(err)
	}
	if restricted, err := CheckOrRestrict(file); restricted || err != nil {
		t.Fatalf("CheckOrRestrict(0600) = %v, %v", restricted, err)
	}
}

func TestWriteFileAndMkdirAll(t *testing.T) {
	dir := t.TempDir()
	priv := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(priv, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(priv, 0o755); err != nil {
		t.Fatal(err)
	}
	// MkdirAll closes a folder that already exists.
	if err := MkdirAll(priv); err != nil {
		t.Fatal(err)
	}
	if got := mode(t, priv); got != 0o700 {
		t.Fatalf("folder mode = %04o, want 0700", got)
	}
	secret := filepath.Join(priv, "secret")
	if err := WriteFile(secret, []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := mode(t, secret); got != 0o600 {
		t.Fatalf("secret mode = %04o, want 0600", got)
	}
	public := filepath.Join(priv, "public")
	if err := WriteFile(public, []byte("p"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(public, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(public, []byte("p"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := mode(t, public); got != 0o644 {
		t.Fatalf("a public file was restricted: %04o", got)
	}
	if err := WriteFile(filepath.Join(dir, "no", "such", "dir"), nil, 0o600); err == nil {
		t.Fatal("WriteFile into a missing folder succeeded")
	}
}

func TestIsExecutable(t *testing.T) {
	cases := []struct {
		goos string
		path string
		mode os.FileMode
		want bool
	}{
		{"linux", "run.sh", 0o755, true},
		{"linux", "run.sh", 0o100, true},
		{"linux", "run.sh", 0o644, false},
		{"linux", "llama.exe", 0o644, false},
		{"linux", "bin", os.ModeDir | 0o755, false},
		{"windows", `C:\b\llama-cpp.EXE`, 0o666, true},
		{"windows", "run.ps1", 0o666, true},
		{"windows", "run.cmd", 0o444, true},
		{"windows", "run.bat", 0o666, true},
		{"windows", "run.sh", 0o777, false},
		{"windows", "model.gguf", 0o666, false},
		{"windows", "noext", 0o777, false},
		{"windows", "dir.exe", os.ModeDir | 0o777, false},
	}
	for _, c := range cases {
		if got := isExecutable(c.goos, c.path, c.mode); got != c.want {
			t.Errorf("isExecutable(%s, %s, %v) = %v, want %v", c.goos, c.path, c.mode, got, c.want)
		}
	}
	if !IsExecutable("x", 0o700) || IsExecutable("x.exe", 0o600) {
		t.Error("IsExecutable does not follow the Unix rule")
	}
}

func TestRequirePrivateChecksTheModeTheCallerHolds(t *testing.T) {
	file := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	restricted, err := RequirePrivate(file, info)
	if restricted || !errors.Is(err, ErrNotPrivate) {
		t.Fatalf("RequirePrivate(0644) = %v, %v; want false, ErrNotPrivate", restricted, err)
	}
	if got := mode(t, file); got != 0o644 {
		t.Fatalf("mode changed to %04o; Unix must keep the mode of the owner", got)
	}

	// The check uses the mode the caller read, not the path, so a file that
	// changes after the caller opened it does not change the answer.
	if err := os.Chmod(file, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RequirePrivate(file, info); !errors.Is(err, ErrNotPrivate) {
		t.Fatalf("RequirePrivate read the path again: %v", err)
	}
	info, err = os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if restricted, err := RequirePrivate(file, info); restricted || err != nil {
		t.Fatalf("RequirePrivate(0600) = %v, %v", restricted, err)
	}
}
