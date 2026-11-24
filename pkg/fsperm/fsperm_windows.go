//go:build windows

package fsperm

import (
	"fmt"
	"io/fs"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// noModeBits is true: Windows ignores the mode bits. Files written by older
// LocalAI versions have the access control list of their folder, so Check
// alone would refuse them.
const noModeBits = true

// aceInheritOnly marks an access control entry that only passes to children
// and gives no access to the object itself.
const aceInheritOnly = 0x08

func currentUserSID() (*windows.SID, error) {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("read the current user: %w", err)
	}
	return u.User.Sid, nil
}

// Restrict sets a protected access control list on path that grants full
// control to the current user only. The list of the parent folder no longer
// applies. On a folder the entry passes to the files and folders in it.
func Restrict(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	sid, err := currentUserSID()
	if err != nil {
		return err
	}
	inherit := uint32(windows.NO_INHERITANCE)
	if st.IsDir() {
		inherit = windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       inherit,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}}, nil)
	if err != nil {
		return fmt.Errorf("build the access control list of %s: %w", path, err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil); err != nil {
		return fmt.Errorf("set the access control list of %s: %w", path, err)
	}
	return nil
}

// Check returns nil when the access control list of path gives access only
// to the current user, the SYSTEM account and the Administrators group. The
// last two can read every file of the machine anyway. Any other entry that
// grants access, or no list at all, makes it return an error that wraps
// ErrNotPrivate.
func Check(path string) error {
	if _, err := os.Stat(path); err != nil {
		return err
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("read the access control list of %s: %w", path, err)
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		// No list means that everyone has full access.
		return fmt.Errorf("%s has no access control list: %w", path, ErrNotPrivate)
	}
	user, err := currentUserSID()
	if err != nil {
		return err
	}
	trusted := []*windows.SID{user}
	for _, t := range []windows.WELL_KNOWN_SID_TYPE{windows.WinLocalSystemSid, windows.WinBuiltinAdministratorsSid} {
		if s, err := windows.CreateWellKnownSid(t); err == nil {
			trusted = append(trusted, s)
		}
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return fmt.Errorf("read the access control list of %s: %w", path, err)
		}
		switch ace.Header.AceType {
		case windows.ACCESS_DENIED_ACE_TYPE:
			continue
		case windows.ACCESS_ALLOWED_ACE_TYPE:
		default:
			// Object and callback entries are rare on files. They have
			// another layout, so do not trust them.
			return fmt.Errorf("%s has an access control entry of type %d: %w", path, ace.Header.AceType, ErrNotPrivate)
		}
		if ace.Header.AceFlags&aceInheritOnly != 0 {
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		ok := false
		for _, t := range trusted {
			if sid.Equals(t) {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("%s gives access to %s: %w", path, sid.String(), ErrNotPrivate)
		}
	}
	return nil
}

// IsExecutable reports whether path can run as a program. Windows has no
// execute bit, so the extension decides: .exe, .com, .cmd, .bat or .ps1.
func IsExecutable(path string, mode fs.FileMode) bool {
	return isExecutable("windows", path, mode)
}
