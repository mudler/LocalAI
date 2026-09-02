package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"syscall"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/messaging"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/xlog"
)

// The worker's lifecycle verbs.
//
// Each takes a decoded request and returns a reply value. They carry no
// carrier: the HTTP control plane in control_routes.go is what decodes the
// request, calls one of these, and encodes what comes back. Keeping the verb
// free of its transport is what let the ten NATS subscriptions these replaced
// be deleted without touching a line of what they actually do.

// backendList answers backend.list with the backends installed in this node's
// gallery.
func (s *backendSupervisor) backendList() messaging.BackendListReply {
	xlog.Info("Serving backend.list")
	backends, err := gallery.ListSystemBackends(s.systemState)
	if err != nil {
		return messaging.BackendListReply{Error: err.Error()}
	}

	var infos []messaging.NodeBackendInfo
	for name, b := range backends {
		// Drop synthetic alias rows: ListSystemBackends emits an entry
		// keyed by the alias name that re-uses the chosen concrete's
		// metadata. The frontend can't reconstruct that aliasing
		// faithfully from a flat NodeBackendInfo, and for upgrade
		// detection it would surface as a phantom `<alias>` install
		// pointing at the dev concrete's URI/digest — tricking the
		// upgrade check into flagging the non-dev gallery entry of the
		// same alias. Concrete and meta entries always have
		// `name == b.Metadata.Name`, so this drops aliases only.
		if b.Metadata != nil && b.Metadata.Name != "" && name != b.Metadata.Name {
			continue
		}
		info := messaging.NodeBackendInfo{
			Name:     name,
			IsSystem: b.IsSystem,
			IsMeta:   b.IsMeta,
		}
		if b.Metadata != nil {
			info.InstalledAt = b.Metadata.InstalledAt
			info.GalleryURL = b.Metadata.GalleryURL
			info.Version = b.Metadata.Version
			info.URI = b.Metadata.URI
			info.Digest = b.Metadata.Digest
		}
		infos = append(infos, info)
	}

	return messaging.BackendListReply{Backends: infos}
}

// stopBackends serves backend.stop: it terminates the processes the request
// names, or every process when it names none.
//
// It reports nothing. The verb has always been fire-and-forget, and a stop that
// could report per-process failure would be a different contract than the one
// the frontend was written against.
func (s *backendSupervisor) stopBackends(req messaging.BackendStopRequest, stopAll bool) {
	if stopAll {
		xlog.Info("Serving backend.stop (all)", "force", req.Force)
		s.stopAllBackends(req.Force)
		return
	}
	xlog.Info("Serving backend.stop", "backend", req.Backend, "force", req.Force)
	// The identifier may be a backend name, a model name, or an exact
	// modelID#replica key depending on the caller; resolveStopTargets
	// handles all three. stopBackend alone resolves only the model meanings.
	for _, key := range s.resolveStopTargets(req.Backend) {
		if err := s.stopBackendExact(key, req.Force); err != nil {
			xlog.Error("Failed to stop backend process", "backend", req.Backend, "processKey", key, "error", err)
		}
	}
}

// decodeBackendStopRequest reads a backend.stop body. An EMPTY body means "stop
// everything", which is the shape the frontend uses to drain a node, so it is
// not a decode failure.
func decodeBackendStopRequest(data []byte) (messaging.BackendStopRequest, bool, error) {
	if len(data) == 0 {
		return messaging.BackendStopRequest{}, true, nil
	}
	var req messaging.BackendStopRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return messaging.BackendStopRequest{}, false, fmt.Errorf("decoding backend stop request: %w", err)
	}
	return req, req.Backend == "", nil
}

