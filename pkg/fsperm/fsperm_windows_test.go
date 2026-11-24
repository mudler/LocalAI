//go:build windows

package fsperm

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// grantEveryone adds an entry that lets every user read path, the way an
// inherited list of a shared folder does.
func grantEveryone(t *testing.T, path string) {
	t.Helper()
	everyone, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	old, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_READ,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
			TrusteeValue: windows.TrusteeValueFromSID(everyone),
		},
	}}, old)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, acl, nil); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsRestrictAndCheck(t *testing.T) {
	file := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	grantEveryone(t, file)
	if err := Check(file); !errors.Is(err, ErrNotPrivate) {
		t.Fatalf("Check with an Everyone entry = %v, want ErrNotPrivate", err)
	}
	if err := Restrict(file); err != nil {
		t.Fatal(err)
	}
	if err := Check(file); err != nil {
		t.Fatalf("Check after Restrict = %v", err)
	}
	if b, err := os.ReadFile(file); err != nil || string(b) != "x" {
		t.Fatalf("the owner cannot read the file after Restrict: %q, %v", b, err)
	}
}

func TestWindowsCheckOrRestrictRepairs(t *testing.T) {
	file := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	grantEveryone(t, file)
	restricted, err := CheckOrRestrict(file)
	if err != nil || !restricted {
		t.Fatalf("CheckOrRestrict = %v, %v; want true, nil", restricted, err)
	}
	if err := Check(file); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsMkdirAllAndWriteFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if err := MkdirAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := Check(dir); err != nil {
		t.Fatalf("Check(folder) = %v", err)
	}
	file := filepath.Join(dir, "secret")
	if err := WriteFile(file, []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Check(file); err != nil {
		t.Fatalf("Check(file) = %v", err)
	}
}

func TestWindowsIsExecutable(t *testing.T) {
	if !IsExecutable(`C:\x\llama.exe`, 0o666) || IsExecutable(`C:\x\run.sh`, 0o777) {
		t.Fatal("IsExecutable does not follow the extension rule")
	}
}

func TestWindowsRequirePrivateRepairs(t *testing.T) {
	file := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	grantEveryone(t, file)
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	restricted, err := RequirePrivate(file, info)
	if err != nil || !restricted {
		t.Fatalf("RequirePrivate = %v, %v; want true, nil", restricted, err)
	}
	if err := Check(file); err != nil {
		t.Fatal(err)
	}
	if restricted, err := RequirePrivate(file, info); err != nil || restricted {
		t.Fatalf("RequirePrivate on a private file = %v, %v; want false, nil", restricted, err)
	}
}
