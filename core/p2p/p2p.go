//go:build p2p
// +build p2p

package p2p

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/ipfs/go-log"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	cliP2P "github.com/mudler/LocalAI/core/cli/p2p"
	"github.com/mudler/LocalAI/pkg/utils"
	p2pConfig "github.com/mudler/edgevpn/pkg/config"
	"github.com/mudler/edgevpn/pkg/node"
	"github.com/mudler/edgevpn/pkg/protocol"
	"github.com/mudler/edgevpn/pkg/services"
	"github.com/mudler/edgevpn/pkg/types"
	eutils "github.com/mudler/edgevpn/pkg/utils"
	"github.com/phayes/freeport"
	zlog "github.com/rs/zerolog/log"

	"github.com/mudler/edgevpn/pkg/logger"
)

const DefaultInterval = 10 * time.Second

func GenerateNewConnectionData(DHTInterval, OTPInterval int, privkey string, peerguardMode bool) (*node.YAMLConnectionConfig, error) {
	maxMessSize := 20 << 20 // 20MB
	keyLength := 43
	if DHTInterval == 0 {
		DHTInterval = 360
	}
	if OTPInterval == 0 {
		OTPInterval = 9000
	}

	connectionConfig := node.YAMLConnectionConfig{
		MaxMessageSize: maxMessSize,
		RoomName:       eutils.RandStringRunes(keyLength),
		Rendezvous:     eutils.RandStringRunes(keyLength),
		MDNS:           eutils.RandStringRunes(keyLength),
		OTP: node.OTP{
			DHT: node.OTPConfig{
				Key:      eutils.RandStringRunes(keyLength),
				Interval: DHTInterval,
				Length:   keyLength,
			},
			Crypto: node.OTPConfig{
				Key:      eutils.RandStringRunes(keyLength),
				Interval: OTPInterval,
				Length:   keyLength,
			},
		},
	}

	if peerguardMode {
		key, err := crypto.UnmarshalPrivateKey([]byte(privkey))
		if err != nil {
			return &connectionConfig, err
		}
		pid, err := peer.IDFromPublicKey(key.GetPublic())
		if err != nil {
			return &connectionConfig, err
		}

		connectionConfig.TrustedPeerIDS = []string{
			pid.String(),
		}
		connectionConfig.ProtectedStoreKeys = []string{
			"trustzone",
			"trustzoneAuth",
		}

	}
	return &connectionConfig, nil
}

func IsP2PEnabled() bool {
	return true
}

func nodeID(s string) string {
	hostname, _ := os.Hostname()
	return fmt.Sprintf("%s-%s", hostname, s)
}

func nodeAnnounce(ctx context.Context, node *node.Node) {
	ledger, _ := node.Ledger()

	// Announce ourselves so nodes accepts our connection
	ledger.Announce(
		ctx,
		10*time.Second,
		func() {
			updatedMap := map[string]interface{}{}
			updatedMap[node.Host().ID().String()] = &types.User{
				PeerID:    node.Host().ID().String(),
				Timestamp: time.Now().String(),
			}
			ledger.Add(protocol.UsersLedgerKey, updatedMap)
		},
	)
}

func proxyP2PConnection(ctx context.Context, node *node.Node, serviceID string, conn net.Conn) {
	ledger, _ := node.Ledger()
	// Retrieve current ID for ip in the blockchain
	existingValue, found := ledger.GetKey(protocol.ServicesLedgerKey, serviceID)
	service := &types.Service{}
	existingValue.Unmarshal(service)
	// If mismatch, update the blockchain
	if !found {
		zlog.Error().Msg("Service not found on blockchain")
		conn.Close()
		//	ll.Debugf("service '%s' not found on blockchain", serviceID)
		return
	}

	// Decode the Peer
	d, err := peer.Decode(service.PeerID)
	if err != nil {
		zlog.Error().Msg("cannot decode peer")

		conn.Close()
		//	ll.Debugf("could not decode peer '%s'", service.PeerID)
		return
	}

	// Open a stream
	stream, err := node.Host().NewStream(ctx, d, protocol.ServiceProtocol.ID())
	if err != nil {
		zlog.Error().Err(err).Msg("cannot open stream peer")

		conn.Close()
		//	ll.Debugf("could not open stream '%s'", err.Error())
		return
	}
	//	ll.Debugf("(service %s) Redirecting", serviceID, l.Addr().String())
	zlog.Info().Msgf("Redirecting %s to %s", conn.LocalAddr().String(), stream.Conn().RemoteMultiaddr().String())
	closer := make(chan struct{}, 2)
	go copyStream(closer, stream, conn)
	go copyStream(closer, conn, stream)
	<-closer

	stream.Close()
	conn.Close()
}

