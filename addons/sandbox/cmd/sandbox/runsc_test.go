// SPDX-License-Identifier: MIT
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/addons/sandbox/internal/sandbox"
)

func pack(t *testing.T, runsc string) (src string, pin string) {
	t.Helper()
	src = t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "runsc"), []byte(runsc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "gvisor-bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"gvisor_sentry", "checkpointgofer"} {
		if err := os.WriteFile(filepath.Join(src, "gvisor-bin", n), []byte(n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sum := sha256.Sum256([]byte(runsc))
	return src, hex.EncodeToString(sum[:])
}

func TestAPinnedRunscIsCopiedAsExecutableWithItsHelpers(t *testing.T) {
	src, pin := pack(t, "the real runsc")
	dst := filepath.Join(t.TempDir(), "runsc")
	got, err := installRunsc(src, dst, "amd64", map[string]string{"amd64": pin})
	if err != nil || got != filepath.Join(dst, "runsc") {
		t.Fatalf("installRunsc = %q, %v", got, err)
	}
	for _, f := range []string{"runsc", "gvisor-bin/gvisor_sentry", "gvisor-bin/checkpointgofer"} {
		fi, err := os.Stat(filepath.Join(dst, f))
		if err != nil || fi.Mode().Perm() != 0o755 {
			t.Errorf("%s: %v %v", f, fi, err)
		}
	}
	// A second call changes nothing and still answers.
	if again, err := installRunsc(src, dst, "amd64", map[string]string{"amd64": pin}); err != nil || again != got {
		t.Errorf("second call = %q, %v", again, err)
	}
}

func TestARunscThatDoesNotMatchThePinIsNeverUsed(t *testing.T) {
	src, _ := pack(t, "a runsc someone replaced")
	dst := filepath.Join(t.TempDir(), "runsc")
	_, err := installRunsc(src, dst, "amd64", map[string]string{"amd64": strings.Repeat("0", 64)})
	if !errors.Is(err, sandbox.ErrUnsupported) || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("err = %v, want ErrUnsupported naming the checksum", err)
	}
	if _, err := os.Stat(dst); err == nil {
		t.Error("a runsc that failed the check was copied")
	}
}

func TestAnArchitectureWithoutAPinIsRefusedAndAMissingRunscIsNotFound(t *testing.T) {
	src, pin := pack(t, "x")
	if _, err := installRunsc(src, filepath.Join(t.TempDir(), "r"), "riscv64", map[string]string{"amd64": pin}); !errors.Is(err, sandbox.ErrUnsupported) {
		t.Errorf("no pin for the architecture: %v", err)
	}
	if _, err := installRunsc(t.TempDir(), filepath.Join(t.TempDir(), "r"), "amd64", map[string]string{"amd64": pin}); !errors.Is(err, errNoPackagedRunsc) {
		t.Errorf("no runsc in the package: %v", err)
	}
}

func TestACorruptedCopyIsReplaced(t *testing.T) {
	src, pin := pack(t, "good")
	dst := filepath.Join(t.TempDir(), "runsc")
	if _, err := installRunsc(src, dst, "amd64", map[string]string{"amd64": pin}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "runsc"), []byte("tampered"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := installRunsc(src, dst, "amd64", map[string]string{"amd64": pin}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dst, "runsc"))
	if string(b) != "good" {
		t.Errorf("the tampered copy stayed: %q", b)
	}
}

// The values the code pins are the ones in the packaging description.
func TestThePinsInTheCodeAreTheOnesTheReleaseDescribes(t *testing.T) {
	raw, err := os.ReadFile("../../package/runsc.json")
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Release   string `json:"release"`
		Platforms map[string]struct {
			Sha256 string `json:"runscSha256"`
			URL    string `json:"url"`
		} `json:"platforms"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	if d.Release != runscRelease {
		t.Errorf("release %q, code %q", d.Release, runscRelease)
	}
	for arch, want := range map[string]string{"amd64": d.Platforms["linux/amd64"].Sha256, "arm64": d.Platforms["linux/arm64"].Sha256} {
		if runscPins[arch] != want || want == "" {
			t.Errorf("%s: code %q, description %q", arch, runscPins[arch], want)
		}
	}
	for p, v := range d.Platforms {
		if !strings.HasPrefix(v.URL, "https://storage.googleapis.com/gvisor/releases/release/"+d.Release+"/") {
			t.Errorf("%s: url %q", p, v.URL)
		}
	}
}

func TestTheProfileToolIsCopiedNextToRunsc(t *testing.T) {
	src, pin := pack(t, "runsc")
	if err := os.WriteFile(filepath.Join(src, "gvisor-profile"), []byte("tool"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "runsc")
	if _, err := installRunsc(src, dst, "amd64", map[string]string{"amd64": pin}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dst, "gvisor-profile"))
	if err != nil || fi.Mode().Perm() != 0o755 {
		t.Fatalf("gvisor-profile: %v %v", fi, err)
	}
}
