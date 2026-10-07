package worker

import (
	"context"
	"fmt"

	"github.com/mudler/LocalAI/core/cli/workerregistry"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/xlog"
)

// connectNATS opens a NATS client using JWT+seed from env or registration (env wins).
func connectNATS(url, envJWT, envSeed, registerJWT, registerSeed string, requireAuth bool, tls messaging.TLSFiles) (*messaging.Client, error) {
	// Env credentials take precedence, but only fall back to registration when
	// the env supplied neither half — otherwise a JWT set without its seed (or
	// vice-versa) would be silently completed from a different source.
	jwt, seed := envJWT, envSeed
	if jwt == "" && seed == "" {
		jwt, seed = registerJWT, registerSeed
	}
	// A JWT without its paired seed (or vice-versa) is a misconfiguration: refuse
	// rather than silently connecting anonymously, which would look authenticated.
	if (jwt == "") != (seed == "") {
		return nil, fmt.Errorf("NATS JWT and seed must be provided together (got JWT set=%t, seed set=%t)", jwt != "", seed != "")
	}
	var opts []messaging.Option
	if jwt != "" && seed != "" {
		opts = append(opts, messaging.WithUserJWT(jwt, seed))
	} else if requireAuth {
		return nil, fmt.Errorf("NATS JWT+seed required: set LOCALAI_NATS_JWT/LOCALAI_NATS_USER_SEED or enable frontend minting")
	}
	if tls.Enabled() {
		opts = append(opts, messaging.WithTLS(tls))
	}
	return messaging.New(url, opts...)
}

// connectNatsFor connects a worker to NATS in the way it always did.
//
// Static credentials from the environment cannot be minted again, so they are
// used as they are. Otherwise the connection takes its credentials from the
// credential manager on every connect, and a goroutine refreshes them before the
// minted JWT expires. If the refresh fails for good, the worker stops, so that it
// restarts and acquires credentials again and does not drift towards a JWT that
// it can no longer renew.
func connectNatsFor(ctx context.Context, stop context.CancelFunc, cfg *Config, credMgr *workerregistry.CredentialManager, static bool, tls messaging.TLSFiles) (*messaging.Client, error) {
	if static {
		return connectNATS(cfg.NatsURL, cfg.NatsJWT, cfg.NatsUserSeed, "", "", cfg.NatsAuthRequired(), tls)
	}
	var opts []messaging.Option
	if credMgr.HasCredentials() {
		opts = append(opts, messaging.WithUserJWTProvider(credMgr.Provider()))
	}
	if tls.Enabled() {
		opts = append(opts, messaging.WithTLS(tls))
	}
	client, err := messaging.New(cfg.NatsURL, opts...)
	if err == nil && credMgr.HasCredentials() {
		go func() {
			if err := credMgr.RefreshLoop(ctx); err != nil {
				xlog.Error("NATS credential refresh permanently failed; shutting down worker", "error", err)
				stop()
			}
		}()
	}
	return client, err
}