func allocateLocalService(ctx context.Context, node *node.Node, listenAddr, service string) error {
	zlog.Info().Msgf("Allocating service '%s' on: %s", service, listenAddr)
	// Open local port for listening
	l, err := net.Listen("tcp", listenAddr)
	if err != nil {
		zlog.Error().Err(err).Msg("Error listening")
		return err
	}
	go func() {
		<-ctx.Done()
		l.Close()
	}()

	nodeAnnounce(ctx, node)

	defer l.Close()
	for {
		select {
		case <-ctx.Done():
			return errors.New("context canceled")
		default:
			zlog.Debug().Msg("New for connection")
			// Listen for an incoming connection.
			conn, err := l.Accept()
			if err != nil {
				fmt.Println("Error accepting: ", err.Error())
				continue
			}

			// Handle connections in a new goroutine, forwarding to the p2p service
			go func() {
				proxyP2PConnection(ctx, node, service, conn)
			}()
		}
	}

}

// This is the main of the server (which keeps the env variable updated)
// This starts a goroutine that keeps LLAMACPP_GRPC_SERVERS updated with the discovered services
func ServiceDiscoverer(ctx context.Context, n *node.Node, servicesID string, discoveryFunc func(serviceID string, node NodeData), allocate bool) error {
	if servicesID == "" {
		servicesID = defaultServicesID
	}
	tunnels, err := discoveryTunnels(ctx, n, servicesID, allocate)
	if err != nil {
		return err
	}
	// TODO: discoveryTunnels should return all the nodes that are available?
	// In this way we updated availableNodes here instead of appending
	// e.g. we have a LastSeen field in NodeData that is updated in discoveryTunnels
	// each time the node is seen
	// In this case the below function should be idempotent and just keep track of the nodes
	go func() {
		for {
			select {
			case <-ctx.Done():
				zlog.Error().Msg("Discoverer stopped")
				return
			case tunnel := <-tunnels:
				AddNode(servicesID, tunnel)
				if discoveryFunc != nil {
					discoveryFunc(servicesID, tunnel)
				}
			}
		}
	}()

	return nil
}

func discoveryTunnels(ctx context.Context, n *node.Node, servicesID string, allocate bool) (chan NodeData, error) {
	tunnels := make(chan NodeData)

	ledger, err := n.Ledger()
	if err != nil {
		return nil, fmt.Errorf("getting the ledger: %w", err)
	}
	// get new services, allocate and return to the channel

	// TODO:
	// a function ensureServices that:
	// - starts a service if not started, if the worker is Online
	// - checks that workers are Online, if not cancel the context of allocateLocalService
	// - discoveryTunnels should return all the nodes and addresses associated with it
	// - the caller should take now care of the fact that we are always returning fresh informations
	go func() {
		for {
			select {
			case <-ctx.Done():
				zlog.Error().Msg("Discoverer stopped")
				return
			default:
				time.Sleep(5 * time.Second)

				data := ledger.LastBlock().Storage[servicesID]

				if logLevel == logLevelDebug {
					// We want to surface this debugging data only if p2p logging is set to debug
					// (and not generally the whole application, as this can be really noisy)
					zlog.Debug().Any("data", ledger.LastBlock().Storage).Msg("Ledger data")
				}

				for k, v := range data {
					// New worker found in the ledger data as k (worker id)
					nd := &NodeData{}
					if err := v.Unmarshal(nd); err != nil {
						zlog.Error().Msg("cannot unmarshal node data")
						continue
					}
					ensureService(ctx, n, nd, k, allocate)
					muservice.Lock()
					if _, ok := service[nd.Name]; ok {
						tunnels <- service[nd.Name].NodeData
					}
					muservice.Unlock()
				}
			}
		}
	}()

	return tunnels, err
}

type nodeServiceData struct {
	NodeData   NodeData
	CancelFunc context.CancelFunc
}

var service = map[string]nodeServiceData{}
var muservice sync.Mutex

func ensureService(ctx context.Context, n *node.Node, nd *NodeData, sserv string, allocate bool) {
	muservice.Lock()
	defer muservice.Unlock()
	nd.ServiceID = sserv
	if ndService, found := service[nd.Name]; !found {
		if !nd.IsOnline() {
			// if node is offline and not present, do nothing
			// Node nd.ID is offline
			return
		}

		newCtxm, cancel := context.WithCancel(ctx)
		if allocate {
			// Start the service
			port, err := freeport.GetFreePort()
			if err != nil {
				zlog.Error().Err(err).Msgf("Could not allocate a free port for %s", nd.ID)
				return
			}

			tunnelAddress := fmt.Sprintf("127.0.0.1:%d", port)
			nd.TunnelAddress = tunnelAddress
			go allocateLocalService(newCtxm, n, tunnelAddress, sserv)
			zlog.Debug().Msgf("Starting service %s on %s", sserv, tunnelAddress)
		}
		service[nd.Name] = nodeServiceData{
			NodeData:   *nd,
			CancelFunc: cancel,
		}
	} else {
		// Check if the service is still alive
		// if not cancel the context
		if !nd.IsOnline() && !ndService.NodeData.IsOnline() {
			ndService.CancelFunc()
			delete(service, nd.Name)
			zlog.Info().Msgf("Node %s is offline, deleting", nd.ID)
		} else if nd.IsOnline() {
			// update last seen inside service
			nd.TunnelAddress = ndService.NodeData.TunnelAddress
			service[nd.Name] = nodeServiceData{
				NodeData:   *nd,
				CancelFunc: ndService.CancelFunc,
			}
		}
	}
}

