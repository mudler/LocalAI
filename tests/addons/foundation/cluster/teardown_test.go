// SPDX-License-Identifier: MIT
package cluster

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"
)

// fakeServer is a server stand-in: it starts two helpers that ignore SIGTERM,
// as an add-on in orphan mode does, records their ids and then waits.
const fakeServer = `#!/bin/sh
dir=$(dirname "$0")
for n in 1 2; do
    ( trap '' TERM; exec sleep 300 ) &
    echo $! >> "$dir/helpers"
done
echo ready > "$dir/ready"
while :; do sleep 1; done
`

func helperPIDs(t *testing.T, dir string) []int {
	t.Helper()
	var pids []int
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(dir, "ready")); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	b, err := os.ReadFile(filepath.Join(dir, "helpers"))
	NewWithT(t).Expect(err).NotTo(HaveOccurred())
	for _, f := range strings.Fields(string(b)) {
		n, _ := strconv.Atoi(f)
		pids = append(pids, n)
	}
	NewWithT(t).Expect(pids).To(HaveLen(2), "want two helpers")
	return pids
}

func newFakeNode(t *testing.T) (*Node, string) {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "server.sh")
	NewWithT(t).Expect(os.WriteFile(script, []byte(fakeServer), 0o700)).To(Succeed())
	cfg := &Config{Dir: dir, Nodes: map[string]Instance{Owner: {}}}
	return &Node{Name: Owner, Config: cfg, ServerPath: script}, dir
}

func assertGone(t *testing.T, pids ...int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		alive := false
		for _, p := range pids {
			alive = alive || Alive(p)
		}
		if !alive {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	NewWithT(t).Expect(false).To(BeTrue(), "processes survived the teardown: %v", pids)
}

// A failing scenario ends in Stop: the helpers that ignore SIGTERM must go.
func TestStopReapsDescendantsThatIgnoreSIGTERM(t *testing.T) {
	n, dir := newFakeNode(t)
	NewWithT(t).Expect(n.Start()).To(Succeed())
	helpers := helperPIDs(t, dir)
	n.Stop()
	assertGone(t, append(helpers, n.PID())...)
	n.Stop() // idempotent
}

// A scenario that kills the server on purpose orphans its helpers; Stop must
// still reach them, across a restart of the same node.
func TestStopReapsOrphansLeftByKill(t *testing.T) {
	n, dir := newFakeNode(t)
	NewWithT(t).Expect(n.Start()).To(Succeed())
	first := helperPIDs(t, dir)
	n.Kill()
	for _, p := range first {
		NewWithT(t).Expect(Alive(p)).To(BeTrue(), "Kill must leave the descendants running, %d is gone", p)
	}
	_ = os.Remove(filepath.Join(dir, "ready"))
	_ = os.Remove(filepath.Join(dir, "helpers"))
	NewWithT(t).Expect(n.Start()).To(Succeed())
	second := helperPIDs(t, dir)
	n.Stop()
	assertGone(t, append(first, second...)...)
}
