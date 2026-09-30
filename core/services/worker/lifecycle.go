package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"slices"
	"strings"
	"syscall"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/workerctl"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/xlog"
)

// registerLifecycleVerbs serves every lifecycle verb this worker accepts on
// srv. Each verb is one line here and one typed method below, so adding a verb
// does not graft onto a monolith.
func (s *backendSupervisor) registerLifecycleVerbs(srv controlServer) error {
	reg := []func() error{
		func() error {
			return srv.handleWithProgress(verbBackendInstall, withProgress(decodeJSON[workerctl.BackendInstallRequest], refuseInstall, s.serveInstall))
		},
		func() error {
			return srv.handleWithProgress(verbBackendUpgrade, withProgress(decodeJSON[workerctl.BackendUpgradeRequest], refuseUpgrade, s.serveUpgrade))
		},
		func() error {
			return srv.handle(verbBackendStop, unary(decodeBackendStop, refuseBackendStop, s.stopBackends))
		},
		func() error {
			return srv.handle(verbBackendDelete, unary(decodeJSON[workerctl.BackendDeleteRequest], refuseDelete, s.deleteBackend))
		},
		func() error {
			return srv.handle(verbBackendList, unary(ignoreBody[workerctl.BackendListRequest], refuseNever[workerctl.BackendListReply], s.backendList))
		},
		func() error {
			return srv.handle(verbModelsRunning, unary(ignoreBody[workerctl.ModelsRunningRequest], refuseNever[workerctl.ModelsRunningReply], s.modelsRunning))
		},
		func() error {
			return srv.handle(verbModelUnload, unary(decodeJSON[workerctl.ModelUnloadRequest], refuseUnload, s.unloadModel))
		},
		func() error {
			return srv.handle(verbModelStop, unary(decodeJSON[workerctl.ModelStopRequest], refuseModelStop, s.stopModelExactCtx))
		},
		func() error {
			return srv.handle(verbModelDelete, unary(decodeJSON[workerctl.ModelDeleteRequest], refuseModelDelete, s.deleteModel))
		},
		func() error { return srv.handle(verbNodeStop, noReply(s.signalNodeStop)) },
	}
	for _, r := range reg {
		if err := r(); err != nil {
			return err
		}
	}
	return nil
}

// The refusals below are the replies each verb sent for an undecodable body
// before the verbs had a carrier seam. Requesters may match on them, so they
// are kept byte for byte, including model.delete omitting the cause. Each one
// logs, because the verbs log receipt only after a successful decode and a
// malformed request would otherwise leave no trace on the worker.

func refuseInstall(err error) workerctl.BackendInstallReply {
	xlog.Warn("Ignoring malformed control request", "verb", verbBackendInstall, "error", err)
	return workerctl.BackendInstallReply{Success: false, Error: fmt.Sprintf("invalid request: %v", err)}
}

func refuseUpgrade(err error) workerctl.BackendUpgradeReply {
	xlog.Warn("Ignoring malformed control request", "verb", verbBackendUpgrade, "error", err)
	return workerctl.BackendUpgradeReply{Success: false, Error: fmt.Sprintf("invalid request: %v", err)}
}

func refuseBackendStop(err error) workerctl.BackendStopReply {
	xlog.Error("Ignoring malformed NATS backend.stop event", "error", err)
	return workerctl.BackendStopReply{
		Error:                   fmt.Sprintf("invalid request: %v", err),
		ReportsStoppedProcesses: true,
	}
}

func refuseDelete(err error) workerctl.BackendDeleteReply {
	xlog.Warn("Ignoring malformed control request", "verb", verbBackendDelete, "error", err)
	return workerctl.BackendDeleteReply{Success: false, Error: fmt.Sprintf("invalid request: %v", err)}
}

func refuseUnload(err error) workerctl.ModelUnloadReply {
	xlog.Warn("Ignoring malformed control request", "verb", verbModelUnload, "error", err)
	return workerctl.ModelUnloadReply{Success: false, Error: fmt.Sprintf("invalid request: %v", err)}
}

func refuseModelStop(err error) workerctl.ModelStopReply {
	xlog.Warn("Ignoring malformed control request", "verb", verbModelStop, "error", err)
	return workerctl.ModelStopReply{Error: fmt.Sprintf("invalid request: %v", err)}
}

func refuseModelDelete(err error) workerctl.ModelDeleteReply {
	xlog.Warn("Ignoring malformed control request", "verb", verbModelDelete, "error", err)
	return workerctl.ModelDeleteReply{Success: false, Error: "invalid request"}
}

func (s *backendSupervisor) stopModelExactCtx(_ context.Context, req workerctl.ModelStopRequest) workerctl.ModelStopReply {
	return s.stopModelExact(req)
}

