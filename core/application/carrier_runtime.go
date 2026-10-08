package application

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/mudler/xlog"
	"gorm.io/gorm"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/carrier"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/jobs"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/storage"
	"github.com/mudler/LocalAI/core/services/tunnel"
	"github.com/mudler/LocalAI/pkg/sanitize"
)

const (
	// natsConnectWait is how long a replica waits for the NATS server when it
	// prepares the carrier for a change. At start-up it does not wait, because a
	// frontend may start before its broker.
	natsConnectWait = 10 * time.Second
	// natsProbeWait is how long the report of what a replica can build waits for
	// the NATS server.
	natsProbeWait = 3 * time.Second
	// availabilityEvery is how often a replica writes what it can build without
	// being asked. A dry run asks, so this only keeps the status view from
	// showing nothing.
	availabilityEvery = 5 * time.Minute
	// availabilityMaxAge is how old a report of what a replica can build may be
	// for a change of carrier to rely on it. A dry run asks the replicas to look
	// again first, so a report older than this is from a replica that did not
	// answer.
	availabilityMaxAge = 30 * time.Second
	// probeWait is how long a dry run waits for the replicas to answer.
	probeWait = 5 * time.Second
	// hintPublishTimeout bounds the publish of a hint.
	hintPublishTimeout = 3 * time.Second
)

// carrierRuntime builds the carrier sets of this replica and reports what it
// could build. It is the one place of the application that names the carriers:
// everything above it holds the holders.
type carrierRuntime struct {
	cfg        *config.ApplicationConfig
	db         *gorm.DB
	settings   *cluster.SettingsStore
	registry   *nodes.NodeRegistry
	fileMgr    *storage.FileManager
	instanceID string
	jobStore   *jobs.JobStore
	clusterReg *cluster.Registry
	tunnels    *tunnel.Registry
	peerPool   *tunnel.PeerPool

	// bus is the broadcaster holder. The work that a set starts uses it, so that
	// its subscriptions follow the sets. It is set before any set starts.
	bus *carrier.Broadcaster
	// active is the pointer the holders read.
	active *atomic.Pointer[carrier.Set]

	probeWake chan struct{}
}

// natsURL returns the NATS server of the cluster: the stored setting, else the
// flag of this replica.
func (rt *carrierRuntime) natsURL(ctx context.Context) (string, error) {
	v, ok, err := rt.settings.Get(ctx, cluster.SettingNATSURL)
	if err != nil {
		return "", err
	}
	if ok && v != "" {
		return v, nil
	}
	return rt.cfg.Distributed.NatsURL, nil
}

// Build builds the set of a carrier for a change. It waits for the carrier to be
// usable, because a replica that reports ready must be.
func (rt *carrierRuntime) Build(ctx context.Context, row cluster.CarrierRow, target cluster.Carrier) (*carrier.Set, error) {
	return rt.build(ctx, row, target, true)
}

func (rt *carrierRuntime) build(ctx context.Context, row cluster.CarrierRow, target cluster.Carrier, wait bool) (*carrier.Set, error) {
	switch target {
	case cluster.CarrierNATS:
		return rt.buildNATS(ctx, row, wait)
	case cluster.CarrierTunnel:
		return rt.buildTunnel(ctx, row)
	}
	return nil, fmt.Errorf("%w: %q", cluster.ErrInvalidCarrier, target)
}

func (rt *carrierRuntime) natsClient(ctx context.Context, wait time.Duration) (*messaging.Client, string, error) {
	url, err := rt.natsURL(ctx)
	if err != nil {
		return nil, "", err
	}
	if url == "" {
		return nil, "", errors.New("no NATS URL is configured: store one in the cluster settings (nats.url) or start the frontend with --nats-url")
	}
	client, err := rt.natsClientAt(ctx, url, wait)
	return client, url, err
}

