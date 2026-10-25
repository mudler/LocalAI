// SPDX-License-Identifier: MIT
package cluster

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Cluster is a running fixture: the catalog, the configuration and the three
// server processes.
type Cluster struct {
	Config  *Config
	Library *Library
	Nodes   map[string]*Node
	Creds   *Credentials
	// Effects is where the fixture add-ons keep their durable commit logs.
	Effects string

	mu   sync.Mutex
	down bool
}

// Paths are the executables a cluster needs.
type Paths struct {
	Server, Mock, Fixture string
}

// New generates a configuration under dir and starts the catalog. No process
// is started: call Up.
func New(dir string, p Paths) (*Cluster, error) {
	lib, err := NewLibrary(p.Fixture)
	if err != nil {
		return nil, err
	}
	cfg, err := Generate(dir, lib.URL())
	if err != nil {
		lib.Close()
		return nil, err
	}
	c := &Cluster{Config: cfg, Library: lib, Nodes: map[string]*Node{}, Effects: filepath.Join(cfg.Dir, "effects")}
	if err := os.MkdirAll(c.Effects, 0o700); err != nil {
		lib.Close()
		return nil, err
	}
	for _, name := range Instances {
		c.Nodes[name] = &Node{Name: name, Config: cfg, ServerPath: p.Server, MockPath: p.Mock}
	}
	return c, nil
}

// Up starts the owner, waits for it, and then starts the frontends.
func (c *Cluster) Up() error {
	for _, name := range Instances {
		c.mu.Lock()
		if c.down {
			c.mu.Unlock()
			return fmt.Errorf("the cluster is down")
		}
		err := c.Nodes[name].Start()
		c.mu.Unlock()
		if err != nil {
			return err
		}
		if err := c.Nodes[name].Ready(120 * time.Second); err != nil {
			return err
		}
	}
	creds, err := LoadCredentials(c.Config.Credentials)
	if err != nil {
		return fmt.Errorf("the owner wrote no credentials: %w", err)
	}
	c.Creds = creds
	return nil
}

// Down stops every process and the catalog. It is idempotent, safe to call
// from a signal handler, and no node starts after it.
func (c *Cluster) Down() {
	c.mu.Lock()
	first := !c.down
	c.down = true
	c.mu.Unlock()
	for _, name := range []string{FrontB, FrontA, Owner} {
		c.Nodes[name].Stop()
	}
	if first {
		c.Library.Close()
	}
}

// Token returns a client of one node authenticated with the API key of a user.
func (c *Cluster) Token(node, user string) Client {
	return Client{Base: c.Nodes[node].URL(), Bearer: c.Creds.Users[user].Token}
}

// Session returns a client of one node with the browser session of a user.
func (c *Cluster) Session(node, user string) Client {
	return Client{Base: c.Nodes[node].URL(), Session: c.Creds.Users[user].Session}
}

// Control calls the owner's test control route.
func (c *Cluster) Control(path string, body any) (*Response, error) {
	return Client{Base: "http://" + c.Config.ControlAddr}.Do("POST", "/"+path, body, map[string]string{"X-Foundation-Control": c.Config.Control})
}
