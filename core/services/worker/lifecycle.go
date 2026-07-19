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
func (s *backendSupervisor) subscribeLifecycleEvents() error {
	if _, err := s.nats.SubscribeReply(messaging.SubjectNodeBackendInstall(s.nodeID), s.handleBackendInstall); err != nil {
		return fmt.Errorf("subscribing to backend install events: %w", err)
	}
	if _, err := s.nats.SubscribeReply(messaging.SubjectNodeBackendUpgrade(s.nodeID), s.handleBackendUpgrade); err != nil {
		return fmt.Errorf("subscribing to backend upgrade events: %w", err)
	}
	if _, err := s.nats.Subscribe(messaging.SubjectNodeBackendStop(s.nodeID), s.handleBackendStop); err != nil {
		return fmt.Errorf("subscribing to backend stop events: %w", err)
	}
	if _, err := s.nats.SubscribeReply(messaging.SubjectNodeBackendDelete(s.nodeID), s.handleBackendDelete); err != nil {
		return fmt.Errorf("subscribing to backend delete events: %w", err)
	}
	if _, err := s.nats.SubscribeReply(messaging.SubjectNodeBackendList(s.nodeID), s.handleBackendList); err != nil {
		return fmt.Errorf("subscribing to backend list events: %w", err)
	}
	if _, err := s.nats.SubscribeReply(messaging.SubjectNodeModelUnload(s.nodeID), s.handleModelUnload); err != nil {
		return fmt.Errorf("subscribing to model unload events: %w", err)
	}
	if _, err := s.nats.SubscribeReply(messaging.SubjectNodeModelDelete(s.nodeID), s.handleModelDelete); err != nil {
		return fmt.Errorf("subscribing to model delete events: %w", err)
	}
	if _, err := s.nats.Subscribe(messaging.SubjectNodeStop(s.nodeID), s.handleNodeStop); err != nil {
		return fmt.Errorf("subscribing to node stop events: %w", err)
	}
	return nil
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
			_, port, err := net.SplitHostPort(addr)
			if err != nil {
				xlog.Error("Failed to parse backend listen address; using it unchanged", "addr", addr, "error", err)
			} else if advertiseHost, _, err := net.SplitHostPort(advAddr); err != nil {
				xlog.Error("Failed to parse worker advertise address; using backend listen address", "addr", advAddr, "error", err)
			} else {
				advertiseAddr = net.JoinHostPort(advertiseHost, port)
			}
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
	req, stopAll, err := decodeBackendStopRequest(data)
	if err != nil {
		xlog.Error("Ignoring malformed NATS backend.stop event", "error", err)
		return
	}
	if stopAll {
		xlog.Info("Received NATS backend.stop event (all)", "force", req.Force)
		s.stopAllBackends(req.Force)
		return
	}
	xlog.Info("Received NATS backend.stop event", "backend", req.Backend, "force", req.Force)
	s.stopBackend(req.Backend, req.Force)
}

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
		s.stopBackend(req.Backend, false)
	}

	// Delete the backend files
	if err := gallery.DeleteBackendFromSystem(s.systemState, req.Backend); err != nil {
		xlog.Warn("Failed to delete backend files", "backend", req.Backend, "error", err)
		resp := messaging.BackendDeleteReply{Success: false, Error: err.Error()}
		replyJSON(reply, resp)
		return
	}

	// Re-register backends after deletion
	if err := gallery.RegisterBackends(s.systemState, s.ml); err != nil {
		xlog.Error("Failed to refresh registered backends after deletion", "backend", req.Backend, "error", err)
		replyJSON(reply, messaging.BackendDeleteReply{Success: false, Error: err.Error()})
		return
	}

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
		// Best-effort bounded gRPC Free(). A model.unload request must not
		// occupy the NATS reply handler forever when a backend is wedged.
		client := grpc.NewClientWithToken(targetAddr, false, nil, false, s.cfg.RegistrationToken)
		freeCtx, cancel := context.WithTimeout(context.Background(), workerBackendFreeTimeout)
		if err := client.Free(freeCtx); err != nil {
			xlog.Warn("Free() failed during model.unload", "error", err, "addr", targetAddr)
		}
		cancel()
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
