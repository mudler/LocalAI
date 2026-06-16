package p2p

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/nodes/prefixcache"
	"github.com/mudler/edgevpn/pkg/node"
	"github.com/mudler/xlog"
)

// ErrBodyTooLarge is returned by readRequest when the buffered request body
// exceeds the configured limit. The proxy turns it into a 413 response.
var ErrBodyTooLarge = errors.New("request body exceeds limit")

// readRequest parses a single HTTP request from r and buffers its body (so the
// body can both be inspected for the model/prefix and replayed to the chosen
// peer). limit caps the buffered body in bytes; 0 means unlimited. A body over
// the cap returns ErrBodyTooLarge. The returned request has its body replaced
// with the buffered bytes and RequestURI cleared so it can be re-serialized
// with req.Write to the peer stream.
func readRequest(r *bufio.Reader, limit int64) (*http.Request, []byte, error) {
	req, err := http.ReadRequest(r)
	if err != nil {
		return nil, nil, err
	}
	var body []byte
	if req.Body != nil {
		reader := io.Reader(req.Body)
		if limit > 0 {
			reader = io.LimitReader(req.Body, limit+1)
		}
		body, err = io.ReadAll(reader)
		_ = req.Body.Close()
		if err != nil {
			return nil, nil, err
		}
		if limit > 0 && int64(len(body)) > limit {
			return nil, nil, ErrBodyTooLarge
		}
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.ContentLength = int64(len(body))
	req.RequestURI = ""
	return req, body, nil
}

// isWebsocketUpgrade reports whether req is a websocket handshake, which must be
// forwarded as a raw bidirectional duplex (not request/streamed-response) and
// is not body-capped or model-routed.
func isWebsocketUpgrade(req *http.Request) bool {
	return strings.Contains(strings.ToLower(req.Header.Get("Connection")), "upgrade") &&
		strings.EqualFold(req.Header.Get("Upgrade"), "websocket")
}

func (f *FederatedServer) Start(ctx context.Context) error {
	var extraOpts []node.Option
	if f.syncAffinity {
		extraOpts = append(extraOpts, node.EnableGenericHub, node.GenericChannelHandlers(f.affinityHandler()))
	}
	n, err := NewNode(f.p2ptoken, extraOpts...)
	if err != nil {
		return fmt.Errorf("creating a new node: %w", err)
	}
	if f.syncAffinity {
		f.prefixSync = prefixcache.NewSync(f.prefixIndex, &genericChannelPublisher{node: n})
		f.prefixProvider = f.prefixSync
		xlog.Info("Federation affinity sync enabled (generic channel)")
	}
	err = n.Start(ctx)
	if err != nil {
		return fmt.Errorf("creating a new node: %w", err)
	}

	if err := ServiceDiscoverer(ctx, n, f.p2ptoken, f.service, func(servicesID string, tunnel schema.NodeData) {
		xlog.Debug("Discovered node", "node", tunnel.ID)
	}, false); err != nil {
		return err
	}

	go f.evictLoop(ctx)

	return f.proxy(ctx, n)
}

func (fs *FederatedServer) proxy(ctx context.Context, node *node.Node) error {

	xlog.Info("Allocating service", "service", fs.service, "address", fs.listenAddr)
	// Open local port for listening
	l, err := net.Listen("tcp", fs.listenAddr)
	if err != nil {
		xlog.Error("Error listening", "error", err)
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
			xlog.Debug("New connection", "address", l.Addr().String())
			// Listen for an incoming connection.
			conn, err := l.Accept()
			if err != nil {
				fmt.Println("Error accepting: ", err.Error())
				continue
			}

			// Handle connections in a new goroutine, terminating HTTP and
			// forwarding the request to the chosen p2p peer.
			go func() {
				br := bufio.NewReader(conn)
				req, body, err := readRequest(br, fs.bodyLimit)
				if err != nil {
					if err == ErrBodyTooLarge {
						fs.sendHTMLResponse(conn, 413, "Request body too large")
						return
					}
					xlog.Error("Failed to read request", "error", err)
					_ = conn.Close()
					return
				}

				upgrade := isWebsocketUpgrade(req)

				now := time.Now()
				var (
					workerID string
					model    string
					chain    []uint64
				)
				switch {
				case fs.workerTarget != "":
					workerID = fs.workerTarget
				case !fs.loadBalanced:
					// Explicit random mode (the RandomWorker flag): keep the
					// historical random pick, no model/affinity routing.
					workerID = fs.RandomServer()
				case upgrade:
					// Websocket: no readable model; route by load only.
					workerID, _ = fs.selectPeer("", nil, now)
				default:
					model = extractModel(req.URL.Query().Get("model"), body)
					workerID, chain = fs.selectPeer(model, body, now)
				}

				if workerID == "" {
					fs.sendHTMLResponse(conn, 503, "No federated peer available for this request")
					return
				}

				nodeData, exists := GetNode(fs.service, workerID)
				if !exists {
					fs.sendHTMLResponse(conn, 404, "Node not found")
					return
				}

				proxyHTTPToPeer(ctx, node, nodeData.ServiceID, conn, req, upgrade)

				fs.RecordRequest(workerID)
				if !upgrade {
					fs.observeServed(model, chain, workerID, now)
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
		xlog.Error("Error writing response to client", "error", writeErr)
	}
}

// getHTTPStatusText returns a textual representation of HTTP status codes.
func getHTTPStatusText(statusCode int) string {
	switch statusCode {
	case 503:
		return "Service Unavailable"
	case 413:
		return "Request Entity Too Large"
	case 404:
		return "Not Found"
	case 200:
		return "OK"
	default:
		return "Unknown Status"
	}
}
