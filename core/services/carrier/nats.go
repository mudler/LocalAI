package carrier

import (
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/storage"
)

// NATSClient is the connection a NATS set is built over. *messaging.Client
// satisfies it.
type NATSClient interface {
	messaging.MessagingClient
	OnReconnect(func())
}

// NATSOptions is what NewNATSSet needs besides the connection.
type NATSOptions struct {
	Client NATSClient
	// Epoch is the epoch of the cluster row the set is built for.
	Epoch int64

	// Registry finds the nodes that hold a model, for the verbs that fan out.
	Registry nodes.ModelLocator
	// InstallTimeout and UpgradeTimeout bound the install and upgrade requests.
	InstallTimeout, UpgradeTimeout time.Duration

	// Token is the registration token. It is the bearer credential of the
	// gRPC clients and of the file transfer to a worker.
	Token string

	// S3Staging selects the file stager: shared object storage with a NATS
	// request to the worker when true, a direct HTTP transfer to the worker
	// when false.
	S3Staging bool
	// FileManager is the object storage client of the S3 stager.
	FileManager *storage.FileManager
	// HTTPAddrFor returns the address of a worker's file transfer server, for
	// the HTTP stager.
	HTTPAddrFor func(nodeID string) (string, error)
}

// NewNATSSet builds the set of seam implementations that run over NATS for
// the control plane and over a direct dial to the worker for gRPC and HTTP.
func NewNATSSet(o NATSOptions) (*Set, error) {
	if o.Client == nil {
		return nil, fmt.Errorf("NATS set needs a connection")
	}
	dial := nodes.DirectWorkerNetDialer()

	var files FileCarrier
	if o.S3Staging {
		files = nodes.NewS3NATSFileStager(o.FileManager, o.Client)
	} else {
		files = nodes.NewHTTPFileStager(o.HTTPAddrFor, o.Token, dial)
	}

	set := &Set{
		Name:        cluster.CarrierNATS,
		Epoch:       o.Epoch,
		Broadcaster: o.Client,
		OnReconnect: o.Client.OnReconnect,
		WorkQueue:   messaging.NewNATSWorkQueue(o.Client),
		Commands:    nodes.NewRemoteUnloaderAdapter(o.Registry, o.Client, o.InstallTimeout, o.UpgradeTimeout),
		Files:       files,
		Clients:     nodes.NewTokenClientFactory(o.Token),
		Dialer:      dial,
		Agents:      nodes.NewNATSAgentControl(o.Client),
		Close:       o.Client.Close,
	}
	if err := set.Validate(); err != nil {
		return nil, err
	}
	return set, nil
}