// deleteBackend serves backend.delete: stop the backend's processes if running,
// then remove its files from disk.
func (s *backendSupervisor) deleteBackend(req messaging.BackendDeleteRequest) messaging.BackendDeleteReply {
	xlog.Info("Serving backend.delete", "backend", req.Backend)

	// Resolve the backend's identity (concrete name + alias) BEFORE touching
	// the filesystem: DeleteBackendFromSystem removes the metadata.json that
	// carries the alias, and a model loaded via the alias records the alias as
	// its process's backend name.
	identity := s.backendIdentity(req.Backend)

	// Stop every process started for this backend. Processes are keyed by
	// modelID#replica, so the lookup must match the recorded backend name — a
	// lookup by backend name alone resolved to nothing and left the process
	// running with its directory deleted underneath it.
	keys := s.resolveProcessKeysForBackend(identity)
	if len(keys) == 0 {
		// Not an error: deleting a backend that was never loaded is routine.
		// But log it — silence here is what made the orphan case invisible.
		xlog.Info("Deleting backend with no matching running process",
			"backend", req.Backend, "identity", slices.Sorted(maps.Keys(identity)))
	}
	// Accumulate the processes we actually terminate. Every stop hands a gRPC
	// port back to this worker's allocator while the controller still holds a
	// NodeModel row for that address, so the controller needs these keys to
	// drop those rows before the port is re-bound by an unrelated backend. A
	// key is appended only after its process is confirmed gone, which is what
	// lets the controller trust the list on the partial-failure replies below.
	stopped := make([]string, 0, len(keys))
	deleteReply := func(success bool, errMsg string) messaging.BackendDeleteReply {
		return messaging.BackendDeleteReply{
			Success:                 success,
			Error:                   errMsg,
			StoppedProcessKeys:      stopped,
			ReportsStoppedProcesses: true,
		}
	}

	for _, key := range keys {
		if err := s.stopBackendExact(key, false); err != nil {
			// We knew about this process and could not kill it. Replying
			// success would repeat the original defect: the operator is told
			// "backend deleted" while the process keeps serving requests.
			xlog.Error("Failed to stop backend process during delete; aborting delete",
				"backend", req.Backend, "processKey", key, "error", err)
			return deleteReply(false, fmt.Sprintf("could not stop running process %s: %v", key, err))
		}
		stopped = append(stopped, key)
	}

	// Delete the backend files
	if err := gallery.DeleteBackendFromSystem(s.systemState, req.Backend); err != nil {
		xlog.Warn("Failed to delete backend files", "backend", req.Backend, "error", err)
		return deleteReply(false, err.Error())
	}

	// Re-register backends after deletion
	if err := gallery.RegisterBackends(s.systemState, s.ml); err != nil {
		xlog.Error("Failed to refresh registered backends after deletion", "backend", req.Backend, "error", err)
		return deleteReply(false, err.Error())
	}

	return deleteReply(true, "")
}

// unloadModel serves model.unload: a gRPC Free() that releases GPU memory
// without killing the backend process.
func (s *backendSupervisor) unloadModel(ctx context.Context, req messaging.ModelUnloadRequest) messaging.ModelUnloadReply {
	xlog.Info("Serving model.unload")

	// Find the backend address for this model's backend type
	// The request includes an Address field if the router knows which process to target
	targetAddr := req.Address
	if targetAddr == "" {
		// Fallback: try all running backends
		s.mu.Lock()
		for _, bp := range s.processes {
			targetAddr = bp.addr
			break
		}
		s.mu.Unlock()
	}

	if targetAddr != "" {
		// Best-effort bounded gRPC Free(). A model.unload request must not
		// occupy the handler forever when a backend is wedged. The bound is
		// derived from the caller's own budget where it has one, so a caller
		// that allowed less than workerBackendFreeTimeout is not made to wait
		// longer than it asked for.
		client := grpc.NewClientWithToken(targetAddr, false, nil, false, s.cfg.RegistrationToken)
		freeCtx, cancel := context.WithTimeout(ctx, workerBackendFreeTimeout)
		if err := client.Free(freeCtx); err != nil {
			xlog.Warn("Free() failed during model.unload", "error", err, "addr", targetAddr)
		}
		cancel()
	}

	return messaging.ModelUnloadReply{Success: true}
}

// deleteModel serves model.delete: remove a model's staged files from disk.
func (s *backendSupervisor) deleteModel(req messaging.ModelDeleteRequest) messaging.ModelDeleteReply {
	xlog.Info("Serving model.delete", "model", req.ModelName)

	if err := gallery.DeleteStagedModelFiles(s.cfg.ModelsPath, req.ModelName); err != nil {
		xlog.Warn("Failed to delete model files", "model", req.ModelName, "error", err)
		return messaging.ModelDeleteReply{Success: false, Error: err.Error()}
	}

	return messaging.ModelDeleteReply{Success: true}
}

// signalNodeStop serves node.stop: it triggers the normal shutdown path via
// sigCh so deferred cleanup runs, rather than exiting the process here.
func (s *backendSupervisor) signalNodeStop() {
	xlog.Info("Serving node.stop — signaling shutdown")
	select {
	case s.sigCh <- syscall.SIGTERM:
	default:
		xlog.Debug("Shutdown already signaled, ignoring duplicate stop")
	}
}
