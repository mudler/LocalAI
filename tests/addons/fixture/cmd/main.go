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
	"path/filepath"
	"strconv"
	"strings"
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
	var config struct {
		Mode string `json:"mode"`
		// Effects names a file that makes the add-on's data durable: one line
		// per committed operation, and "<Effects>.applies" with one line per
		// apply request that reached the process, duplicates included. Empty
		// keeps the receipts in memory only.
		Effects string `json:"effects"`
	}
	b, _ := os.ReadFile("fixture.json")
	_ = json.Unmarshal(b, &config)
	// The "orphan" mode ignores the closing of the startup channel, as a
	// misbehaving add-on would: only the owner's ledger can then stop it.
	if config.Mode != "orphan" {
		go func() { _, _ = io.Copy(io.Discard, io.MultiReader(d.Buffered(), parent)); os.Exit(0) }()
	}
	_ = os.WriteFile("fixture.pid", []byte(strconv.Itoa(os.Getpid())), 0600)
	// The data directory the owner passed is recorded as received, so a test
	// can assert what the startup object carried. Writing a probe file there
	// shows the add-on can use it.
	_ = os.WriteFile("fixture.datadir", []byte(startup.DataDir), 0600)
	if startup.DataDir != "" {
		_ = os.WriteFile(filepath.Join(startup.DataDir, "fixture.probe"), []byte("ok"), 0600)
	}
	// The host operation endpoint and credential, as received, so a test can
	// assert that an add-on the host does not name gets neither.
	_ = os.WriteFile("fixture.hostops", []byte(startup.HostEndpoint+"\n"+startup.HostCredential), 0600)
	if config.Mode == "exit-start" {
		os.Exit(4)
	}
	if config.Mode == "env-check" && (os.Getenv("LOCALAI_RUNTIME_TEST_SECRET") != "" || os.Getenv("HTTP_PROXY") != "" || len(os.Environ()) != 2) { //nolint:forbidigo // the fixture checks the exact environment the host gives a child
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
		defer func() { _ = r.Body.Close() }()
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
			result := contract.Result{Payload: payload}
			// The tool "export" answers with a file result, and the owner
			// turns it into a private artifact.
			var tool struct {
				Tool string `json:"tool"`
			}
			if json.Unmarshal(v.Request.Arguments, &tool) == nil && tool.Tool == "export" {
				result.Outputs = []contract.OutputDescription{{Handle: "report", Name: "report.txt", MediaType: "text/plain", Size: int64(len("fixture\n"))}}
			}
			_ = json.NewEncoder(w).Encode(result)
		case "/apply":
			var v addonruntime.ApplyRequest
			if !read(&v) || !valid(v.Envelope, "apply") {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			id := v.Envelope.Request.OperationID
			noteLine(config.Effects+".applies", id)
			receipt, ok := lookup(config.Effects, receipts, id)
			fresh := !ok
			if fresh {
				receipt = contract.Receipt{OperationID: id, State: contract.ReceiptCommitted, Revision: contract.Revision{Schema: v.Revision.Schema, Data: v.Revision.Data + 1}}
				receipts[id] = receipt
				noteLine(config.Effects, id)
			}
			// "lose-reply": the change is committed and the connection is
			// dropped before the reply, as a crash or a network cut would.
			if fresh && config.Mode == "lose-reply" {
				if conn, _, e := w.(http.Hijacker).Hijack(); e == nil {
					_ = conn.Close()
					return
				}
			}
			_ = json.NewEncoder(w).Encode(receipt)
		case "/receipt":
			var v addonruntime.ReceiptRequest
			if !read(&v) {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			receipt, ok := lookup(config.Effects, receipts, v.OperationID)
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

// noteLine appends one line to a durable log. An empty path keeps nothing.
func noteLine(path, line string) {
	if path == "" || path == ".applies" {
		return
	}
	f, e := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600) // #nosec G304 -- test harness reads files it created in its own work directory
	if e != nil {
		return
	}
	_, _ = f.WriteString(line + "\n")
	_ = f.Sync()
	_ = f.Close()
}

// lookup finds the receipt of an operation, in memory and then in the durable
// log, so that a restarted process still knows what it committed.
func lookup(path string, memory map[string]contract.Receipt, id string) (contract.Receipt, bool) {
	if r, ok := memory[id]; ok {
		return r, true
	}
	if path == "" {
		return contract.Receipt{}, false
	}
	b, e := os.ReadFile(path) // #nosec G304 -- test harness reads files it created in its own work directory
	if e != nil {
		return contract.Receipt{}, false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if line == id {
			return contract.Receipt{OperationID: id, State: contract.ReceiptCommitted, Revision: contract.Revision{Schema: 1, Data: 1}}, true
		}
	}
	return contract.Receipt{}, false
}
