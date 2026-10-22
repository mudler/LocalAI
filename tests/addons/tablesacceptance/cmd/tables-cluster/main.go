// SPDX-License-Identifier: MIT

// Command tables-cluster runs the acceptance fixture for the browser checks:
// one owner and two forwarding frontends over the real carrier, a local
// catalog that serves the packaged Tables, and Tables installed and enabled
// through a frontend as an administrator would. It writes a ready file with
// the addresses, the accounts and a loopback control address, and runs until
// it is stopped. The control address lets a browser spec stop and start the
// owner, so it can look at the page when the owner is gone.
package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mudler/LocalAI/tests/addons/foundation/cluster"
	"github.com/mudler/LocalAI/tests/addons/tablesacceptance/tablespkg"
)

func main() {
	dir := flag.String("dir", "", "directory of the cluster state (created)")
	server := flag.String("server", "", "UI test server executable")
	mock := flag.String("mock-backend", "", "mock backend executable")
	tables := flag.String("tables", "", "Tables executable")
	root := flag.String("root", "", "repository root")
	ready := flag.String("ready", "", "file written when the cluster is serving")
	flag.Parse()
	if err := run(*dir, *root, cluster.Paths{Server: *server, Mock: *mock}, *tables, *ready); err != nil {
		fmt.Fprintln(os.Stderr, "tables-cluster:", err)
		os.Exit(1)
	}
}

func run(dir, root string, p cluster.Paths, tables, ready string) error {
	if dir == "" || root == "" || ready == "" || p.Server == "" || p.Mock == "" || tables == "" {
		return fmt.Errorf("-dir, -root, -server, -mock-backend, -tables and -ready are required")
	}
	scratch := filepath.Join(dir, "packages")
	if err := os.MkdirAll(scratch, 0o700); err != nil {
		return err
	}
	releases, err := tablespkg.Build(root, scratch, tables, nil, "0.1.0")
	if err != nil {
		return err
	}
	c, err := cluster.New(filepath.Join(dir, "cluster"), p)
	if err != nil {
		return err
	}
	defer c.Down()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	interrupted := make(chan struct{})
	go func() {
		<-stop
		c.Down()
		close(interrupted)
		os.Exit(1)
	}()
	if _, err := c.Library.AddArchive("tables", "0.1.0", releases["0.1.0"].Archive); err != nil {
		return err
	}
	if err := c.Up(); err != nil {
		return err
	}
	admin := c.Token(cluster.FrontA, "admin")
	for i, action := range []string{"install", "enable"} {
		args := map[string]any{}
		if action == "install" {
			args = map[string]any{"sourceId": "fixture", "version": "0.1.0"}
		}
		r, err := admin.Do("POST", "/api/addons/dispatch", map[string]any{"operationId": fmt.Sprintf("browser-%s-%d", action, i), "addonId": "tables", "generation": 1, "action": action, "arguments": args}, nil)
		if err != nil || r.Status != 200 {
			return fmt.Errorf("%s: %v %v", action, err, r)
		}
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		r, err := admin.Do("GET", "/api/addons/apps", nil, nil)
		if err == nil && r.Status == 200 && strings.Contains(string(r.Body), `"state":"running"`) {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("Tables did not start")
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Owner control: stop (as a crash) and start again.
	token := make([]byte, 16)
	_, _ = rand.Read(token)
	secret := hex.EncodeToString(token)
	mux := http.NewServeMux()
	guard := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method != http.MethodPost || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Control")), []byte(secret)) != 1 {
			http.Error(w, "denied", http.StatusForbidden)
			return false
		}
		return true
	}
	mux.HandleFunc("/owner/stop", func(w http.ResponseWriter, r *http.Request) {
		if guard(w, r) {
			c.Nodes[cluster.Owner].Kill()
			_, _ = w.Write([]byte("stopped"))
		}
	})
	mux.HandleFunc("/owner/start", func(w http.ResponseWriter, r *http.Request) {
		if !guard(w, r) {
			return
		}
		n := c.Nodes[cluster.Owner]
		if err := n.Start(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := n.Ready(120 * time.Second); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte("started"))
	})
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(l) }()
	defer func() { _ = srv.Close() }()

	out := map[string]any{"urls": map[string]string{}, "users": c.Creds.Users, "dir": c.Config.Dir,
		"control": map[string]string{"url": "http://" + l.Addr().String(), "token": secret}}
	for name, n := range c.Nodes {
		out["urls"].(map[string]string)[name] = n.URL()
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	tmp := filepath.Clean(ready) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, ready); err != nil {
		return err
	}
	<-interrupted
	return nil
}
