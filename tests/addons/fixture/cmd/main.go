// SPDX-License-Identifier: MIT
// This executable is a real protocol fixture, not a model backend.
package main

import (
	"crypto/subtle"
	"encoding/json"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

func main() {
	if len(os.Args) != 2 || os.Args[1] != "--localai-addon-v1" {
		os.Exit(2)
	}
	parent := os.NewFile(4, "startup")
	d := json.NewDecoder(parent)
	var startup addonruntime.Startup
	if d.Decode(&startup) != nil {
		os.Exit(3)
	}
	go func() { _, _ = io.Copy(io.Discard, io.MultiReader(d.Buffered(), parent)); os.Exit(0) }()
	var config struct {
		Mode string `json:"mode"`
	}
	b, _ := os.ReadFile("fixture.json")
	_ = json.Unmarshal(b, &config)
	_ = os.WriteFile("fixture.pid", []byte(strconv.Itoa(os.Getpid())), 0600)
	if config.Mode == "exit-start" {
		os.Exit(4)
	}
	if config.Mode == "env-check" && (os.Getenv("LOCALAI_RUNTIME_TEST_SECRET") != "" || os.Getenv("HTTP_PROXY") != "" || len(os.Environ()) != 2) {
		os.Exit(5)
	}
	listener, e := net.FileListener(os.NewFile(3, "listener"))
	if e != nil {
		os.Exit(6)
	}
	var mu sync.Mutex
	receipts := map[string]contract.Receipt{}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+startup.Credential)) != 1 || r.Header.Get("X-LocalAI-Incarnation") != startup.Registration.Incarnation {
			http.Error(w, "denied", 401)
			return
		}
		if r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "invalid request", 400)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, startup.Limits.EnvelopeBytes)
		defer r.Body.Close()
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		read := func(v any) bool {
			if decoder.Decode(v) != nil || decoder.Decode(new(any)) != io.EOF {
				http.Error(w, "invalid request", 400)
				return false
			}
			return true
		}
		valid := func(v contract.Envelope, scope string) bool {
			if v.Validate(time.Now(), startup.Limits) != nil || v.Authority.Scope != scope || v.Request.AddonID != startup.Registration.AddonID || v.Request.Generation != startup.Registration.Generation {
				http.Error(w, "invalid authority", 403)
				return false
			}
			return true
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/health":
			if config.Mode == "never-ready" {
				http.Error(w, "starting", 503)
				return
			}
			reg := startup.Registration
			switch config.Mode {
			case "forged":
				reg.Capabilities.Tools = []string{"forged"}
			case "nonloopback":
				reg.Endpoint = "http://192.0.2.1:80"
			case "stale":
				reg.Incarnation = "stale"
			case "wrong-version":
				reg.Protocol = 2
			}
			_ = json.NewEncoder(w).Encode(reg)
		case "/read", "/preview":
			var v contract.Envelope
			if !read(&v) || !valid(v, r.URL.Path[1:]) {
				return
			}
			if v.Request.Action == "delay" {
				select {
				case <-r.Context().Done():
					return
				case <-time.After(time.Second):
				}
			}
			if r.URL.Path == "/preview" {
				_ = json.NewEncoder(w).Encode(contract.Preview{OperationID: v.Request.OperationID, Revision: contract.Revision{Schema: 1, Data: 1}, ExpiresAt: time.Now().Add(time.Minute), Summary: json.RawMessage(`{"rows":1}`)})
				return
			}
			payload, _ := json.Marshal(map[string]any{"actor": v.Authority.Actor.UserID, "arguments": v.Request.Arguments})
			_ = json.NewEncoder(w).Encode(contract.Result{Payload: payload})
		case "/apply":
			var v addonruntime.ApplyRequest
			if !read(&v) || !valid(v.Envelope, "apply") {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			id := v.Envelope.Request.OperationID
			receipt, ok := receipts[id]
			if !ok {
				receipt = contract.Receipt{OperationID: id, State: contract.ReceiptCommitted, Revision: contract.Revision{Schema: v.Revision.Schema, Data: v.Revision.Data + 1}}
				receipts[id] = receipt
			}
			_ = json.NewEncoder(w).Encode(receipt)
		case "/receipt":
			var v addonruntime.ReceiptRequest
			if !read(&v) {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			receipt, ok := receipts[v.OperationID]
			if !ok {
				receipt = contract.Receipt{OperationID: v.OperationID, State: contract.ReceiptUnknown}
			}
			_ = json.NewEncoder(w).Encode(receipt)
		case "/output":
			var v addonruntime.OutputRequest
			if !read(&v) || !valid(v.Envelope, "output") {
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.WriteHeader(200)
			if v.Handle == "slow" {
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				return
			}
			if v.Handle == "oversized" {
				for n := int64(0); n <= startup.Limits.ArtifactBytes; n++ {
					if _, e := w.Write([]byte{'x'}); e != nil {
						return
					}
				}
				return
			}
			_, _ = io.WriteString(w, "fixture\n")
		default:
			http.NotFound(w, r)
		}
	})
	if config.Mode == "crash" {
		go func() { time.Sleep(100 * time.Millisecond); os.Exit(7) }()
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: time.Second, ReadTimeout: 2 * time.Second, IdleTimeout: time.Second, MaxHeaderBytes: 16 << 10}
	_ = server.Serve(listener)
}
