package worker

import (
	"fmt"

	"github.com/mudler/LocalAI/core/services/messaging"
)

// connectNATS opens a NATS client using JWT+seed from env or registration (env wins).
func connectNATS(url, envJWT, envSeed, registerJWT, registerSeed string, requireAuth bool, tls messaging.TLSFiles) (*messaging.Client, error) {
	jwt, seed := envJWT, envSeed
	if jwt == "" {
		jwt, seed = registerJWT, registerSeed
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