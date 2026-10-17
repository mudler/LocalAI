// SPDX-License-Identifier: MIT

// Command foundation-cluster runs the foundation fixture for the browser
// checks: one owner and two frontends, the catalog on loopback, and the UI
// package already installed and enabled through a frontend. It writes a
// ready file with the addresses and accounts and runs until it is stopped.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mudler/LocalAI/tests/addons/foundation/cluster"
)

func main() {
	dir := flag.String("dir", "", "directory of the cluster state (created)")
	server := flag.String("server", "", "UI test server executable")
	mock := flag.String("mock-backend", "", "mock backend executable")
	fixture := flag.String("fixture", "", "protocol fixture executable")
	ready := flag.String("ready", "", "file written when the cluster is serving")
	flag.Parse()
	if err := run(*dir, cluster.Paths{Server: *server, Mock: *mock, Fixture: *fixture}, *ready); err != nil {
		fmt.Fprintln(os.Stderr, "foundation-cluster:", err)
		os.Exit(1)
	}
}

func run(dir string, p cluster.Paths, ready string) error {
	if dir == "" || ready == "" || p.Server == "" || p.Mock == "" || p.Fixture == "" {
		return fmt.Errorf("-dir, -server, -mock-backend, -fixture and -ready are required")
	}
	c, err := cluster.New(dir, p)
	if err != nil {
		return err
	}
	defer c.Down()
	// A signal during start-up or serving tears everything down first.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	interrupted := make(chan struct{})
	go func() {
		<-stop
		c.Down()
		close(interrupted)
		os.Exit(1)
	}()
	if err := c.Library.Add(cluster.UIPackage("1.0.0", nil), c.Effects); err != nil {
		return err
	}
	if err := c.Up(); err != nil {
		return err
	}
	// Installed and enabled through a frontend, as an administrator would.
	admin := c.Token(cluster.FrontA, "admin")
	for i, action := range []string{"install", "enable"} {
		args := map[string]any{}
		if action == "install" {
			args = map[string]any{"sourceId": "fixture", "version": "1.0.0"}
		}
		r, err := admin.Do("POST", "/api/addons/dispatch", map[string]any{"operationId": fmt.Sprintf("browser-%s-%d", action, i), "addonId": "found-ui", "generation": 1, "action": action, "arguments": args}, nil)
		if err != nil || r.Status != 200 {
			return fmt.Errorf("%s: %v %v", action, err, r)
		}
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		r, err := admin.Do("GET", "/api/addons/apps", nil, nil)
		if err == nil && r.Status == 200 && strings.Contains(string(r.Body), `"state":"running"`) {
			break
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the add-on did not start")
		}
		time.Sleep(200 * time.Millisecond)
	}
	out := map[string]any{"urls": map[string]string{}, "users": c.Creds.Users, "dir": c.Config.Dir}
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
