package natsauth

import (
	"fmt"
	"time"

	"github.com/mudler/xlog"
)

// DefaultWorkerJWTTTL is how long a worker may use a minted NATS user JWT before re-registering.
const DefaultWorkerJWTTTL = 24 * time.Hour

// Config holds NATS JWT authentication settings for distributed mode.
type Config struct {
	// AccountSeed is the NATS account signing seed (SU...). Used to mint per-node worker JWTs.
	AccountSeed string
	// ServiceUserJWT is a pre-generated user JWT for frontends and agent workers (broad publish).
	ServiceUserJWT string
	// ServiceUserSeed is the signing seed (SU...) paired with ServiceUserJWT.
	ServiceUserSeed string
	// WorkerJWTTTL sets expiry on minted worker JWTs. Zero uses DefaultWorkerJWTTTL.
	WorkerJWTTTL time.Duration
	// RequireAuth rejects anonymous NATS when true (both ServiceUserJWT and AccountSeed expected).
	RequireAuth bool
}

// Enabled reports whether any NATS credential material is configured.
func (c Config) Enabled() bool {
	return c.AccountSeed != "" || c.ServiceUserJWT != ""
}

// CanMintWorkers reports whether per-node JWTs can be issued at registration.
func (c Config) CanMintWorkers() bool {
	return c.AccountSeed != ""
}

// WorkerTTL returns the configured worker JWT lifetime.
func (c Config) WorkerTTL() time.Duration {
	if c.WorkerJWTTTL > 0 {
		return c.WorkerJWTTTL
	}
	return DefaultWorkerJWTTTL
}

// Validate checks consistency when distributed NATS auth is required.
func (c Config) Validate() error {
	if !c.RequireAuth {
		return nil
	}
	if c.ServiceUserJWT == "" || c.ServiceUserSeed == "" {
		return fmt.Errorf("LOCALAI_NATS_REQUIRE_AUTH requires LOCALAI_NATS_SERVICE_JWT and LOCALAI_NATS_SERVICE_SEED")
	}
	if c.AccountSeed == "" {
		return fmt.Errorf("LOCALAI_NATS_REQUIRE_AUTH is set but LOCALAI_NATS_ACCOUNT_SEED is empty")
	}
	return nil
}

// WarnIfInsecure logs when distributed NATS is reachable without credentials.
func (c Config) WarnIfInsecure(distributed bool) {
	if !distributed || c.Enabled() {
		return
	}
	xlog.Warn("NATS is used without JWT credentials — any client on the bus can publish backend.install. " +
		"Set LOCALAI_NATS_ACCOUNT_SEED + LOCALAI_NATS_SERVICE_JWT (see docs/features/distributed-mode.md).")
}