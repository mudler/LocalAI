package promptcachesvc

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/workerctl"
)

// WireSettings turns the settings of the frontends into what a worker
// applies. authOn and users feed the automatic per-user quota, which the
// worker computes from its own cap. Version is a hash of the values, so every
// frontend gives the same version for the same settings without a counter.
func WireSettings(c config.PromptCacheConfig, authOn bool, users int) workerctl.PromptCacheSettings {
	s := workerctl.PromptCacheSettings{
		Disabled:        c.DiskDisabled,
		MaxSizeMiB:      c.MaxSizeMiB,
		TTLSeconds:      int64(c.TTL / time.Second),
		UserQuotaMiB:    c.UserQuotaMiB,
		MinTokens:       c.MinTokens,
		Shared:          c.Shared,
		LowFreeMiB:      c.LowFreeMiB,
		CriticalFreeMiB: c.CriticalFreeMiB,
		AuthOn:          authOn,
		Users:           users,
	}
	b, _ := json.Marshal(s)
	sum := sha256.Sum256(b)
	s.Version = hex.EncodeToString(sum[:8])
	return s
}

// ConfigOf is the configuration a worker runs from the settings it was sent.
// A value the frontends left at zero takes its default.
func ConfigOf(s workerctl.PromptCacheSettings) config.PromptCacheConfig {
	c := config.DefaultPromptCacheConfig()
	c.DiskDisabled = s.Disabled
	c.MaxSizeMiB = s.MaxSizeMiB
	if s.TTLSeconds > 0 {
		c.TTL = time.Duration(s.TTLSeconds) * time.Second
	}
	c.UserQuotaMiB = s.UserQuotaMiB
	if s.MinTokens > 0 {
		c.MinTokens = s.MinTokens
	}
	c.Shared = s.Shared
	c.LowFreeMiB = s.LowFreeMiB
	c.CriticalFreeMiB = s.CriticalFreeMiB
	return c
}
