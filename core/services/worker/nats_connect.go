package worker

import (
	"fmt"

	"github.com/mudler/LocalAI/core/services/messaging"
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
