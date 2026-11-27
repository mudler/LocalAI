package auth

import (
	"sync/atomic"

	"github.com/mudler/LocalAI/core/promptcache"
	"gorm.io/gorm"
)

// principalRemovedHook gets the disk prompt cache namespaces of a principal
// that is gone (a deleted user and its client keys, a revoked client key).
// The application sets it to purge their cached prompts.
var principalRemovedHook atomic.Pointer[func(namespaces []string)]

// SetPrincipalRemovedHook sets the function that gets the namespaces of a
// removed principal. nil removes it. The function must not block: it runs on
// the request that removed the principal.
func SetPrincipalRemovedHook(fn func(namespaces []string)) {
	if fn == nil {
		principalRemovedHook.Store(nil)
		return
	}
	principalRemovedHook.Store(&fn)
}

func principalRemoved(namespaces ...string) {
	if len(namespaces) == 0 {
		return
	}
	if fn := principalRemovedHook.Load(); fn != nil {
		(*fn)(namespaces)
	}
}

// clientKeyIDs lists the IDs of the client keys of a user.
func clientKeyIDs(db *gorm.DB, userID string) []string {
	var ids []string
	db.Model(&UserAPIKey{}).Where("user_id = ? AND kind = ?", userID, KeyKindClient).Pluck("id", &ids)
	return ids
}

// UserPromptCacheNamespaces are the disk prompt cache namespaces of a user:
// its own and the ones of its client keys.
func UserPromptCacheNamespaces(db *gorm.DB, userID string) []string {
	out := []string{promptcache.UserNamespace(userID)}
	if db == nil {
		return out
	}
	for _, id := range clientKeyIDs(db, userID) {
		out = append(out, promptcache.ClientNamespace(id))
	}
	return out
}
