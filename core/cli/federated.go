package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"math/rand/v2"

	cliContext "github.com/mudler/LocalAI/core/cli/context"
	"github.com/mudler/LocalAI/core/p2p"
	"github.com/mudler/edgevpn/pkg/node"
	"github.com/mudler/edgevpn/pkg/protocol"
	"github.com/mudler/edgevpn/pkg/types"
	"github.com/rs/zerolog/log"
)

type FederatedCLI struct {
	Address        string `env:"LOCALAI_ADDRESS,ADDRESS" default:":8080" help:"Bind address for the API server" group:"api"`
	Peer2PeerToken string `env:"LOCALAI_P2P_TOKEN,P2P_TOKEN,TOKEN" name:"p2ptoken" help:"Token for P2P mode (optional)" group:"p2p"`
}

func (f *FederatedCLI) Run(ctx *cliContext.Context) error {

	n, err := p2p.NewNode(f.Peer2PeerToken)
	if err != nil {
		return fmt.Errorf("creating a new node: %w", err)
	}
	err = n.Start(context.Background())
	if err != nil {
		return fmt.Errorf("creating a new node: %w", err)
	}

	if err := p2p.ServiceDiscoverer(context.Background(), n, f.Peer2PeerToken, p2p.FederatedID, nil); err != nil {
		return err
	}

	return Proxy(context.Background(), n, f.Address, p2p.FederatedID)
}

func Proxy(ctx context.Context, node *node.Node, listenAddr, service string) error {

	log.Info().Msgf("Allocating service '%s' on: %s", service, listenAddr)
	// Open local port for listening
	l, err := net.Listen("tcp", listenAddr)
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
				for _, v := range p2p.GetAvailableNodes(p2p.FederatedID) {
					if v.IsOnline() {
						tunnelAddresses = append(tunnelAddresses, v.TunnelAddress)
					} else {
						log.Info().Msgf("Node %s is offline", v.ID)
					}
				}

				// open a TCP stream to one of the tunnels
				// chosen randomly
				// TODO: optimize this and track usage
				tunnelAddr := tunnelAddresses[rand.IntN(len(tunnelAddresses))]

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

func copyStream(closer chan struct{}, dst io.Writer, src io.Reader) {
	defer func() { closer <- struct{}{} }() // connection is closed, send signal to stop proxy
	io.Copy(dst, src)
}