// serveInstall answers backend.install: install the backend (idempotent: skips
// download if binary exists on disk) and start its gRPC process.
//
// The server runs each request on its own goroutine so that a slow install on
// one backend does NOT head-of-line-block install requests for unrelated
// backends. Per-backend serialization is provided by lockBackend so two
// requests targeting the same on-disk artifact don't race the gallery
// directory.
func (s *backendSupervisor) serveInstall(_ context.Context, req workerctl.BackendInstallRequest, progress progressSink) workerctl.BackendInstallReply {
	xlog.Info("Received NATS backend.install event")
	release := s.lockBackend(req.Backend)
	defer release()
	downloadCb, flush := s.downloadProgress(req.OpID, req.Backend, progress)
	defer flush()

	// req.Force=true is the legacy path used by pre-2026-05-08 masters
	// that don't know about backend.upgrade. Honor it so a rolling
	// update with new worker + old master keeps working; new masters
	// send to backend.upgrade instead.
	install := s.installFn
	if install == nil {
		install = s.installBackend
	}
	addr, err := install(req, req.Force, downloadCb)
	if err != nil {
		xlog.Error("Failed to install backend via NATS", "error", err)
		return workerctl.BackendInstallReply{Success: false, Error: err.Error()}
	}

	advertiseAddr := addr
	advAddr := s.cfg.advertiseAddr()
	if advAddr != addr {
		_, port, err := net.SplitHostPort(addr)
		if err != nil {
			xlog.Error("Failed to parse backend listen address; using it unchanged", "addr", addr, "error", err)
		} else if advertiseHost, _, err := net.SplitHostPort(advAddr); err != nil {
			xlog.Error("Failed to parse worker advertise address; using backend listen address", "addr", advAddr, "error", err)
		} else {
			advertiseAddr = net.JoinHostPort(advertiseHost, port)
		}
	}
	return workerctl.BackendInstallReply{Success: true, Address: advertiseAddr}
}

// serveUpgrade answers backend.upgrade: force-reinstall a backend. It is its
// own verb so a multi-minute download here does NOT block the install
// fast-path on the same worker.
func (s *backendSupervisor) serveUpgrade(_ context.Context, req workerctl.BackendUpgradeRequest, progress progressSink) workerctl.BackendUpgradeReply {
	xlog.Info("Received NATS backend.upgrade event")
	release := s.lockBackend(req.Backend)
	defer release()
	downloadCb, flush := s.downloadProgress(req.OpID, req.Backend, progress)
	defer flush()

	// stopped is meaningful even on the error paths: it lists processes
	// already terminated (and ports already recycled) before the failure, so
	// the controller must drop those rows regardless of the outcome.
	upgrade := s.upgradeFn
	if upgrade == nil {
		upgrade = s.upgradeBackend
	}
	stopped, err := upgrade(req, downloadCb)
	if err != nil {
		xlog.Error("Failed to upgrade backend via NATS", "error", err)
		return workerctl.BackendUpgradeReply{
			Success:                 false,
			Error:                   err.Error(),
			StoppedProcessKeys:      stopped,
			ReportsStoppedProcesses: true,
		}
	}
	return workerctl.BackendUpgradeReply{
		Success:                 true,
		StoppedProcessKeys:      stopped,
		ReportsStoppedProcesses: true,
	}
}

// downloadProgress returns the gallery download callback for one install or
// upgrade and the flush the caller must defer. Requesters that send no OpID
// predate progress reporting and get a nil callback, so they see no events.
// The debounce and the terminal flush sit here, in the handler path, so every
// carrier behind progress forwards what it receives and sees the same bounded
// event rate. The flush runs before the reply, so the requester sees the
// terminal percentage even when the install fails.
func (s *backendSupervisor) downloadProgress(opID, backend string, progress progressSink) (func(file, current, total string, percentage float64), func()) {
	if opID == "" {
		return nil, func() {}
	}
	sink := nodes.NewDebouncedInstallProgressSink(progress, s.nodeID, opID, backend, installProgressDebounce)
	return sink.OnDownload, sink.Flush
}

