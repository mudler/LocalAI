package system

import (
	"fmt"
	"os"
	"os/user"
	"runtime"
	"strings"
)

// UserScopedName returns a folder name that is different for each user of the
// machine: "localai-<uid>" on Unix. Windows has no uid (os.Getuid returns -1
// there), so the name has the security identifier (SID) of the user, for
// example "localai-S-1-5-21-1004". Use it below a temp folder that several
// users can share.
func UserScopedName() string {
	return userScopedName(runtime.GOOS, os.Getuid(), user.Current)
}

func userScopedName(goos string, uid int, current func() (*user.User, error)) string {
	if goos != "windows" {
		return fmt.Sprintf("localai-%d", uid)
	}
	u, err := current()
	if err != nil || u.Uid == "" {
		// The temp folder of a Windows user is in the profile of that user,
		// so a plain name does not collide in the default setup.
		return "localai"
	}
	// A SID has only letters, digits and dashes. Keep that so the name is a
	// valid file name whatever the account database returns.
	safe := strings.Map(func(r rune) rune {
		if r == '-' || (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
			return r
		}
		return '_'
	}, u.Uid)
	return "localai-" + safe
}