// natsClientAt connects to the NATS server at url with the credentials of this
// replica. With a wait it returns only once the server has answered.
func (rt *carrierRuntime) natsClientAt(ctx context.Context, url string, wait time.Duration) (*messaging.Client, error) {
	d := rt.cfg.Distributed
	auth := d.NatsAuthConfig()
	if auth.RequireAuth && (auth.ServiceUserJWT == "" || auth.ServiceUserSeed == "") {
		return nil, errors.New("LOCALAI_NATS_REQUIRE_AUTH requires LOCALAI_NATS_SERVICE_JWT and LOCALAI_NATS_SERVICE_SEED")
	}
	// A URL or a credential the client cannot use stops here. A server that is
	// not up does not, unless the caller waits for it: the client keeps trying.
	client, err := messaging.New(url, d.NatsMessagingOptions("", "")...)
	if err != nil {
		return nil, fmt.Errorf("connecting to NATS: %w", err)
	}
	if wait > 0 {
		deadline := time.Now().Add(wait)
		for !client.IsConnected() {
			if time.Now().After(deadline) || ctx.Err() != nil {
				client.Close()
				return nil, fmt.Errorf("the NATS server at %s did not answer within %s: check the address and the credentials", sanitize.URL(url), wait)
			}
			select {
			case <-ctx.Done():
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
	return client, nil
}

// CheckNATS says whether this replica reaches the NATS server at url with its own
// credentials. It opens a connection and closes it.
func (rt *carrierRuntime) CheckNATS(ctx context.Context, url string) error {
	client, err := rt.natsClientAt(ctx, url, natsProbeWait)
	if err != nil {
		return err
	}
	client.Close()
	return nil
}

func (rt *carrierRuntime) buildNATS(ctx context.Context, row cluster.CarrierRow, wait bool) (*carrier.Set, error) {
	var waitFor time.Duration
	if wait {
		waitFor = natsConnectWait
	}
	client, url, err := rt.natsClient(ctx, waitFor)
	if err != nil {
		return nil, err
	}
	d := rt.cfg.Distributed
	set, err := carrier.NewNATSSet(carrier.NATSOptions{
		Client:         client,
		Epoch:          row.Epoch,
		Registry:       rt.registry,
		InstallTimeout: d.BackendInstallTimeoutOrDefault(),
		UpgradeTimeout: d.BackendUpgradeTimeoutOrDefault(),
		Token:          d.RegistrationToken,
		S3Staging:      d.StorageURL != "",
		FileManager:    rt.fileMgr,
		HTTPAddrFor: func(nodeID string) (string, error) {
			node, err := rt.registry.Get(context.Background(), nodeID)
			if err != nil {
				return "", err
			}
			if node.HTTPAddress == "" {
				return "", fmt.Errorf("node %s has no HTTP address for file transfer", nodeID)
			}
			return node.HTTPAddress, nil
		},
		NodeHasAddress: func(nodeID string) (bool, error) {
			node, err := rt.registry.Get(context.Background(), nodeID)
			if err != nil {
				return false, err
			}
			return node.Address != "", nil
		},
	})
	if err != nil {
		client.Close()
		return nil, err
	}
	xlog.Info("Connected to NATS", "url", sanitize.URL(url), "epoch", row.Epoch)
	return set, nil
}

func (rt *carrierRuntime) buildTunnel(ctx context.Context, row cluster.CarrierRow) (*carrier.Set, error) {
	d := rt.cfg.Distributed
	fanout, err := carrier.NewPgbusFanout(ctx, carrier.PgbusOptions{DB: rt.db, DSN: rt.cfg.Auth.DatabaseURL})
	if err != nil {
		return nil, err
	}
	selector := nodes.NewAgentSelector(rt.registry, rt.clusterReg, rt.instanceID)
	set, err := carrier.NewTunnelSet(carrier.TunnelOptions{
		Epoch:          row.Epoch,
		Dialer:         tunnel.NewWorkerDialer(rt.tunnels, rt.peerPool),
		Fanout:         fanout,
		WorkQueue:      jobs.NewClaimQueue(rt.db, fanout.Broadcaster),
		AgentSelector:  selector,
		Registry:       rt.registry,
		InstallTimeout: d.BackendInstallTimeoutOrDefault(),
		UpgradeTimeout: d.BackendUpgradeTimeoutOrDefault(),
		Token:          d.RegistrationToken,
		S3Staging:      d.StorageURL != "",
		FileManager:    rt.fileMgr,
		// While the tunnel is the carrier in use, and while it drains, this replica
		// claims queued work and drives it on agent workers.
		Claims: &carrier.ClaimWork{
			DB: rt.db, Owner: rt.instanceID, Store: rt.jobStore,
			Bus: func() messaging.Broadcaster { return rt.bus },
		},
	})
	if err != nil {
		fanout.Close()
		return nil, err
	}

	return set, nil
}

// availability says, for each carrier, why this replica could not build it now.
func (rt *carrierRuntime) availability(ctx context.Context) map[cluster.Carrier]string {
	out := map[cluster.Carrier]string{}
	cur := rt.active.Load()

	switch {
	case cur != nil && cur.Name == cluster.CarrierNATS:
		out[cluster.CarrierNATS] = ""
	default:
		client, _, err := rt.natsClient(ctx, natsProbeWait)
		if err != nil {
			out[cluster.CarrierNATS] = err.Error()
		} else {
			client.Close()
			out[cluster.CarrierNATS] = ""
		}
	}

	if cur != nil && cur.Name == cluster.CarrierTunnel {
		out[cluster.CarrierTunnel] = ""
	} else if err := carrier.ProbeListen(ctx, rt.cfg.Auth.DatabaseURL); err != nil {
		out[cluster.CarrierTunnel] = err.Error()
	} else {
		out[cluster.CarrierTunnel] = ""
	}
	return out
}

// reportAvailability looks at what this replica can build and writes it.
func (rt *carrierRuntime) reportAvailability(ctx context.Context) {
	probeCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := rt.clusterReg.ReportAvailability(ctx, rt.instanceID, rt.availability(probeCtx)); err != nil && ctx.Err() == nil {
		xlog.Warn("Could not report which carriers this replica can build", "error", err)
	}
}

// RunAvailability reports what this replica can build at start, then when asked
// and every availabilityEvery.
func (rt *carrierRuntime) RunAvailability(ctx context.Context) {
	rt.reportAvailability(ctx)
	ticker := time.NewTicker(availabilityEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-rt.probeWake:
		}
		rt.reportAvailability(ctx)
	}
}

// wakeProbe asks RunAvailability to look now.
func (rt *carrierRuntime) wakeProbe() {
	select {
	case rt.probeWake <- struct{}{}:
	default:
	}
}

// AskReplicas asks every replica, this one included, to look at what it can
// build, and does not wait for the answer.
func (rt *carrierRuntime) AskReplicas(ctx context.Context) {
	rt.wakeProbe()
	pubCtx, cancel := context.WithTimeout(ctx, hintPublishTimeout)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := rt.bus.Publish(messaging.SubjectCarrierProbe, map[string]string{"from": rt.instanceID}); err != nil {
			xlog.Debug("Could not ask the other replicas to look at the carriers", "error", err)
		}
	}()
	select {
	case <-done:
	case <-pubCtx.Done():
	}
}

// ProbeReplicas asks every replica to look at what it can build, and waits until
// the live replicas have answered or probeWait has passed. A replica that did not
// answer shows up as stale in the preflight.
func (rt *carrierRuntime) ProbeReplicas(ctx context.Context) {
	asked := time.Now()
	rt.AskReplicas(ctx)

	deadline := asked.Add(probeWait)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		live, err := rt.clusterReg.ListLive(ctx, cluster.InstanceLiveness)
		if err == nil {
			fresh := true
			for _, in := range live {
				if _, known := in.AvailabilityFor(cluster.CarrierNATS); !known || in.AvailabilityAge > time.Since(asked) {
					fresh = false
					break
				}
			}
			if fresh {
				return
			}
		}
		select {
		case <-ctx.Done():
		case <-time.After(150 * time.Millisecond):
		}
	}
}

