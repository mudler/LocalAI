package application

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/core/services/carrier"
	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/storage"
	"github.com/mudler/LocalAI/core/services/tunnel"
)

// natsProbeWait is how long the check of a NATS address waits for the server.
const natsProbeWait = 3 * time.Second

// natsRuntime is the NATS server of the cluster as this replica uses it: the
// address in the cluster settings, the connection of the NATS bus and of the
// direct route, and what a worker is handed to reach the server. It is the one
// place of the application that opens a NATS connection; everything above it
// holds the holders of package carrier.
type natsRuntime struct {
	cfg        *config.ApplicationConfig
	settings   *cluster.SettingsStore
	registry   *nodes.NodeRegistry
	instanceID string
	clusterReg *cluster.Registry
	peerPool   *tunnel.PeerPool

	// busNATS is the connection of the NATS bus of the broadcaster holder, nil
	// when no NATS server is configured. The direct route shares it, so that a
	// replica holds one NATS connection.
	busNATS *messaging.Client
}

// natsURL returns the NATS server of the cluster: the stored setting, else the
// flag of this replica.
func (rt *natsRuntime) natsURL(ctx context.Context) (string, error) {
	v, ok, err := rt.settings.Get(ctx, cluster.SettingNATSURL)
	if err != nil {
		return "", err
	}
	if ok && v != "" {
		return v, nil
	}
	return rt.cfg.Distributed.NatsURL, nil
}

// WorkerURL is the address of the NATS server that workers are told to use: the
// stored address for workers, else the address of the frontends, else the flag of
// this replica. A worker may reach the server by another name than a frontend
// does. It is empty when the deployment has no NATS.
func (rt *natsRuntime) WorkerURL(ctx context.Context) (string, error) {
	v, ok, err := rt.settings.Get(ctx, cluster.SettingNATSWorkerURL)
	if err != nil {
		return "", err
	}
	if ok && v != "" {
		return v, nil
	}
	return rt.natsURL(ctx)
}

// CAPEM is the private CA of the NATS server as PEM, or empty when the server has
// none. A worker is handed it, so that it needs no file of its own to verify the
// server.
func (rt *natsRuntime) CAPEM() (string, error) {
	path := rt.cfg.Distributed.NatsTLSCA
	if path == "" {
		return "", nil
	}
	// #nosec G304 -- the operator set this path for the CA of the NATS server.
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading the NATS CA to hand to workers: %w", err)
	}
	return string(raw), nil
}

// ClientTLS says that this replica connects to NATS with a client certificate. A
// worker cannot be handed such a file, so it needs its own.
func (rt *natsRuntime) ClientTLS() bool {
	d := rt.cfg.Distributed
	return d.NatsTLSCert != "" || d.NatsTLSKey != ""
}

// connectURL is the address to dial for target. The cluster stores the address
// without a credential. When the flag of this replica names the same server, the
// flag is dialled, because it is where this replica keeps its user name,
// password or token.
func (rt *natsRuntime) connectURL(target string) string {
	if flag := rt.cfg.Distributed.NatsURL; flag != "" && cluster.PublicNATSURL(flag) == target {
		return flag
	}
	return target
}

// openBusNATS opens the connection of the NATS bus when a NATS server is
// configured for the cluster (the setting nats.url, else the flag of this
// replica), and returns the bus. It returns nil when none is configured. It
// does not wait for the server: a frontend may start before its broker, and it
// is not ready until it reaches it (Broadcaster.Unready).
func (rt *natsRuntime) openBusNATS(ctx context.Context) (*carrier.Bus, error) {
	url, err := rt.natsURL(ctx)
	if err != nil {
		return nil, err
	}
	if url == "" {
		return nil, nil
	}
	client, err := rt.natsClientAt(ctx, rt.connectURL(cluster.PublicNATSURL(url)), 0)
	if err != nil {
		return nil, err
	}
	rt.busNATS = client
	xlog.Info("Broadcasts use the NATS server of the cluster when it is reachable, and PostgreSQL otherwise", "url", cluster.PublicNATSURL(url))
	return &carrier.Bus{
		Name:        carrier.BusNATS,
		Broadcaster: client,
		OnReconnect: client.OnReconnect,
		Connected:   client.IsConnected,
		Close:       client.Close,
	}, nil
}

// natsClientAt connects to the NATS server at url with the credentials of this
// replica. With a wait it returns only once the server has answered.
func (rt *natsRuntime) natsClientAt(ctx context.Context, url string, wait time.Duration) (*messaging.Client, error) {
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
		if err := waitNATS(ctx, client, url, wait); err != nil {
			client.Close()
			return nil, err
		}
	}
	return client, nil
}

