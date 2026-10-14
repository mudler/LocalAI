// SPDX-License-Identifier: MIT
package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Node is one UI test server process of the cluster.
type Node struct {
	Name       string
	Config     *Config
	ServerPath string
	MockPath   string

	cmd  *exec.Cmd
	done chan struct{}
}

// URL is the HTTP address of the node.
func (n *Node) URL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", n.Config.Nodes[n.Name].HTTPPort)
}

// Start launches the process. It does not wait for readiness.
func (n *Node) Start() error {
	if n.cmd != nil && !n.Exited() {
		return fmt.Errorf("%s is already running", n.Name)
	}
	logFile, err := os.OpenFile(filepath.Join(n.Config.Dir, n.Name+".log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	node := n.Config.Nodes[n.Name]
	cmd := exec.Command(n.ServerPath,
		"--mock-backend="+n.MockPath,
		"--port="+strconv.Itoa(node.HTTPPort),
		"--foundation="+n.Config.Path(),
		"--foundation-role="+n.Name)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.Env = append(os.Environ(), "TMPDIR="+node.Dir)
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return err
	}
	done := make(chan struct{})
	n.cmd, n.done = cmd, done
	go func() { _ = cmd.Wait(); _ = logFile.Close(); close(done) }()
	return nil
}

// PID is the process id, or zero when it was never started.
func (n *Node) PID() int {
	if n.cmd == nil || n.cmd.Process == nil {
		return 0
	}
	return n.cmd.Process.Pid
}

// Exited reports whether the process has ended.
func (n *Node) Exited() bool {
	if n.done == nil {
		return true
	}
	select {
	case <-n.done:
		return true
	default:
		return false
	}
}

// Wait blocks until the process ended or the timeout passed.
func (n *Node) Wait(timeout time.Duration) bool {
	if n.done == nil {
		return true
	}
	select {
	case <-n.done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// Ready waits until the node answers its readiness probe.
func (n *Node) Ready(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if n.Exited() {
			return fmt.Errorf("%s exited before it was ready: %s", n.Name, n.Tail(15))
		}
		resp, err := http.Get(n.URL() + "/readyz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("%s was not ready in %s: %s", n.Name, timeout, n.Tail(15))
}

// Kill ends the process without any chance to clean up.
func (n *Node) Kill() {
	if n.cmd != nil && n.cmd.Process != nil && !n.Exited() {
		_ = n.cmd.Process.Signal(syscall.SIGKILL)
		n.Wait(15 * time.Second)
	}
}

// Stop asks the process to end and then makes sure it did.
func (n *Node) Stop() {
	if n.cmd != nil && n.cmd.Process != nil && !n.Exited() {
		_ = n.cmd.Process.Signal(syscall.SIGTERM)
		if !n.Wait(20 * time.Second) {
			n.Kill()
		}
	}
}

// Tail returns the last lines of the node log.
func (n *Node) Tail(lines int) string {
	b, err := os.ReadFile(filepath.Join(n.Config.Dir, n.Name+".log"))
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(parts) > lines {
		parts = parts[len(parts)-lines:]
	}
	return strings.Join(parts, "\n")
}

// ChildrenOf lists the live direct children of a process, as pid and the
// command line, by reading /proc.
func ChildrenOf(pid int) map[int]string {
	out := map[int]string{}
	entries, _ := os.ReadDir("/proc")
	for _, e := range entries {
		child, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		s := string(stat)
		end := strings.LastIndex(s, ")")
		if end < 0 {
			continue
		}
		f := strings.Fields(s[end+1:])
		// f[0] is the state and f[1] the parent.
		if len(f) < 2 || f[0] == "Z" {
			continue
		}
		if ppid, _ := strconv.Atoi(f[1]); ppid == pid {
			cmdline, _ := os.ReadFile("/proc/" + e.Name() + "/cmdline")
			out[child] = strings.ReplaceAll(strings.TrimRight(string(cmdline), "\x00"), "\x00", " ")
		}
	}
	return out
}

// Alive reports whether a process id is a live, non-zombie process.
func Alive(pid int) bool {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	s := string(b)
	end := strings.LastIndex(s, ")")
	if end < 0 {
		return false
	}
	f := strings.Fields(s[end+1:])
	return len(f) > 0 && f[0] != "Z"
}

// Credentials are the accounts the owner seeded.
type Credentials struct {
	Users    map[string]User `json:"users"`
	Packages []string        `json:"packages"`
}

// User is one seeded account with its API key and browser session.
type User struct {
	ID      string `json:"id"`
	Role    string `json:"role"`
	Token   string `json:"token"`
	Session string `json:"session"`
	KeyID   string `json:"keyId"`
}

// LoadCredentials reads the file the owner wrote.
func LoadCredentials(path string) (*Credentials, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Credentials
	return &c, json.Unmarshal(b, &c)
}

// Response is one HTTP answer.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// JSON decodes the body.
func (r *Response) JSON(v any) error { return json.Unmarshal(r.Body, v) }

// String is the body as text.
func (r *Response) String() string { return string(r.Body) }

// Client talks to one node as one account.
type Client struct {
	Base    string
	Bearer  string
	Session string
	// Headers are sent on every request, after the credential headers.
	Headers map[string]string
}

// Do sends one request. A session client sends the browser fetch metadata the
// add-on routes require; a bearer client sends none.
func (c Client) Do(method, path string, body any, extra map[string]string) (*Response, error) {
	var reader io.Reader
	if body != nil {
		if raw, ok := body.(string); ok {
			reader = strings.NewReader(raw)
		} else {
			b, err := json.Marshal(body)
			if err != nil {
				return nil, err
			}
			reader = bytes.NewReader(b)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, c.Base+path, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	switch {
	case c.Bearer != "":
		req.Header.Set("Authorization", "Bearer "+c.Bearer)
	case c.Session != "":
		req.Header.Set("Cookie", "session="+c.Session)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("Sec-Fetch-Mode", "cors")
		req.Header.Set("Sec-Fetch-Dest", "empty")
	}
	for k, v := range c.Headers {
		req.Header.Set(k, v)
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	return &Response{Status: resp.StatusCode, Header: resp.Header, Body: data}, err
}