// workCounts counts the work that a change of carrier can touch.
type workCounts struct{ db *gorm.DB }

func (w workCounts) InFlight(ctx context.Context) (cluster.InFlight, error) {
	var out cluster.InFlight
	count := func(dst *int, q *gorm.DB) error {
		var n int64
		if err := q.Count(&n).Error; err != nil {
			return err
		}
		*dst = int(n)
		return nil
	}
	db := w.db.WithContext(ctx)
	if err := count(&out.Loads, db.Model(&nodes.NodeModel{}).Where("state IN ?", []string{"loading", "staging"})); err != nil {
		return out, fmt.Errorf("counting the models that load: %w", err)
	}
	if err := count(&out.Jobs, db.Model(&jobs.JobRecord{}).Where("status = ?", "running")); err != nil {
		return out, fmt.Errorf("counting the jobs that run: %w", err)
	}
	if err := count(&out.PendingClaims, db.Model(&jobs.WorkClaim{}).Where("state = ?", jobs.ClaimPending)); err != nil {
		return out, fmt.Errorf("counting the pending claims: %w", err)
	}
	if err := count(&out.ClaimedClaims, db.Model(&jobs.WorkClaim{}).Where("state = ?", jobs.ClaimClaimed)); err != nil {
		return out, fmt.Errorf("counting the claimed claims: %w", err)
	}
	return out, nil
}
