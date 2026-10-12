// SPDX-License-Identifier: MIT
package worker

import (
	"context"
	"net"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/cli/workerregistry"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/pkg/workerstatus"
)

// observeApproval is deliberately independent of heartbeat and JWT renewal.
// It never calls RegisterFull: registration restores offline nodes and mints
// credentials, neither of which is an observation of administrator intent.
func observeApproval(ctx context.Context, status *workerstatus.Publisher, client *workerregistry.RegistrationClient, nodeID string, interval time.Duration) {
	if status == nil {
		return
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		outcome, err := client.Approval(ctx, nodeID)
		status.Update(func(s *workerstatus.Snapshot) {
			s.RegistrationTime = time.Now()
			s.Registration = "unknown"
			if err != nil {
				s.Registration = "error"
			} else if outcome == "healthy" || outcome == "pending" {
				s.Registration = outcome
			}
		})
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// armWorkerStatus is shared by Run and its bounded production-component tests.
// Backend reachability remains part of readiness even with healthy NATS.
func armWorkerStatus(status *workerstatus.Publisher, readiness *nodes.WorkerReadiness, bus *messaging.Client, backends nodes.BackendAddressLister) {
	readiness.Set(nodes.CompositeReadiness(nodes.NATSReadiness(bus), nodes.BackendDataPathReadiness(backends, func(addr string) error {
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err != nil {
			return err
		}
		return conn.Close()
	})))
	status.Arm(func() (bool, bool, bool, bool) {
		return bus.IsConnected(), bus.Authenticated(), bus.SubscriptionsReady(), readiness.Check() == nil
	})
}

// workerRegistration shares Run's acquisition and renewal wiring with status tests.
func workerRegistration(cfg *Config, status *workerstatus.Publisher, regClient *workerregistry.RegistrationClient, acquired *atomic.Bool, nodeID *string, registrationBody map[string]any) workerregistry.RegisterFunc {
	regClient.ObserveRegistration = func(res *workerregistry.RegisterResponse, err error) {
		if status == nil {
			return
		}
		if acquired.Load() {
			// Approval and retained credentials remain independent of renewal.
			// Only a successful renewal can clear this categorical failure.
			status.Update(func(s *workerstatus.Snapshot) {
				switch {
				case err != nil || res == nil:
					s.RenewalFailure = "request_failed"
				case res.Status == "pending":
					s.RenewalFailure = "pending"
				case res.NatsJWT == "" || res.NatsUserSeed == "":
					s.RenewalFailure = "missing_credentials"
				default:
					s.RenewalFailure = ""
				}
			})
			return
		}
		if res == nil {
			status.Registration("error", false, err)
			return
		}
		credentials := (cfg.NatsJWT != "" && cfg.NatsUserSeed != "") || (res.NatsJWT != "" && res.NatsUserSeed != "")
		status.Registration(res.Status, credentials, err)
	}

	return func(ctx context.Context) (*workerregistry.RegisterResponse, error) {
		if acquired.Load() && status != nil {
			return regClient.RegisterFull(ctx, map[string]any{"renew_node_id": *nodeID, "token": cfg.RegistrationToken})
		}
		return regClient.RegisterFull(ctx, registrationBody)
	}
}