// stopBackends answers backend.stop: stop a specific backend process (or all
// of them) and report what it terminated.
//
// The reply is what lets the controller tell a stop that worked from one that
// matched nothing or failed. Callers that publish without a reply subject (an
// older controller) still work: SubscribeReply drops the response.
func (s *backendSupervisor) stopBackends(_ context.Context, req workerctl.BackendStopRequest) workerctl.BackendStopReply {
	// decodeBackendStopRequest reports stop-all exactly when Backend is empty
	// (an empty body decodes to that too), so it is derived here, not carried.
	if req.Backend == "" {
		xlog.Info("Received NATS backend.stop event (all)", "force", req.Force)
		stopped := s.stopAllBackends(req.Force)
		return workerctl.BackendStopReply{
			Success:                 true,
			StoppedProcessKeys:      stopped,
			ReportsStoppedProcesses: true,
		}
	}
	xlog.Info("Received NATS backend.stop event", "backend", req.Backend, "force", req.Force)
	// The identifier may be a backend name, a model name, or an exact
	// modelID#replica key depending on the publisher; resolveStopTargets
	// handles all three. stopBackend alone resolves only the model meanings.
	var stopped []string
	var failures []string
	for _, key := range s.resolveStopTargets(req.Backend) {
		if err := s.stopBackendExact(key, req.Force); err != nil {
			xlog.Error("Failed to stop backend process", "backend", req.Backend, "processKey", key, "error", err)
			failures = append(failures, fmt.Sprintf("%s: %v", key, err))
			continue
		}
		stopped = append(stopped, key)
	}
	// Resolving to nothing is reported as success with an empty list, not as a
	// failure: stopping a backend that is not running is the state the caller
	// asked for. The empty list is what tells the caller nothing matched, and
	// ReportsStoppedProcesses is what makes that emptiness trustworthy.
	res := workerctl.BackendStopReply{
		Success:                 len(failures) == 0,
		StoppedProcessKeys:      stopped,
		ReportsStoppedProcesses: true,
	}
	if len(failures) > 0 {
		res.Error = strings.Join(failures, "; ")
	}
	return res
}

func decodeBackendStop(data []byte) (workerctl.BackendStopRequest, error) {
	req, _, err := decodeBackendStopRequest(data)
	return req, err
}

func decodeBackendStopRequest(data []byte) (workerctl.BackendStopRequest, bool, error) {
	if len(data) == 0 {
		return workerctl.BackendStopRequest{}, true, nil
	}
	var req workerctl.BackendStopRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return workerctl.BackendStopRequest{}, false, fmt.Errorf("decoding backend stop request: %w", err)
	}
	return req, req.Backend == "", nil
}

// deleteBackend answers backend.delete: stop the backend process if running,
// then remove its files from disk.
func (s *backendSupervisor) deleteBackend(_ context.Context, req workerctl.BackendDeleteRequest) workerctl.BackendDeleteReply {
	xlog.Info("Received NATS backend.delete event", "backend", req.Backend)

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
	deleteReply := func(success bool, errMsg string) workerctl.BackendDeleteReply {
		return workerctl.BackendDeleteReply{
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

// backendList answers backend.list with the installed backends from this
// node's gallery.
func (s *backendSupervisor) backendList(_ context.Context, _ workerctl.BackendListRequest) workerctl.BackendListReply {
	xlog.Info("Received NATS backend.list event")
	backends, err := gallery.ListSystemBackends(s.systemState)
	if err != nil {
		return workerctl.BackendListReply{Error: err.Error()}
	}

	var infos []workerctl.NodeBackendInfo
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
		info := workerctl.NodeBackendInfo{
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

	return workerctl.BackendListReply{Backends: infos}
}

// unloadModel answers model.unload: call gRPC Free() to release GPU memory
// without killing the backend process.
func (s *backendSupervisor) unloadModel(ctx context.Context, req workerctl.ModelUnloadRequest) workerctl.ModelUnloadReply {
	xlog.Info("Received NATS model.unload event")

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
		// occupy the NATS reply handler forever when a backend is wedged.
		client := grpc.NewClientWithToken(targetAddr, false, nil, false, s.cfg.RegistrationToken)
		freeCtx, cancel := context.WithTimeout(ctx, workerBackendFreeTimeout)
		if err := client.Free(freeCtx); err != nil {
			xlog.Warn("Free() failed during model.unload", "error", err, "addr", targetAddr)
		}
		cancel()
	}

	return workerctl.ModelUnloadReply{Success: true}
}

// deleteModel answers model.delete: remove model files from disk.
func (s *backendSupervisor) deleteModel(_ context.Context, req workerctl.ModelDeleteRequest) workerctl.ModelDeleteReply {
	xlog.Info("Received NATS model.delete event")
	if err := gallery.DeleteStagedModelFiles(s.cfg.ModelsPath, req.ModelName); err != nil {
		xlog.Warn("Failed to delete model files", "model", req.ModelName, "error", err)
		return workerctl.ModelDeleteReply{Success: false, Error: err.Error()}
	}
	return workerctl.ModelDeleteReply{Success: true}
}

// signalNodeStop answers node.stop: trigger the normal shutdown path via sigCh
// so deferred cleanup runs. It never replies.
func (s *backendSupervisor) signalNodeStop(_ context.Context) {
	xlog.Info("Received NATS stop event — signaling shutdown")
	select {
	case s.sigCh <- syscall.SIGTERM:
	default:
		xlog.Debug("Shutdown already signaled, ignoring duplicate stop")
	}
}
