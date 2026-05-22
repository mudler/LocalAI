package galleryop

import "errors"

// ErrWorkerStillInstalling indicates a distributed backend install
// timed out at the NATS round-trip layer but the worker is most likely
// still pulling the OCI image in the background. Producers
// (DistributedBackendManager) wrap this when the round-trip times out;
// consumers (backendHandler) use errors.Is(err, ErrWorkerStillInstalling)
// to surface a yellow "in progress" OpStatus instead of a red error,
// leaving the pending_backend_ops row in place for the reconciler to
// confirm via backend.list.
var ErrWorkerStillInstalling = errors.New("worker did not reply in time; install may still be running in the background")
