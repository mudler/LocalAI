package p2p

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"

	"github.com/mudler/LocalAI/core/schema"
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

	if err := ServiceDiscoverer(ctx, n, f.p2ptoken, f.service, func(servicesID string, tunnel schema.NodeData) {
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
					fs.sendHTMLResponse(conn, 503, "Sorry, waiting for nodes to connect")
					return
				}

				log.Debug().Msgf("Selected node %s", workerID)
				nodeData, exists := GetNode(fs.service, workerID)
				if !exists {
					log.Error().Msgf("Node %s not found", workerID)
					fs.sendHTMLResponse(conn, 404, "Node not found")
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

// sendHTMLResponse sends a basic HTML response with a status code and a message.
// This is extracted to make the HTML content maintainable.
func (fs *FederatedServer) sendHTMLResponse(conn net.Conn, statusCode int, message string) {
	defer conn.Close()

	// Define the HTML content separately for easier maintenance.
	htmlContent := fmt.Sprintf("<html><body><h1>%s</h1></body></html>\r\n", message)

	// Create the HTTP response with dynamic status code and content.
	response := fmt.Sprintf(
		"HTTP/1.1 %d %s\r\n"+
			"Content-Type: text/html\r\n"+
			"Connection: close\r\n"+
			"\r\n"+
			"%s",
		statusCode, getHTTPStatusText(statusCode), htmlContent,
	)

	// Write the response to the client connection.
	_, writeErr := io.WriteString(conn, response)
	if writeErr != nil {
		log.Error().Err(writeErr).Msg("Error writing response to client")
	}
}

// getHTTPStatusText returns a textual representation of HTTP status codes.
func getHTTPStatusText(statusCode int) string {
	switch statusCode {
	case 503:
		return "Service Unavailable"
	case 404:
		return "Not Found"
	case 200:
		return "OK"
	default:
		return "Unknown Status"
	}
}
