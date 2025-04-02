//go:build p2p
// +build p2p

package p2p

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	logP2P "github.com/ipfs/go-log/v2"
	cliP2P "github.com/mudler/LocalAI/core/cli/p2p"
	edgevpnConfig "github.com/mudler/edgevpn/pkg/config"
	"github.com/mudler/edgevpn/pkg/logger"
	"github.com/mudler/edgevpn/pkg/node"
	"github.com/mudler/edgevpn/pkg/trustzone"
	"github.com/rs/zerolog/log"
)

const Timeout = 20 * time.Second

const (
	peekBufferSize = 512
	authHeader     = "X-Auth-Token"
	headerEnd      = "\r\n\r\n"
	lineEnd        = "\r\n"
)

func (fs *FederatedServer) Start(ctx context.Context, p2pCommonFlags cliP2P.P2PCommonFlags) error {
	p2pCfg := NewP2PConfig(p2pCommonFlags)
	p2pCfg.NetworkToken = fs.p2ptoken
	p2pCfg.PeerGuard.Autocleanup = true
	p2pCfg.PeerGuard.PeerGate = true

	n, err := NewNode(p2pCfg)
	if err != nil {
		return fmt.Errorf("creating a new node: %w", err)
	}
	err = n.Start(ctx)
	if err != nil {
		return fmt.Errorf("creating a new node: %w", err)
	}

	if err := ServiceDiscoverer(ctx, n, fs.service, func(servicesID string, tunnel NodeData) {
		log.Debug().Msgf("Discovered node: %s", tunnel.ID)
	}, false); err != nil {
		return err
	}

	lvl, err := logP2P.LevelFromString(p2pCfg.LogLevel)
	if err != nil {
		lvl = logP2P.LevelError
	}
	llger := logger.New(lvl)

	aps := []trustzone.AuthProvider{}
	for ap, providerOpts := range p2pCfg.PeerGuard.AuthProviders {
		a, err := edgevpnConfig.AuthProvider(llger, ap, providerOpts)
		if err != nil {
			log.Warn().Msgf("invalid authprovider: %v", err)
			continue
		}
		aps = append(aps, a)
	}

	return fs.listener(ctx, n, aps)
}

func (fs *FederatedServer) listener(ctx context.Context, node *node.Node, aps []trustzone.AuthProvider) error {
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

			go func() {
				if len(aps) > 0 {
					if fs.handleHTTP(conn, node, aps) {
						return
					}
				}
				fs.proxy(ctx, node, conn)
			}()
		}
	}
}

func (fs *FederatedServer) handleHTTP(conn net.Conn, node *node.Node, aps []trustzone.AuthProvider) bool {
	defer func() {
		if r := recover(); r != nil {
			log.Debug().Msgf("Recovered from panic: %v", r)
			conn.Close()
		}
	}()

	r, err := testForHTTPRequest(conn)
	if err != nil {
		return false
	}
	defer r.Body.Close()
	pathParts := strings.Split(strings.TrimPrefix(r.URL.Path, "/ledger/"), "/")
	announcing := struct{ State string }{"Announcing"}

	// TODO deal with AuthProviders
	// pubKey := r.Header.Get(authHeader)

	switch r.Method {
	case http.MethodGet:
		switch len(pathParts) {
		case 2: // /ledger/:bucket/:key
			bucket := pathParts[0]
			key := pathParts[1]

			ledger, err := node.Ledger()
			if err != nil {
				fs.sendRawResponse(conn, http.StatusInternalServerError, "text/plain", []byte(err.Error()))
				return true
			}

			fs.sendJSONResponse(conn, http.StatusOK, ledger.CurrentData()[bucket][key])

		case 1: // /ledger/:bucket
			bucket := pathParts[0]

			ledger, err := node.Ledger()
			if err != nil {
				fs.sendRawResponse(conn, http.StatusInternalServerError, "text/plain", []byte(err.Error()))
				return true
			}

			fs.sendJSONResponse(conn, http.StatusOK, ledger.CurrentData()[bucket])

		default:
			fs.sendRawResponse(conn, http.StatusNotFound, "text/plain", []byte("not found"))

		}

	case http.MethodPut:
		if len(pathParts) == 3 { // /ledger/:bucket/:key/:value
			bucket := pathParts[0]
			key := pathParts[1]
			value := pathParts[2]

			ledger, err := node.Ledger()
			if err != nil {
				fs.sendRawResponse(conn, http.StatusInternalServerError, "text/plain", []byte(err.Error()))
				return true
			}

			ledger.Persist(context.Background(), DefaultInterval, Timeout, bucket, key, value)
			fs.sendJSONResponse(conn, http.StatusOK, announcing)

		} else {
			fs.sendRawResponse(conn, http.StatusNotFound, "text/plain", []byte("not found"))
		}

	case http.MethodDelete:
		switch len(pathParts) {
		case 1: // /ledger/:bucket
			bucket := pathParts[0]

			ledger, err := node.Ledger()
			if err != nil {
				fs.sendRawResponse(conn, http.StatusInternalServerError, "text/plain", []byte(err.Error()))
				return true
			}

			ledger.AnnounceDeleteBucket(context.Background(), DefaultInterval, Timeout, bucket)
			fs.sendJSONResponse(conn, http.StatusOK, announcing)

		case 2: // /ledger/:bucket/:key
			bucket := pathParts[0]
			key := pathParts[1]

			ledger, err := node.Ledger()
			if err != nil {
				fs.sendRawResponse(conn, http.StatusInternalServerError, "text/plain", []byte(err.Error()))
				return true
			}

			ledger.AnnounceDeleteBucketKey(context.Background(), DefaultInterval, Timeout, bucket, key)
			fs.sendJSONResponse(conn, http.StatusOK, announcing)

		default:
			fs.sendRawResponse(conn, http.StatusNotFound, "text/plain", []byte("not found"))

		}
	}

	return true
}

