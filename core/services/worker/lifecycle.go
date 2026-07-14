package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"syscall"

	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/messaging"
	grpc "github.com/mudler/LocalAI/pkg/grpc"
	"github.com/mudler/xlog"
)

// subscribeLifecycleEvents wires every NATS subject this worker accepts to its
// per-event handler method. Each handler lives on *backendSupervisor below;
// keeping the dispatcher to a single line per subject makes adding a new
// subject a 2-line patch (one line here, one new method) instead of grafting
// onto a monolith.
func (s *backendSupervisor) subscribeLifecycleEvents() {
	s.nats.SubscribeReply(messaging.SubjectNodeBackendInstall(s.nodeID), s.handleBackendInstall)
	s.nats.SubscribeReply(messaging.SubjectNodeBackendUpgrade(s.nodeID), s.handleBackendUpgrade)
	s.nats.Subscribe(messaging.SubjectNodeBackendStop(s.nodeID), s.handleBackendStop)
	s.nats.SubscribeReply(messaging.SubjectNodeBackendDelete(s.nodeID), s.handleBackendDelete)
	s.nats.SubscribeReply(messaging.SubjectNodeBackendList(s.nodeID), s.handleBackendList)
	s.nats.SubscribeReply(messaging.SubjectNodeModelUnload(s.nodeID), s.handleModelUnload)
	s.nats.SubscribeReply(messaging.SubjectNodeModelDelete(s.nodeID), s.handleModelDelete)
	s.nats.Subscribe(messaging.SubjectNodeStop(s.nodeID), s.handleNodeStop)
}

// handleBackendInstall is the NATS callback for backend.install — install
// backend (idempotent: skips download if binary exists on disk) + start gRPC
// process (request-reply).
//
// Each request runs in its own goroutine so that a slow install on one
// backend does NOT head-of-line-block install requests for unrelated
// backends arriving on the same subscription. Per-backend serialization
// is provided by lockBackend so two requests targeting the same on-disk
// artifact don't race the gallery directory.
func (s *backendSupervisor) handleBackendInstall(data []byte, reply func([]byte)) {
	go func() {
		xlog.Info("Received NATS backend.install event")
		var req messaging.BackendInstallRequest
		if err := json.Unmarshal(data, &req); err != nil {
			resp := messaging.BackendInstallReply{Success: false, Error: fmt.Sprintf("invalid request: %v", err)}
			replyJSON(reply, resp)
			return
		}

		release := s.lockBackend(req.Backend)
		defer release()

		// req.Force=true is the legacy path used by pre-2026-05-08 masters
		// that don't know about backend.upgrade. Honor it so a rolling
		// update with new worker + old master keeps working; new masters
		// send to backend.upgrade instead.
		addr, err := s.installBackend(req, req.Force)
		if err != nil {
			xlog.Error("Failed to install backend via NATS", "error", err)
			resp := messaging.BackendInstallReply{Success: false, Error: err.Error()}
			replyJSON(reply, resp)
			return
		}

		advertiseAddr := addr
		advAddr := s.cfg.advertiseAddr()
		if advAddr != addr {
			_, port, _ := net.SplitHostPort(addr)
			advertiseHost, _, _ := net.SplitHostPort(advAddr)
			advertiseAddr = net.JoinHostPort(advertiseHost, port)
		}
		resp := messaging.BackendInstallReply{Success: true, Address: advertiseAddr}
		replyJSON(reply, resp)
	}()
}

// handleBackendUpgrade is the NATS callback for backend.upgrade — force-reinstall
// a backend (request-reply). Lives on its own subscription so a multi-minute
// download here does NOT block the install fast-path subscription on the same
// worker.
func (s *backendSupervisor) handleBackendUpgrade(data []byte, reply func([]byte)) {
	go func() {
		xlog.Info("Received NATS backend.upgrade event")
		var req messaging.BackendUpgradeRequest
		if err := json.Unmarshal(data, &req); err != nil {
			resp := messaging.BackendUpgradeReply{Success: false, Error: fmt.Sprintf("invalid request: %v", err)}
			replyJSON(reply, resp)
			return
		}

		release := s.lockBackend(req.Backend)
		defer release()

		if err := s.upgradeBackend(req); err != nil {
			xlog.Error("Failed to upgrade backend via NATS", "error", err)
			replyJSON(reply, messaging.BackendUpgradeReply{Success: false, Error: err.Error()})
			return
		}
		replyJSON(reply, messaging.BackendUpgradeReply{Success: true})
	}()
}

