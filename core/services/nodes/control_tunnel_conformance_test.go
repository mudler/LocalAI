// SPDX-License-Identifier: MIT
package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"

	"github.com/mudler/LocalAI/core/services/workerctl"
)

// httpLoadOperationHarness drives the HTTP carrier for the conformance of
// LoadOperationControl. The worker is a scripted HTTP server on a loopback
// socket, and the dialer is the part that stands for the tunnel: it reaches that
// server, or it fails the way the tunnel fails.
type httpLoadOperationHarness struct {
	srv     *httptest.Server
	control *TunnelControl
	link    *httpLink

	mu      sync.Mutex
	mode    string
	dialErr error
	bodies  []sentRequest
	traced  []time.Duration
}

func newHTTPLoadOperationHarness() loadOperationHarness {
	h := &httpLoadOperationHarness{mode: "answer"}
	h.srv = httptest.NewServer(http.HandlerFunc(h.serve))
	DeferCleanup(h.srv.Close)

	dialFor := func(string) func(context.Context, string, string) (net.Conn, error) {
		return func(ctx context.Context, _, _ string) (net.Conn, error) {
			h.mu.Lock()
			err := h.dialErr
			h.mu.Unlock()
			if err != nil {
				return nil, err
			}
			var d net.Dialer
			return d.DialContext(ctx, "tcp", h.srv.Listener.Addr().String())
		}
	}
	control := NewTunnelControl(&fakeModelLocator{}, NewControlClient(dialFor, "token"), 3*time.Minute, 15*time.Minute)
	link := control.link.(*httpLink)
	// A worker that says nothing is waited out at a hundredth of the timeout of
	// the contract. The trace still reports the timeout of the contract.
	link.scale = 0.01
	link.trace = func(_ string, timeout time.Duration) {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.traced = append(h.traced, timeout)
	}
	h.control, h.link = control, link
	return h
}

func (h *httpLoadOperationHarness) Control() LoadOperationControl { return h.control }

func (h *httpLoadOperationHarness) NoRoute() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.dialErr = fmt.Errorf("%w: the tunnel of the node is not held", ErrNoRoute)
}

func (h *httpLoadOperationHarness) TimesOut() { h.setMode("hang") }

func (h *httpLoadOperationHarness) WorkerRefuses() { h.setMode("refuse") }

func (h *httpLoadOperationHarness) WorkerAnswers() { h.setMode("answer") }

func (h *httpLoadOperationHarness) WorkerAnswersUnreadably() { h.setMode("unreadable") }

func (h *httpLoadOperationHarness) setMode(m string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.mode = m
}

func (h *httpLoadOperationHarness) Sent() []sentRequest {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]sentRequest, len(h.bodies))
	for i, b := range h.bodies {
		out[i] = b
		if i < len(h.traced) {
			out[i].Timeout = h.traced[i]
		}
	}
	return out
}

// serve is the worker. It records the request and answers as the mode says.
func (h *httpLoadOperationHarness) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	h.mu.Lock()
	mode := h.mode
	h.mu.Unlock()

	rec := sentRequest{}
	var reply any
	switch r.URL.Path {
	case workerctl.PathOf(workerctl.VerbBackendInstall):
		rec.Verb = "install"
		_ = json.Unmarshal(body, &rec.Install)
		if mode == "refuse" {
			reply = workerctl.BackendInstallReply{Success: false, Error: "disk full"}
		} else {
			reply = workerctl.BackendInstallReply{Success: true, Address: "127.0.0.1:9001", ProcessInstance: "i", ReportsOperations: true}
		}
	case workerctl.PathOf(workerctl.VerbModelStop):
		rec.Verb = "stop"
		_ = json.Unmarshal(body, &rec.Stop)
		if mode == "refuse" {
			reply = workerctl.ModelStopReply{Matched: true, Error: "does not belong to operation"}
		} else {
			reply = workerctl.ModelStopReply{Matched: true, Terminated: true}
		}
	case workerctl.PathOf(workerctl.VerbModelOp):
		rec.Verb = "op"
		_ = json.Unmarshal(body, &rec.Op)
		if mode == "refuse" {
			reply = workerctl.OperationReply{Unknown: []string{"op"}}
		} else {
			reply = workerctl.OperationReply{Renewed: []string{"op"}, Completed: []string{"op"}}
		}
	case workerctl.PathOf(workerctl.VerbModelUnload):
		rec.Verb = "unload"
		_ = json.Unmarshal(body, &rec.Unload)
		if mode == "refuse" {
			reply = workerctl.ModelUnloadReply{Success: false, Error: "process was replaced during unload"}
		} else {
			reply = workerctl.ModelUnloadReply{Success: true}
		}
	default:
		workerctl.WriteUnknownPath(w, r)
		return
	}
	h.mu.Lock()
	h.bodies = append(h.bodies, rec)
	h.mu.Unlock()

	if mode == "unreadable" {
		if rec.Verb == "install" {
			w.Header().Set("Content-Type", workerctl.ContentTypeStream)
			_, _ = io.WriteString(w, "this is not a line of the stream\n")
			return
		}
		_, _ = io.WriteString(w, `"this is not a reply"`)
		return
	}

	if mode == "hang" {
		<-r.Context().Done()
		return
	}
	if rec.Verb == "install" {
		raw, _ := json.Marshal(reply)
		w.Header().Set("Content-Type", workerctl.ContentTypeStream)
		_, _ = fmt.Fprintf(w, "{\"reply\":%s}\n", strings.TrimSpace(string(raw)))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(reply)
}
