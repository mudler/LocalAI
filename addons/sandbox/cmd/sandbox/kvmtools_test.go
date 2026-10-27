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

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func writeProgram(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPackagedKVMProgramsAreCopiedAsExecutableAndUsed(t *testing.T) {
	src, dst := t.TempDir(), filepath.Join(t.TempDir(), "kvm-bin")
	writeProgram(t, src, "cloud-hypervisor", "chv")
	writeProgram(t, src, "virtiofsd", "vfs")
	pins := map[string]map[string]string{"amd64": {"cloud-hypervisor": sum("chv"), "virtiofsd": sum("vfs")}}
	bins, err := installKVMTools(src, dst, "amd64", pins)
	if err != nil {
		t.Fatalf("installKVMTools: %v", err)
	}
	if bins.CloudHypervisor != filepath.Join(dst, "cloud-hypervisor") || bins.Virtiofsd != filepath.Join(dst, "virtiofsd") {
		t.Fatalf("bins = %+v", bins)
	}
	for _, p := range []string{bins.CloudHypervisor, bins.Virtiofsd} {
		fi, err := os.Stat(p)
		if err != nil || fi.Mode().Perm() != 0o755 {
			t.Errorf("%s: %v %v", p, fi, err)
		}
	}
}

func TestAKVMProgramThatDoesNotMatchItsPinIsNeverUsed(t *testing.T) {
	src, dst := t.TempDir(), filepath.Join(t.TempDir(), "kvm-bin")
	writeProgram(t, src, "cloud-hypervisor", "replaced")
	pins := map[string]map[string]string{"amd64": {"cloud-hypervisor": sum("chv")}}
	_, err := installKVMTools(src, dst, "amd64", pins)
	if !errors.Is(err, sandbox.ErrUnsupported) || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("err = %v, want ErrUnsupported naming the checksum", err)
	}
	if _, statErr := os.Stat(filepath.Join(dst, "cloud-hypervisor")); statErr == nil {
		t.Error("a program that failed the check was copied")
	}
}

func TestAMissingProgramIsLeftToTheSystemLookup(t *testing.T) {
	src, dst := t.TempDir(), filepath.Join(t.TempDir(), "kvm-bin")
	writeProgram(t, src, "cloud-hypervisor", "chv")
	pins := map[string]map[string]string{"arm64": {"cloud-hypervisor": sum("chv")}}
	bins, err := installKVMTools(src, dst, "arm64", pins)
	if err != nil {
		t.Fatalf("installKVMTools: %v", err)
	}
	if bins.Virtiofsd != "" || bins.CloudHypervisor == "" {
		t.Fatalf("bins = %+v, want only cloud-hypervisor", bins)
	}
}

func TestAnArchitectureWithoutAPinRefusesAPackagedProgram(t *testing.T) {
	src := t.TempDir()
	writeProgram(t, src, "virtiofsd", "vfs")
	_, err := installKVMTools(src, filepath.Join(t.TempDir(), "kvm-bin"), "riscv64", map[string]map[string]string{})
	if !errors.Is(err, sandbox.ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

// The values the code pins are the ones in the packaging description.
func TestTheKVMPinsInTheCodeAreTheOnesThePackageDescribes(t *testing.T) {
	raw, err := os.ReadFile("../../package/kvm.json")
	if err != nil {
		t.Fatal(err)
	}
	var d map[string]json.RawMessage
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	for _, program := range []string{"cloud-hypervisor", "virtiofsd"} {
		var p struct {
			Platforms map[string]struct {
				Sha256 string `json:"sha256"`
			} `json:"platforms"`
		}
		if err := json.Unmarshal(d[program], &p); err != nil {
			t.Fatal(err)
		}
		for arch, want := range map[string]string{"amd64": p.Platforms["linux/amd64"].Sha256, "arm64": p.Platforms["linux/arm64"].Sha256} {
			if kvmPins[arch][program] != want {
				t.Errorf("%s %s: code %q, description %q", program, arch, kvmPins[arch][program], want)
			}
		}
	}
}

func TestNoBwrapFollowsTheVariableThenTheContainer(t *testing.T) {
	tests := []struct {
		name        string
		env         string
		set         bool
		inContainer bool
		want        bool
	}{
		{name: "variable on", env: "1", set: true, want: true},
		{name: "variable true", env: "true", set: true, want: true},
		{name: "variable off in a container", env: "0", set: true, inContainer: true, want: false},
		{name: "unset in a container", inContainer: true, want: true},
		{name: "unset on a host", want: false},
		{name: "empty on a host", env: "", set: true, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lookup := func(string) (string, bool) { return tt.env, tt.set }
			if got := noBwrap(lookup, tt.inContainer); got != tt.want {
				t.Errorf("noBwrap = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAContainerIsRecognisedByItsMarkerFiles(t *testing.T) {
	dir := t.TempDir()
	if inContainer(filepath.Join(dir, "a"), filepath.Join(dir, "b")) {
		t.Fatal("no marker file, yet a container")
	}
	marker := filepath.Join(dir, "marker")
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !inContainer(filepath.Join(dir, "a"), marker) {
		t.Fatal("a marker file was not recognised")
	}
}
