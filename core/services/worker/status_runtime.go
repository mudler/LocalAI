// SPDX-License-Identifier: MIT
package worker

import (
	"context"
	"net"
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
