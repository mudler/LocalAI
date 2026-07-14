package billing

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/xlog"
)

// LocalUserName is the fixed display name used for the synthetic
// no-auth user. Surfaces it in the dashboard so single-user installs
// have a recognizable label rather than an opaque UUID.
const LocalUserName = "local"

// localUserIDFile is the basename, inside DataPath, where we persist
// the synthetic user's UUID so it stays stable across restarts.
const localUserIDFile = ".local_user_id"

var (
	localOnce sync.Once
	localUser *auth.User
)

// LocalUser returns a process-singleton "local" user used by
// UsageMiddleware when --auth is off. The user's ID is persisted to
// dataPath so usage history aggregates correctly across restarts; if
// dataPath is empty, a fresh random UUID is generated for this process
// only and aggregation drops on restart (in-memory mode).
//
// Concurrency note: the singleton uses sync.Once, so calling LocalUser
// from any goroutine is safe; the first call may briefly hit disk.
func LocalUser(dataPath string) *auth.User {
	localOnce.Do(func() {
		id := loadOrGenerateLocalUserID(dataPath)
		localUser = &auth.User{
			ID:       id,
			Name:     LocalUserName,
			Email:    "",
			Provider: auth.ProviderLocal,
			Role:     "admin", // single-user box: the only user has full access
			Status:   "active",
		}
	})
	return localUser
}

func loadOrGenerateLocalUserID(dataPath string) string {
	if dataPath != "" {
		path := filepath.Join(dataPath, localUserIDFile)
		if b, err := os.ReadFile(path); err == nil {
			id := string(b)
			if len(id) > 0 {
				return id
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			xlog.Warn("failed to read local user id file; generating fresh", "path", path, "error", err)
		}
		id := newUUID()
		// 0600: only the LocalAI process owner should read this. The file
		// is just a stable identifier, not a credential, but we keep it
		// tight by default.
		if err := os.WriteFile(path, []byte(id), 0o600); err != nil {
			xlog.Warn("failed to persist local user id; will regenerate next start", "path", path, "error", err)
		}
		return id
	}
	return newUUID()
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	// Set version 4 + RFC 4122 variant bits so this round-trips through
	// any UUID parser the rest of the codebase might use.
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	hexb := hex.EncodeToString(b[:])
	return hexb[0:8] + "-" + hexb[8:12] + "-" + hexb[12:16] + "-" + hexb[16:20] + "-" + hexb[20:32]
}