// waitNATS waits until client is connected, for at most wait.
func waitNATS(ctx context.Context, client *messaging.Client, url string, wait time.Duration) error {
	deadline := time.Now().Add(wait)
	for !client.IsConnected() {
		if time.Now().After(deadline) || ctx.Err() != nil {
			return fmt.Errorf("the NATS server at %s did not answer within %s: check the address and the credentials", cluster.PublicNATSURL(url), wait)
		}
		select {
		case <-ctx.Done():
		case <-time.After(50 * time.Millisecond):
		}
	}
	return nil
}

// CheckNATS says whether this replica reaches the NATS server at url with its own
// credentials. It opens a connection and closes it.
func (rt *natsRuntime) CheckNATS(ctx context.Context, url string) error {
	client, err := rt.natsClientAt(ctx, rt.connectURL(url), natsProbeWait)
	if err != nil {
		return err
	}
	client.Close()
	return nil
}

// directRoutes builds the direct route over the connection of the NATS bus: the
// control verbs and the agent RPC over NATS, the gRPC and HTTP connections by a
// direct dial of the worker. It returns nil when no NATS server is configured.
// The route shares the connection, so its Close leaves it open.
func (rt *natsRuntime) directRoutes(fileMgr *storage.FileManager) (*carrier.WorkerRoute, error) {
	if rt.busNATS == nil {
		return nil, nil
	}
	d := rt.cfg.Distributed
	route, err := carrier.NewNATSRoutes(carrier.NATSOptions{
		Client:         rt.busNATS,
		Registry:       rt.registry,
		InstallTimeout: d.BackendInstallTimeoutOrDefault(),
		UpgradeTimeout: d.BackendUpgradeTimeoutOrDefault(),
		Token:          d.WorkerAuthToken(),
		S3Staging:      d.StorageURL != "",
		FileManager:    fileMgr,
		ExpectedSHA256: managedFileSHA256(rt.cfg),
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
		Shared: true,
	})
	if err != nil {
		return nil, err
	}
	return route, nil
}

// Bus returns the broadcast bus of this replica: NATS while it reaches the
// NATS server of the cluster, PostgreSQL otherwise. It reads the connection of
// this replica, not a row of the cluster.
func (rt *natsRuntime) Bus() carrier.BusState {
	s := carrier.BusState{Bus: carrier.BusPostgres}
	if rt.busNATS == nil {
		return s
	}
	s.NATSConfigured = true
	s.NATSConnected = rt.busNATS.IsConnected()
	if s.NATSConnected {
		s.Bus = carrier.BusNATS
	}
	return s
}

// peerReplies builds request and reply between the frontends and chat workers
// over the peer link, the one transport for it on every deployment: the chat
// start goes to a live chat worker that the node list names, and the replay and
// the jobs go to the process that the caller names. local holds the handlers of
// this process, which the relay of the peer link serves.
func (rt *natsRuntime) peerReplies(local *tunnel.Replies) (messaging.ProcessReplies, error) {
	return carrier.NewPeerReplies(carrier.PeerRepliesOptions{
		Self:  rt.instanceID,
		Peers: rt.peerPool,
		Local: local,
		Queues: map[string]carrier.QueueMembers{
			messaging.SubjectChatStart: chatWorkerInstances(rt.registry, rt.liveInstances),
		},
	})
}

// Replicas lists the live frontend replicas.
func (rt *natsRuntime) Replicas(ctx context.Context) ([]cluster.LiveInstance, error) {
	return rt.clusterReg.ListLive(ctx, cluster.InstanceLiveness)
}

// liveInstances lists the ids of the live replicas.
func (rt *natsRuntime) liveInstances(ctx context.Context) ([]string, error) {
	live, err := rt.clusterReg.ListLive(ctx, cluster.InstanceLiveness)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(live))
	for _, inst := range live {
		ids = append(ids, inst.ID)
	}
	return ids, nil
}

// managedFileSHA256 returns the sha256 that the gallery recorded for a model
// file, so that the S3 stager makes a worker replace a cached copy that a
// model upgrade changed. It returns "" for a file the gallery does not manage.
func managedFileSHA256(cfg *config.ApplicationConfig) func(localPath string) string {
	return func(localPath string) string {
		if cfg.SystemState == nil {
			return ""
		}
		return gallery.ManagedFileSHA256(cfg.SystemState.Model.ModelsPath, localPath)
	}
}
