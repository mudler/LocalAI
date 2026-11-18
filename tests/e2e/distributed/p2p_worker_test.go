//go:build linux

package distributed_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/testcontainers/testcontainers-go"
	tcnats "github.com/testcontainers/testcontainers-go/modules/nats"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// A worker that joins with a worker invite, with real processes: a frontend in
// P2P mode share on a cluster that runs on NATS, and a worker started with
// `local-ai worker --join`. The worker has no address and no registration
// token; it reaches the frontend over P2P only, and the frontend reaches it
// through its tunnel over P2P while NATS is the active carrier.
//
// It runs with `make test-e2e-distributed-p2p-worker` and needs what the
// carrier switch scenario needs: docker and a Go toolchain, or LOCALAI_E2E_BIN
// and LOCALAI_E2E_MOCK_BACKEND.
var _ = Describe("A worker that joins over P2P, with real processes", Ordered, Label("P2PWorker"), func() {
	var (
		stack  *switchStack
		fe     *switchFrontend
		worker *switchProc
		p2pAt  string
		link   string
		args   []string
		dir    string
	)

	const (
		settle = 120 * time.Second
		poll   = 500 * time.Millisecond
		name   = "worker-p2p"
	)

	type p2pNode struct {
		ID              string `json:"id"`
		Name            string `json:"name"`
		Status          string `json:"status"`
		Address         string `json:"address"`
		PeerID          string `json:"peer_id"`
		Attached        string `json:"attached"`
		FollowError     string `json:"follow_error"`
		TunnelTransport string `json:"tunnel_transport"`
		TunnelPath      string `json:"tunnel_path"`
	}
	node := func() (p2pNode, bool) {
		code, body := stack.api(fe, http.MethodGet, "/api/nodes", nil)
		if code != http.StatusOK {
			return p2pNode{}, false
		}
		var all []p2pNode
		if json.Unmarshal(body, &all) != nil {
			return p2pNode{}, false
		}
		for _, n := range all {
			if n.Name == name {
				return n, true
			}
		}
		return p2pNode{}, false
	}

	BeforeAll(func() {
		if _, err := exec.LookPath("docker"); err != nil {
			Skip("docker is not installed")
		}
		stack = &switchStack{root: GinkgoT().TempDir(), httpClient: &http.Client{Timeout: 60 * time.Second}}
		var skip string
		stack.localAI, stack.mock, skip = switchBinaries(stack.root)
		if skip != "" {
			Skip(skip)
		}

		ctx := context.Background()
		pg, err := tcpostgres.Run(ctx, "postgres:16-alpine",
			tcpostgres.WithDatabase("p2pworker"), tcpostgres.WithUsername("test"), tcpostgres.WithPassword("test"),
			testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second)))
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = pg.Terminate(context.Background()) })
		stack.pgURL, err = pg.ConnectionString(ctx, "sslmode=disable")
		Expect(err).ToNot(HaveOccurred())

		nc, err := tcnats.Run(ctx, "nats:2-alpine")
		Expect(err).ToNot(HaveOccurred())
		DeferCleanup(func() { _ = nc.Terminate(context.Background()) })
		stack.natsURL, err = nc.ConnectionString(ctx)
		Expect(err).ToNot(HaveOccurred())

		DeferCleanup(stack.stopAll)

		p2pPort := switchFreePort()
		p2pAt = fmt.Sprintf("/ip4/127.0.0.1/tcp/%d", p2pPort)
		fe = stack.frontend("frontend-p2p", switchFreePort(),
			"LOCALAI_P2P_MODE=share",
			"LOCALAI_P2P_LISTEN_MADDRS="+p2pAt,
		)
		Eventually(func() error {
			_, err := stack.carrier(fe)
			return err
		}, settle, poll).Should(Succeed(), "the frontend did not come up")
	})

	JustAfterEach(func() {
		if CurrentSpecReport().Failed() && stack != nil {
			stack.dumpLogs()
		}
	})

	It("makes a worker invite on a frontend in mode share, with no QR code", func() {
		var status struct {
			Mode   string `json:"mode"`
			PeerID string `json:"peer_id"`
		}
		Eventually(func() string {
			code, body := stack.api(fe, http.MethodGet, "/api/p2p/status", nil)
			if code != http.StatusOK || json.Unmarshal(body, &status) != nil {
				return ""
			}
			return status.Mode
		}, settle, poll).Should(Equal("share"))
		Expect(status.PeerID).ToNot(BeEmpty())
		p2pAt += "/p2p/" + status.PeerID

		code, body := stack.api(fe, http.MethodPost, "/api/p2p/invites", map[string]any{"kind": "worker", "label": name, "relays": "none"})
		Expect(code).To(Equal(http.StatusCreated), string(body))
		var minted struct {
			Link string `json:"link"`
			QR   bool   `json:"qr"`
		}
		Expect(json.Unmarshal(body, &minted)).To(Succeed())
		Expect(minted.QR).To(BeFalse())
		link = minted.Link
	})

	It("joins with the invite, registers over P2P without a token, and waits for approval", func() {
		dir = filepath.Join(stack.root, name)
		backend := filepath.Join(dir, "backends", "mock")
		Expect(os.MkdirAll(backend, 0o755)).To(Succeed())
		Expect(os.MkdirAll(filepath.Join(dir, "models"), 0o755)).To(Succeed())
		run := fmt.Sprintf("#!/bin/sh\nexec %s \"$@\"\n", stack.mock)
		Expect(os.WriteFile(filepath.Join(backend, "run.sh"), []byte(run), 0o755)).To(Succeed())
		args = []string{"worker",
			"--join", link,
			// The invite carries public addresses only, and this frontend has none.
			"--p2p-bootstrap", p2pAt,
			"--backends-path", filepath.Join(dir, "backends"),
			"--backends-system-path", filepath.Join(dir, "nosys"),
			"--models-path", filepath.Join(dir, "models"),
			"--node-name", name,
			"--serve-addr", fmt.Sprintf("127.0.0.1:%d", workerBasePort()),
			"--log-level", "debug",
		}
		worker = stack.start(name, dir, []string{"LOCALAI_HEARTBEAT_INTERVAL=3s", "LOCALAI_FOLLOW_MAX_DELAY=2s"}, args...)

		var n p2pNode
		Eventually(func() bool {
			var ok bool
			n, ok = node()
			return ok
		}, settle, poll).Should(BeTrue(), "the worker did not register")
		// Automatic approval is on for this cluster, and a worker over P2P still waits.
		Expect(n.Status).To(Equal("pending"))
		Expect(n.PeerID).ToNot(BeEmpty())
		Expect(n.Address).To(BeEmpty())
	})

	It("is reached through its P2P tunnel on a cluster that runs on NATS once approved, and answers a chat", func() {
		n, _ := node()
		code, body := stack.api(fe, http.MethodPost, "/api/nodes/"+n.ID+"/approve", nil)
		Expect(code).To(Equal(http.StatusOK), string(body))

		Eventually(func() string {
			n, _ := node()
			return n.Status + "/" + n.Attached + "/" + n.TunnelTransport
		}, settle, poll).Should(Equal("healthy/tunnel/p2p"))
		r, err := stack.carrier(fe)
		Expect(err).ToNot(HaveOccurred())
		Expect(r.Active).To(Equal("nats"))

		Eventually(func() error { return stack.chat(fe) }, settle, poll).Should(Succeed())

		n, _ = node()
		Expect(n.FollowError).To(ContainSubstring("joined over P2P"))
		Expect(n.TunnelPath).To(Equal("direct"))
	})

	It("comes back after a restart with the same command, without redeeming the used invite again", func() {
		worker.killGroup()
		Eventually(worker.done, 20*time.Second).Should(BeClosed())
		worker = stack.start(name, dir, []string{"LOCALAI_HEARTBEAT_INTERVAL=3s", "LOCALAI_FOLLOW_MAX_DELAY=2s"}, args...)
		Eventually(func() error { return stack.chat(fe) }, settle, poll).Should(Succeed())
		n, _ := node()
		Expect(n.Status).To(Equal("healthy"))
	})
})