// testForHTTPRequest peeking the first N bytes from the accepted conn, and trying to match it
// against the supported http methods, then against the supported route, then if there is auth header
func testForHTTPRequest(conn net.Conn) (*http.Request, error) {
	reader := bufio.NewReader(conn)

	peekedData, err := reader.Peek(peekBufferSize)
	if err != nil && err != bufio.ErrBufferFull {
		log.Debug().Msgf("Error peeking data: %v", err)
		return nil, err
	}
	peekedString := string(peekedData)

	// 1. Parse Request Line
	firstLineEnd := strings.Index(peekedString, lineEnd)
	if firstLineEnd == -1 {
		log.Debug().Msg("Could not find request line end")
		return nil, err
	}
	requestLine := peekedString[:firstLineEnd]
	parts := strings.Split(requestLine, " ")
	if len(parts) != 3 {
		log.Debug().Msg("Invalid request line format")
		return nil, err
	}
	method := parts[0]
	uri := parts[1]

	if !slices.Contains([]string{
		http.MethodGet,
		http.MethodPut,
		http.MethodDelete,
	}, method) {
		log.Debug().Msg("Unsupported HTTP method")
		return nil, err
	}
	if !strings.HasPrefix(uri, "/ledger") {
		log.Debug().Msg("Unsupported HTTP route")
		return nil, err
	}

	headersPart := peekedString[firstLineEnd+len(lineEnd):]
	headerEndIndex := strings.Index(headersPart, headerEnd)
	if headerEndIndex == -1 {
		log.Debug().Msg("Could not find end of headers within peek buffer")
		return nil, err
	}
	headersString := headersPart[:headerEndIndex]
	headers := strings.Split(headersString, lineEnd)

	foundAuth := false
	for _, header := range headers {
		if strings.HasPrefix(header, authHeader+":") {
			parts := strings.SplitN(header, ":", 2)
			if len(parts) == 2 {
				foundAuth = true
				break
			}
		}
	}

	if !foundAuth {
		log.Debug().Msgf("Required header '%s' not found.", authHeader)
		return nil, err
	}

	req, err := http.ReadRequest(reader)
	if err != nil {
		log.Debug().Msgf("Error reading full request: %v", err)
		return nil, err
	}
	return req, nil
}

func (fs *FederatedServer) proxy(ctx context.Context, node *node.Node, conn net.Conn) {
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
}

// sendRawResponse sends whatever provided byte data with provided content type header
func (fs *FederatedServer) sendRawResponse(conn net.Conn, statusCode int, contentType string, data []byte) {
	defer conn.Close()

	response := fmt.Sprintf(
		"HTTP/1.1 %d %s\r\n"+
			"Content-Type: %s\r\n"+
			"Connection: close\r\n"+
			"\r\n"+
			"%s",
		statusCode, http.StatusText(statusCode), contentType, data,
	)

	// Write the response to the client connection.
	_, writeErr := io.WriteString(conn, response)
	if writeErr != nil {
		log.Error().Err(writeErr).Msg("Error writing response to client")
	}
}

// sendJSONResponse marshals provided data to JSON and sends it
func (fs *FederatedServer) sendJSONResponse(conn net.Conn, statusCode int, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		log.Error().Err(err).Msg("Error JSON marshaling")
	}

	fs.sendRawResponse(conn, statusCode, "application/json", data)
}

// sendHTMLResponse sends a basic HTML response with a status code and a message.
// This is extracted to make the HTML content maintainable.
func (fs *FederatedServer) sendHTMLResponse(conn net.Conn, statusCode int, message string) {
	// Define the HTML content separately for easier maintenance.
	htmlContent := fmt.Sprintf("<html><body><h1>%s</h1></body></html>\r\n", message)

	fs.sendRawResponse(conn, statusCode, "text/html", []byte(htmlContent))
}
