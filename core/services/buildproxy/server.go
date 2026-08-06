package buildproxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type Server struct {
	server   *http.Server
	listener net.Listener
	handler  http.Handler
	recorder *Recorder
	wg       sync.WaitGroup
}

func NewServer(address string, handler http.Handler, recorder *Recorder) *Server {
	s := &Server{handler: handler, recorder: recorder}
	s.server = &http.Server{Addr: address, Handler: http.HandlerFunc(s.serveHTTP), ReadHeaderTimeout: 30 * time.Second}
	return s
}

func (s *Server) Start() error {
	listener, err := net.Listen("tcp", s.server.Addr)
	if err != nil {
		return err
	}
	s.listener = listener
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := s.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.recorder.Record(Event{Method: "PROXY", Attempts: 1, Error: err.Error()})
		}
	}()
	return nil
}

func (s *Server) Addr() string { return s.listener.Addr().String() }

func (s *Server) Stop(ctx context.Context) error {
	err := s.server.Shutdown(ctx)
	s.wg.Wait()
	return err
}

func (s *Server) serveHTTP(w http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodConnect {
		s.handler.ServeHTTP(w, request)
		return
	}
	s.tunnel(w, request)
}

func (s *Server) tunnel(w http.ResponseWriter, request *http.Request) {
	host := request.Host
	if _, _, err := net.SplitHostPort(host); err != nil {
		host += ":443"
	}
	event := Event{Host: hostname(request.Host), Method: http.MethodConnect, Attempts: 1}
	upstream, err := net.DialTimeout("tcp", host, 15*time.Second)
	if err != nil {
		event.Error = err.Error()
		s.recorder.Record(event)
		http.Error(w, "build proxy: CONNECT failed", http.StatusBadGateway)
		return
	}
	defer upstream.Close()
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		event.Error = "response writer does not support hijacking"
		s.recorder.Record(event)
		http.Error(w, event.Error, http.StatusInternalServerError)
		return
	}
	client, _, err := hijacker.Hijack()
	if err != nil {
		event.Error = err.Error()
		s.recorder.Record(event)
		return
	}
	defer client.Close()
	if _, err := io.WriteString(client, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
		event.Error = err.Error()
		s.recorder.Record(event)
		return
	}
	event.BytesRead, event.BytesSent = pipe(client, upstream)
	s.recorder.Record(event)
}

func pipe(client, upstream net.Conn) (read, sent int64) {
	type result struct {
		upstreamToClient bool
		bytes            int64
	}
	done := make(chan result, 2)
	copyConn := func(dst, src net.Conn, upstreamToClient bool) {
		n, _ := io.Copy(dst, src)
		_ = dst.SetDeadline(time.Now())
		done <- result{upstreamToClient: upstreamToClient, bytes: n}
	}
	go copyConn(client, upstream, true)
	go copyConn(upstream, client, false)
	for range 2 {
		result := <-done
		if result.upstreamToClient {
			read = result.bytes
		} else {
			sent = result.bytes
		}
	}
	return read, sent
}

func ParseListenAddress(address string) (string, error) {
	if strings.TrimSpace(address) == "" {
		return "", fmt.Errorf("listen address is empty")
	}
	return address, nil
}