// handleBackendStop is the NATS callback for backend.stop — stop a specific
// backend process (fire-and-forget, no reply expected).
func (s *backendSupervisor) handleBackendStop(data []byte) {
	// Try to parse backend name from payload; if empty, stop all
	var req struct {
		Backend string `json:"backend"`
	}
	if json.Unmarshal(data, &req) == nil && req.Backend != "" {
		xlog.Info("Received NATS backend.stop event", "backend", req.Backend)
		s.stopBackend(req.Backend)
	} else {
		xlog.Info("Received NATS backend.stop event (all)")
		s.stopAllBackends()
	}
}

// handleBackendDelete is the NATS callback for backend.delete — stop the
// backend process if running, then remove its files from disk (request-reply).
func (s *backendSupervisor) handleBackendDelete(data []byte, reply func([]byte)) {
	var req messaging.BackendDeleteRequest
	if err := json.Unmarshal(data, &req); err != nil {
		resp := messaging.BackendDeleteReply{Success: false, Error: fmt.Sprintf("invalid request: %v", err)}
		replyJSON(reply, resp)
		return
	}
	xlog.Info("Received NATS backend.delete event", "backend", req.Backend)

	// Stop if running this backend
	if s.isRunning(req.Backend) {
		s.stopBackend(req.Backend)
	}

	// Delete the backend files
	if err := gallery.DeleteBackendFromSystem(s.systemState, req.Backend); err != nil {
		xlog.Warn("Failed to delete backend files", "backend", req.Backend, "error", err)
		resp := messaging.BackendDeleteReply{Success: false, Error: err.Error()}
		replyJSON(reply, resp)
		return
	}

	// Re-register backends after deletion
	gallery.RegisterBackends(s.systemState, s.ml)

	resp := messaging.BackendDeleteReply{Success: true}
	replyJSON(reply, resp)
}

// handleBackendList is the NATS callback for backend.list — reply with the
// installed backends from this node's gallery (request-reply).
func (s *backendSupervisor) handleBackendList(data []byte, reply func([]byte)) {
	xlog.Info("Received NATS backend.list event")
	backends, err := gallery.ListSystemBackends(s.systemState)
	if err != nil {
		resp := messaging.BackendListReply{Error: err.Error()}
		replyJSON(reply, resp)
		return
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

	resp := messaging.BackendListReply{Backends: infos}
	replyJSON(reply, resp)
}

// handleModelUnload is the NATS callback for model.unload — call gRPC Free()
// to release GPU memory without killing the backend process (request-reply).
func (s *backendSupervisor) handleModelUnload(data []byte, reply func([]byte)) {
	xlog.Info("Received NATS model.unload event")
	var req messaging.ModelUnloadRequest
	if err := json.Unmarshal(data, &req); err != nil {
		resp := messaging.ModelUnloadReply{Success: false, Error: fmt.Sprintf("invalid request: %v", err)}
		replyJSON(reply, resp)
		return
	}

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
		// Best-effort gRPC Free()
		client := grpc.NewClientWithToken(targetAddr, false, nil, false, s.cfg.RegistrationToken)
		if err := client.Free(context.Background()); err != nil {
			xlog.Warn("Free() failed during model.unload", "error", err, "addr", targetAddr)
		}
	}

	resp := messaging.ModelUnloadReply{Success: true}
	replyJSON(reply, resp)
}

// handleModelDelete is the NATS callback for model.delete — remove model
// files from disk (request-reply).
func (s *backendSupervisor) handleModelDelete(data []byte, reply func([]byte)) {
	xlog.Info("Received NATS model.delete event")
	var req messaging.ModelDeleteRequest
	if err := json.Unmarshal(data, &req); err != nil {
		replyJSON(reply, messaging.ModelDeleteReply{Success: false, Error: "invalid request"})
		return
	}

	if err := gallery.DeleteStagedModelFiles(s.cfg.ModelsPath, req.ModelName); err != nil {
		xlog.Warn("Failed to delete model files", "model", req.ModelName, "error", err)
		replyJSON(reply, messaging.ModelDeleteReply{Success: false, Error: err.Error()})
		return
	}

	replyJSON(reply, messaging.ModelDeleteReply{Success: true})
}

// handleNodeStop is the NATS callback for node.stop — trigger the normal
// shutdown path via sigCh so deferred cleanup runs (fire-and-forget).
func (s *backendSupervisor) handleNodeStop(data []byte) {
	xlog.Info("Received NATS stop event — signaling shutdown")
	select {
	case s.sigCh <- syscall.SIGTERM:
	default:
		xlog.Debug("Shutdown already signaled, ignoring duplicate stop")
	}
}
