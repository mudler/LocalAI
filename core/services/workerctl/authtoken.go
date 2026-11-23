package workerctl

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// WorkerAuthToken is the bearer token between a frontend and a worker for the
// data plane: the gRPC calls to the backends of a worker and its file transfer
// server. It is derived from the registration token of the deployment, so that
// the registration token itself stays a secret of the registration step: a
// worker that joined with a join token gets this value in the answer to its
// registration, and never sees the registration token.
//
// It is empty when the deployment has no registration token, and the data plane
// then has no token, as it never had.
func WorkerAuthToken(registrationToken string) string {
	if registrationToken == "" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(registrationToken))
	mac.Write([]byte("localai/worker-data-plane/v1"))
	return hex.EncodeToString(mac.Sum(nil))
}
