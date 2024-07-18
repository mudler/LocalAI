//go:build p2p
// +build p2p

package p2p

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"math/rand/v2"

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
	}); err != nil {
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

	ledger, _ := node.Ledger()

	// Announce ourselves so nodes accepts our connection
	ledger.Announce(
		ctx,
		10*time.Second,
		func() {
			// Retrieve current ID for ip in the blockchain
			//_, found := ledger.GetKey(protocol.UsersLedgerKey, node.Host().ID().String())
			// If mismatch, update the blockchain
			//if !found {
			updatedMap := map[string]interface{}{}
			updatedMap[node.Host().ID().String()] = &types.User{
				PeerID:    node.Host().ID().String(),
				Timestamp: time.Now().String(),
			}
			ledger.Add(protocol.UsersLedgerKey, updatedMap)
			//	}
		},
	)

	defer l.Close()
	for {
		select {
		case <-ctx.Done():
			return errors.New("context canceled")
		default:
			log.Debug().Msg("New for connection")
			// Listen for an incoming connection.
			conn, err := l.Accept()
			if err != nil {
				fmt.Println("Error accepting: ", err.Error())
				continue
			}

			// Handle connections in a new goroutine, forwarding to the p2p service
			go func() {
				var tunnelAddresses []string
				for _, v := range GetAvailableNodes(fs.service) {
					if v.IsOnline() {
						tunnelAddresses = append(tunnelAddresses, v.TunnelAddress)
					} else {
						log.Info().Msgf("Node %s is offline", v.ID)
					}
				}

				if len(tunnelAddresses) == 0 {
					log.Error().Msg("No available nodes yet")
					return
				}

				tunnelAddr := ""

				if fs.loadBalanced {
					for _, t := range tunnelAddresses {
						fs.EnsureRecordExist(t)
					}

					tunnelAddr = fs.SelectLeastUsedServer()
					log.Debug().Msgf("Selected tunnel %s", tunnelAddr)
					if tunnelAddr == "" {
						tunnelAddr = tunnelAddresses[rand.IntN(len(tunnelAddresses))]
					}

					fs.RecordRequest(tunnelAddr)
				} else {
					tunnelAddr = tunnelAddresses[rand.IntN(len(tunnelAddresses))]
				}

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
				//	ll.Infof("(service %s) Done handling %s", serviceID, l.Addr().String())
			}()
		}
	}

}
