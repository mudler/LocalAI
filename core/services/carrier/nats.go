package carrier

import (
	"fmt"
	"time"

	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/core/services/storage"
)

// NATSClient is the connection the direct route is built over. *messaging.Client
// satisfies it.
type NATSClient interface {
	messaging.MessagingClient
	OnReconnect(func())
}

// NATSOptions is what NewNATSRoutes needs besides the connection.
type NATSOptions struct {
	Client NATSClient

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
	// ExpectedSHA256 gives the S3 stager the sha256 a staged file must have
	// (nodes.S3FileStager.ExpectedSHA256). Nil sends none.
	ExpectedSHA256 func(localPath string) string
	// HTTPAddrFor returns the address of a worker's file transfer server, for
	// the HTTP stager.
	HTTPAddrFor func(nodeID string) (string, error)

	// NodeHasAddress says whether a worker registered an address that can be
	// dialled. With it, a worker that registered none (it holds a tunnel) is not
	// dialled on a loopback address, which would name a port on the frontend and
	// make a healthy backend look dead. Without it every address is dialled.
	NodeHasAddress func(nodeID string) (bool, error)

	// Shared says that the connection belongs to the caller: the NATS bus of the
	// broadcaster holder uses it too. Close then leaves it open.
	Shared bool
}

// NewNATSRoutes builds the direct route: the control verbs and the agent RPC
// over NATS, and the gRPC and HTTP connections by a direct dial of the address
// of the worker. Close closes the NATS connection, unless o.Shared.
func NewNATSRoutes(o NATSOptions) (*WorkerRoute, error) {
	if o.Client == nil {
		return nil, fmt.Errorf("the direct route needs a NATS connection")
	}
	dial := nodes.DirectWorkerNetDialer()

	var files FileCarrier
	if o.S3Staging {
		s3 := nodes.NewS3NATSFileStager(o.FileManager, o.Client)
		s3.ExpectedSHA256 = o.ExpectedSHA256
		files = s3
	} else {
		files = nodes.NewHTTPFileStager(o.HTTPAddrFor, o.Token, dial)
	}

	var clients nodes.BackendClientFactory = nodes.NewTokenClientFactory(o.Token)
	if o.NodeHasAddress != nil {
		clients = nodes.NewGuardedDirectClientFactory(o.Token, o.NodeHasAddress)
	}

	route := &WorkerRoute{
		Kind:     nodes.RouteDirect,
		Commands: nodes.NewRemoteUnloaderAdapter(o.Registry, o.Client, o.InstallTimeout, o.UpgradeTimeout),
		Files:    files,
		Clients:  clients,
		Dialer:   dial,
		Agents:   nodes.NewNATSAgentControl(o.Client),
	}
	if !o.Shared {
		route.Close = o.Client.Close
	}
	// The HTTP stager keeps one client for each node, and nothing else ever drops
	// it.
	if stager, ok := files.(*nodes.HTTPFileStager); ok {
		route.ForgetNode = stager.ForgetNode
	}
	if err := route.Validate(); err != nil {
		return nil, err
	}
	return route, nil
}
