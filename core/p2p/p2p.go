//go:build p2p
// +build p2p

package p2p

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/mudler/LocalAI/pkg/utils"
	"github.com/mudler/edgevpn/pkg/node"
	"github.com/mudler/edgevpn/pkg/protocol"
	"github.com/mudler/edgevpn/pkg/types"
	"github.com/phayes/freeport"

	"github.com/ipfs/go-log"
	"github.com/mudler/edgevpn/pkg/config"
	"github.com/mudler/edgevpn/pkg/services"
	zlog "github.com/rs/zerolog/log"

	"github.com/mudler/edgevpn/pkg/logger"
)

func GenerateToken() string {
	// Generates a new config and exit
	newData := node.GenerateNewConnectionData(900)
	return newData.Base64()
}

func allocateLocalService(ctx context.Context, node *node.Node, listenAddr, service string) error {

	zlog.Info().Msgf("Allocating service '%s' on: %s", service, listenAddr)
	// Open local port for listening
	l, err := net.Listen("tcp", listenAddr)
	if err != nil {
		zlog.Error().Err(err).Msg("Error listening")
		return err
	}
	//	ll.Info("Binding local port on", srcaddr)

	ledger, _ := node.Ledger()

	// Announce ourselves so nodes accepts our connection
	ledger.Announce(
		ctx,
		10*time.Second,
		func() {
			// Retrieve current ID for ip in the blockchain
			_, found := ledger.GetKey(protocol.UsersLedgerKey, node.Host().ID().String())
			// If mismatch, update the blockchain
			if !found {
				updatedMap := map[string]interface{}{}
				updatedMap[node.Host().ID().String()] = &types.User{
					PeerID:    node.Host().ID().String(),
					Timestamp: time.Now().String(),
				}
				ledger.Add(protocol.UsersLedgerKey, updatedMap)
			}
		},
	)

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

			//	ll.Info("New connection from", l.Addr().String())
			// Handle connections in a new goroutine, forwarding to the p2p service
			go func() {
				// Retrieve current ID for ip in the blockchain
				existingValue, found := ledger.GetKey(protocol.ServicesLedgerKey, service)
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
					zlog.Error().Msg("cannot open stream peer")

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
				//	ll.Infof("(service %s) Done handling %s", serviceID, l.Addr().String())
			}()
		}
	}

}

func copyStream(closer chan struct{}, dst io.Writer, src io.Reader) {
	defer func() { closer <- struct{}{} }() // connection is closed, send signal to stop proxy
	io.Copy(dst, src)
}

// This is the main of the server (which keeps the env variable updated)
// This starts a goroutine that keeps LLAMACPP_GRPC_SERVERS updated with the discovered services
func LLamaCPPRPCServerDiscoverer(ctx context.Context, token string) error {
	tunnels, err := discoveryTunnels(ctx, token)
	if err != nil {
		return err
	}

	go func() {
		totalTunnels := []string{}
		for {
			select {
			case <-ctx.Done():
				zlog.Error().Msg("Discoverer stopped")
				return
			case tunnel := <-tunnels:

				totalTunnels = append(totalTunnels, tunnel)
				os.Setenv("LLAMACPP_GRPC_SERVERS", strings.Join(totalTunnels, ","))
				zlog.Debug().Msgf("setting LLAMACPP_GRPC_SERVERS to %s", strings.Join(totalTunnels, ","))
			}
		}
	}()

	return nil
}

func discoveryTunnels(ctx context.Context, token string) (chan string, error) {
	tunnels := make(chan string)

	nodeOpts, err := newNodeOpts(token)
	if err != nil {
		return nil, err
	}

	n, err := node.New(nodeOpts...)
	if err != nil {
		return nil, fmt.Errorf("creating a new node: %w", err)
	}
	err = n.Start(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating a new node: %w", err)
	}
	ledger, err := n.Ledger()
	if err != nil {
		return nil, fmt.Errorf("creating a new node: %w", err)
	}

	// get new services, allocate and return to the channel
	go func() {
		emitted := map[string]bool{}
		for {
			select {
			case <-ctx.Done():
				zlog.Error().Msg("Discoverer stopped")
				return
			default:
				time.Sleep(5 * time.Second)
				zlog.Debug().Msg("Searching for workers")

				data := ledger.LastBlock().Storage["services_localai"]
				for k := range data {
					zlog.Info().Msgf("Found worker %s", k)
					if _, found := emitted[k]; !found {
						emitted[k] = true
						//discoveredPeers <- k
						port, err := freeport.GetFreePort()
						if err != nil {
							fmt.Print(err)
						}
						tunnelAddress := fmt.Sprintf("127.0.0.1:%d", port)
						go allocateLocalService(ctx, n, tunnelAddress, k)
						tunnels <- tunnelAddress
					}
				}
			}
		}
	}()

	return tunnels, err
}

// This is the P2P worker main
func BindLLamaCPPWorker(ctx context.Context, host, port, token string) error {
	llger := logger.New(log.LevelFatal)

	nodeOpts, err := newNodeOpts(token)
	if err != nil {
		return err
	}
	// generate a random string for the name
	name := utils.RandString(10)

	// Register the service
	nodeOpts = append(nodeOpts,
		services.RegisterService(llger, time.Duration(60)*time.Second, name, fmt.Sprintf("%s:%s", host, port))...)
	n, err := node.New(nodeOpts...)
	if err != nil {
		return fmt.Errorf("creating a new node: %w", err)
	}

	err = n.Start(ctx)
	if err != nil {
		return fmt.Errorf("creating a new node: %w", err)
	}

	ledger, err := n.Ledger()
	if err != nil {
		return fmt.Errorf("creating a new node: %w", err)
	}

	ledger.Announce(
		ctx,
		10*time.Second,
		func() {
			// Retrieve current ID for ip in the blockchain
			_, found := ledger.GetKey("services_localai", name)
			// If mismatch, update the blockchain
			if !found {
				updatedMap := map[string]interface{}{}
				updatedMap[name] = "p2p"
				ledger.Add("services_localai", updatedMap)
			}
		},
	)

	return err
}

func newNodeOpts(token string) ([]node.Option, error) {
	llger := logger.New(log.LevelFatal)
	defaultInterval := 10 * time.Second

	loglevel := "info"

	c := config.Config{
		Limit: config.ResourceLimit{
			Enable:   true,
			MaxConns: 100,
		},
		NetworkToken:   token,
		LowProfile:     false,
		LogLevel:       loglevel,
		Libp2pLogLevel: "fatal",
		Ledger: config.Ledger{
			SyncInterval:     defaultInterval,
			AnnounceInterval: defaultInterval,
		},
		NAT: config.NAT{
			Service:           true,
			Map:               true,
			RateLimit:         true,
			RateLimitGlobal:   10,
			RateLimitPeer:     10,
			RateLimitInterval: defaultInterval,
		},
		Discovery: config.Discovery{
			DHT:      true,
			MDNS:     true,
			Interval: 30 * time.Second,
		},
		Connection: config.Connection{
			HolePunch:      true,
			AutoRelay:      true,
			MaxConnections: 100,
		},
	}

	nodeOpts, _, err := c.ToOpts(llger)
	if err != nil {
		return nil, fmt.Errorf("parsing options: %w", err)
	}

	nodeOpts = append(nodeOpts, services.Alive(30*time.Second, 900*time.Second, 15*time.Minute)...)

	return nodeOpts, nil
}