// This is the P2P worker main
func ExposeService(ctx context.Context, p2pCfg p2pConfig.Config, host, port, servicesID string) (*node.Node, error) {
	llger := logger.New(log.LevelFatal)
	nodeOpts, _, err := p2pCfg.ToOpts(llger)
	if err != nil {
		return nil, fmt.Errorf("parsing config for new node: %w", err)
	}
	nodeOpts = append(nodeOpts, node.FromBase64(true, p2pCfg.Discovery.DHT, p2pCfg.NetworkToken, nil, nil))

	nodeOpts = append(nodeOpts, services.Alive(30*time.Second, 900*time.Second, 15*time.Minute)...)

	// generate a random string for the name
	name := utils.RandString(10)

	// Register the service
	nodeOpts = append(nodeOpts,
		services.RegisterService(llger, time.Duration(60)*time.Second, name, fmt.Sprintf("%s:%s", host, port))...)
	n, err := node.New(nodeOpts...)
	if err != nil {
		return nil, fmt.Errorf("creating a new node: %w", err)
	}

	err = n.Start(ctx)
	if err != nil {
		return n, fmt.Errorf("creating a new node: %w", err)
	}

	ledger, err := n.Ledger()
	if err != nil {
		return n, fmt.Errorf("creating a new node: %w", err)
	}

	if servicesID == "" {
		servicesID = defaultServicesID
	}
	ledger.Announce(
		ctx,
		20*time.Second,
		func() {
			updatedMap := map[string]interface{}{}
			updatedMap[name] = &NodeData{
				Name:     name,
				LastSeen: time.Now(),
				ID:       nodeID(name),
			}
			ledger.Add(servicesID, updatedMap)
		},
	)

	return n, err
}

func NewNode(p2pCfg p2pConfig.Config) (*node.Node, error) {
	llger := logger.New(log.LevelFatal)
	nodeOpts, _, err := p2pCfg.ToOpts(llger)
	if err != nil {
		return nil, fmt.Errorf("parsing config for new node: %w", err)
	}
	nodeOpts = append(nodeOpts, node.FromBase64(true, p2pCfg.Discovery.DHT, p2pCfg.NetworkToken, nil, nil))

	nodeOpts = append(nodeOpts, services.Alive(30*time.Second, 900*time.Second, 15*time.Minute)...)

	n, err := node.New(nodeOpts...)
	if err != nil {
		return nil, fmt.Errorf("creating a new node: %w", err)
	}

	return n, nil
}

func NewP2PConfig(p2pCommonFlags cliP2P.P2PCommonFlags) p2pConfig.Config {
	pa := p2pCommonFlags.Peer2PeerAuthProvders
	d := map[string]map[string]interface{}{}
	json.Unmarshal([]byte(pa), &d)

	c := p2pConfig.Config{
		ListenMaddrs:      p2pCommonFlags.Peer2PeerListenAddrs,
		DHTAnnounceMaddrs: utils.StringsToMultiAddr(p2pCommonFlags.Peer2PeerDHTAnnounceAddrs),
		Limit: p2pConfig.ResourceLimit{
			Enable:   p2pCommonFlags.Peer2PeerLimit,
			MaxConns: 100,
		},
		LowProfile: false,
		LogLevel:   logLevel,
		Ledger: p2pConfig.Ledger{
			SyncInterval:     DefaultInterval,
			AnnounceInterval: DefaultInterval,
		},
		NAT: p2pConfig.NAT{
			Service:           true,
			Map:               true,
			RateLimit:         true,
			RateLimitGlobal:   100,
			RateLimitPeer:     100,
			RateLimitInterval: DefaultInterval,
		},
		Discovery: p2pConfig.Discovery{
			DHT:            !p2pCommonFlags.Peer2PeerNoDHT,
			MDNS:           true,
			Interval:       DefaultInterval,
			BootstrapPeers: p2pCommonFlags.Peer2PeerBootAddrs,
		},
		Connection: p2pConfig.Connection{
			HolePunch:      true,
			AutoRelay:      true,
			MaxConnections: 1000,
		},
		PeerGuard: p2pConfig.PeerGuard{
			Enable: p2pCommonFlags.Peer2PeerUsePeerguard,

			// Default from edgevpn
			SyncInterval:  120 * time.Second,
			AuthProviders: d,
		},
	}

	privkey := p2pCommonFlags.Peer2PeerPrivkey
	if privkey != "" {
		c.Privkey = []byte(privkey)
	}

	libp2ploglevel := p2pCommonFlags.Peer2PeerLibLoglevel
	if libp2ploglevel == "" {
		libp2ploglevel = "fatal"
	}
	c.Libp2pLogLevel = libp2ploglevel

	return c
}

func copyStream(closer chan struct{}, dst io.Writer, src io.Reader) {
	defer func() { closer <- struct{}{} }() // connection is closed, send signal to stop proxy
	io.Copy(dst, src)
}
