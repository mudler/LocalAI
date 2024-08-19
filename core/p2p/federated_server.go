//go:build p2p
// +build p2p

package p2p

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/mudler/edgevpn/pkg/node"
	"github.com/mudler/edgevpn/pkg/protocol"
	"github.com/mudler/edgevpn/pkg/types"
	"github.com/rs/zerolog/log"
)

func (f *FederatedServer) Start(ctx context.Context) error {

	n, err := NewNode(f.p2ptoken)
	if err != nil {
		return fmt.Errorf("creating a new node: %w", err)
	}
	err = n.Start(ctx)
	if err != nil {
		return fmt.Errorf("creating a new node: %w", err)
	}

	if err := ServiceDiscoverer(ctx, n, f.p2ptoken, f.service, func(servicesID string, tunnel NodeData) {
		log.Debug().Msgf("Discovered node: %s", tunnel.ID)
	}, true); err != nil {
		return err
	}

	return f.proxy(ctx, n)
}

func (fs *FederatedServer) proxy(ctx context.Context, node *node.Node) error {

	log.Info().Msgf("Allocating service '%s' on: %s", fs.service, fs.listenAddr)
	// Open local port for listening
	l, err := net.Listen("tcp", fs.listenAddr)
	if err != nil {
		log.Error().Err(err).Msg("Error listening")
		return err
	}
	//	ll.Info("Binding local port on", srcaddr)
	go func() {
		<-ctx.Done()
		l.Close()
	}()
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

	defer l.Close()
	for {
		select {
		case <-ctx.Done():
			return errors.New("context canceled")
		default:
			log.Debug().Msgf("New connection from %s", l.Addr().String())
			// Listen for an incoming connection.
			conn, err := l.Accept()
			if err != nil {
				fmt.Println("Error accepting: ", err.Error())
				continue
			}

			// Handle connections in a new goroutine, forwarding to the p2p service
			go func() {
				tunnelAddr := ""

				if fs.workerTarget != "" {
					for _, v := range GetAvailableNodes(fs.service) {
						if v.ID == fs.workerTarget {
							tunnelAddr = v.TunnelAddress
							break
						}
					}
				} else if fs.loadBalanced {
					log.Debug().Msgf("Load balancing request")

					tunnelAddr = fs.SelectLeastUsedServer()
					if tunnelAddr == "" {
						log.Debug().Msgf("Least used server not found, selecting random")
						tunnelAddr = fs.RandomServer()
					}

				} else {
					tunnelAddr = fs.RandomServer()
				}

				if tunnelAddr == "" {
					log.Error().Msg("No available nodes yet")
					return
				}

				log.Debug().Msgf("Selected tunnel %s", tunnelAddr)

				tunnelConn, err := net.Dial("tcp", tunnelAddr)
				if err != nil {
					log.Error().Err(err).Msg("Error connecting to tunnel")
					return
				}

				log.Info().Msgf("Redirecting %s to %s", conn.LocalAddr().String(), tunnelConn.RemoteAddr().String())
				closer := make(chan struct{}, 2)
				go copyStream(closer, tunnelConn, conn)
				go copyStream(closer, conn, tunnelConn)
				<-closer

				tunnelConn.Close()
				conn.Close()

				if fs.loadBalanced {
					fs.RecordRequest(tunnelAddr)
				}
			}()
		}
	}

}
