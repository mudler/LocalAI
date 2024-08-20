//go:build p2p
// +build p2p

package p2p

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/mudler/edgevpn/pkg/node"
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
	}, false); err != nil {
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

	nodeAnnounce(ctx, node)

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
				workerID := ""
				if fs.workerTarget != "" {
					workerID = fs.workerTarget
				} else if fs.loadBalanced {
					log.Debug().Msgf("Load balancing request")

					workerID = fs.SelectLeastUsedServer()
					if workerID == "" {
						log.Debug().Msgf("Least used server not found, selecting random")
						workerID = fs.RandomServer()
					}
				} else {
					workerID = fs.RandomServer()
				}

				if workerID == "" {
					log.Error().Msg("No available nodes yet")
					return
				}

				log.Debug().Msgf("Selected node %s", workerID)
				nodeData, exists := GetNode(fs.service, workerID)
				if !exists {
					log.Error().Msgf("Node %s not found", workerID)
					return
				}

				proxyP2PConnection(ctx, node, nodeData.ServiceID, conn)
				if fs.loadBalanced {
					fs.RecordRequest(workerID)
				}
			}()
		}
	}
}
