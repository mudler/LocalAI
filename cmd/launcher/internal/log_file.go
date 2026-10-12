// SPDX-License-Identifier: MIT
package launcher

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
)

func secureLogFile(dataPath string) (*os.File, error) {
	fail := func() (*os.File, error) {
		return nil, fmt.Errorf("cannot create secure launcher log; check directory ownership and permissions")
	}
	path := filepath.Join(dataPath, "logs")
	if e := os.Mkdir(path, 0700); e != nil && !os.IsExist(e) {
		return fail()
	}
	fd, e := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return fail()
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Uid != uint32(os.Geteuid()) || st.Mode&0022 != 0 {
		return fail()
	}
	if unix.Fchmod(fd, 0700) != nil {
		return fail()
	}
	nonce := make([]byte, 16)
	if _, e = rand.Read(nonce); e != nil {
		return fail()
	}
	name := "localai_" + hex.EncodeToString(nonce) + ".log"
	f, e := unix.Openat(fd, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if e != nil {
		return fail()
	}
	return os.NewFile(uintptr(f), filepath.Join(path, name)), nil
}
