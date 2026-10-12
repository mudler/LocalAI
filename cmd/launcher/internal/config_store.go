// SPDX-License-Identifier: MIT
package launcher

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Never include filesystem paths or JSON decoder payloads in these errors:
// both may contain credentials supplied in settings.
func settingsIOError(action string) error {
	return fmt.Errorf("cannot %s launcher.json securely; ensure ~/.localai is an owned, non-symlink directory writable only by you and launcher.json is an owned regular file (0600); check disk space and permissions", action)
}

func configDirectory() (*os.File, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, settingsIOError("locate")
	}
	path := filepath.Join(home, ".localai")
	if err := os.Mkdir(path, 0700); err != nil && !os.IsExist(err) {
		return nil, settingsIOError("create")
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, settingsIOError("open directory for")
	}
	dir := os.NewFile(uintptr(fd), "launcher settings directory")
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil || st.Uid != uint32(os.Geteuid()) || st.Mode&0022 != 0 {
		dir.Close()
		return nil, settingsIOError("trust directory for")
	}
	return dir, nil
}

// The directory descriptor pins the parent and O_NOFOLLOW prevents following a
// replaced destination. Hard links are refused before chmod to avoid changing
// another file. No permissions on model/backend directories are changed.
func openConfig(dir *os.File) (*os.File, error) {
	fd, err := unix.Openat(int(dir.Fd()), "launcher.json", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "launcher.json")
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 {
		f.Close()
		return nil, settingsIOError("trust")
	}
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return nil, settingsIOError("protect")
	}
	if err = unix.Fstat(fd, &st); err != nil || st.Mode&07777 != 0600 {
		f.Close()
		return nil, settingsIOError("verify permissions for")
	}
	return f, nil
}

func (l *Launcher) loadConfig() (err error) {
	defer func() { l.configError = err }()
	dir, err := configDirectory()
	if err != nil {
		return err
	}
	defer dir.Close()
	f, err := openConfig(dir)
	if errors.Is(err, unix.ENOENT) {
		return l.saveConfig()
	}
	if err != nil {
		return settingsIOError("read")
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return settingsIOError("read")
	}
	var candidate Config
	if err := json.Unmarshal(data, &candidate); err != nil {
		return fmt.Errorf("cannot load launcher settings; repair launcher.json or restore a backup using a supported schema and mode")
	}
	// Migration must be durable before making this profile eligible for startup.
	if err := l.persistConfig(&candidate); err != nil {
		return err
	}
	l.config = &candidate
	return nil
}

func (l *Launcher) saveConfig() error {
	err := l.persistConfig(l.config)
	l.configError = err
	return err
}

func (l *Launcher) persistConfig(candidate *Config) error {
	if candidate == nil {
		return fmt.Errorf("launcher settings are missing")
	}
	data, err := json.MarshalIndent(candidate, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot save launcher settings: %w", err)
	}
	dir, err := configDirectory()
	if err != nil {
		return err
	}
	defer dir.Close()
	existing, err := openConfig(dir)
	if err != nil && !errors.Is(err, unix.ENOENT) {
		return settingsIOError("replace")
	}
	if existing != nil {
		existing.Close()
	}
	// A random name with exclusive creation keeps temporary files private;
	// openat keeps all operations
	// relative to the verified parent, including on Linux and macOS.
	name, err := temporaryConfigName()
	if err != nil {
		return settingsIOError("create temporary")
	}
	fd, err := unix.Openat(int(dir.Fd()), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return settingsIOError("create temporary")
	}
	defer unix.Unlinkat(int(dir.Fd()), name, 0)
	f := os.NewFile(uintptr(fd), "temporary launcher settings")
	defer f.Close()
	if err := f.Chmod(0600); err != nil {
		return settingsIOError("protect temporary")
	}
	info, err := f.Stat()
	if err != nil || info.Mode().Perm() != 0600 {
		return settingsIOError("verify temporary permissions for")
	}
	if _, err := f.Write(data); err != nil {
		return settingsIOError("write")
	}
	if err := f.Sync(); err != nil {
		return settingsIOError("flush")
	}
	if err := f.Close(); err != nil {
		return settingsIOError("close temporary")
	}
	// Recheck immediately before rename. The parent disallows other-user writes;
	// protection against the same user or root is not part of this boundary.
	existing, err = openConfig(dir)
	if err != nil && !errors.Is(err, unix.ENOENT) {
		return settingsIOError("replace")
	}
	if existing != nil {
		existing.Close()
	}
	if err := unix.Renameat(int(dir.Fd()), name, int(dir.Fd()), "launcher.json"); err != nil {
		return settingsIOError("replace")
	}
	candidate.SchemaVersion = 2
	if candidate.Mode == "" {
		candidate.Mode = "standalone"
	}
	return nil
}

func temporaryConfigName() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return ".launcher-" + hex.EncodeToString(random[:]) + ".tmp", nil
}
