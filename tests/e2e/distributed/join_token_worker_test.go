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
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/testcontainers/testcontainers-go"
	tcnats "github.com/testcontainers/testcontainers-go/modules/nats"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// A worker that joins with a join token, with real processes: a frontend with a
// registration token, and a worker that has only the join token. It needs no
// registration token, answers a chat through its tunnel, restarts with no token,
// and the registration token no longer opens its node routes.
//
// It runs with `make test-e2e-distributed-join-token` and needs docker and a Go
// toolchain, or LOCALAI_E2E_BIN and LOCALAI_E2E_MOCK_BACKEND (see
// process_harness_test.go).
var _ = Describe("A worker that joins with a join token, with real processes", Ordered, Label("JoinTokenWorker"), func() {
	var (
		stack *procStack
		fe    *stackFrontend
		dir   string
		token string
		tokID string
	)

	const (
		settle = 120 * time.Second
		poll   = 500 * time.Millisecond
		name   = "worker-join"
	)

	type nodeRow struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Status   string `json:"status"`
		Attached string `json:"attached"`
	}
	node := func(want string) (nodeRow, bool) {
		code, body := stack.api(fe, http.MethodGet, "/api/nodes", nil)
		if code != http.StatusOK {
			return nodeRow{}, false
		}
		var all []nodeRow
		if json.Unmarshal(body, &all) != nil {
			return nodeRow{}, false
		}
		for _, n := range all {
			if n.Name == want {
				return n, true
			}
		}
		return nodeRow{}, false
	}
	call := func(method, path, bearer, body string) int {
		req, err := http.NewRequest(method, fe.url()+path, strings.NewReader(body))
		Expect(err).ToNot(HaveOccurred())
		req.Header.Set("Content-Type", "application/json")
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, err := stack.httpClient.Do(req)
		Expect(err).ToNot(HaveOccurred())
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	workerArgs := func(extra ...string) []string {
		return append([]string{"worker",
			"--register-to", fe.url(),
			"--routes", "websocket",
			"--backends-path", filepath.Join(dir, "backends"),
			"--backends-system-path", filepath.Join(dir, "nosys"),
			"--models-path", filepath.Join(dir, "models"),
			"--node-name", name,
			"--serve-addr", fmt.Sprintf("127.0.0.1:%d", workerBasePort()),
			"--log-level", "debug",
		}, extra...)
	}

	BeforeAll(func() {
		if _, err := exec.LookPath("docker"); err != nil {
			Skip("docker is not installed")
		}
		stack = &procStack{root: GinkgoT().TempDir(), httpClient: &http.Client{Timeout: 60 * time.Second}}
		var skip string
		stack.localAI, stack.mock, skip = stackBinaries(stack.root)
		if skip != "" {
			Skip(skip)
		}
		ctx := context.Background()
		pg, err := tcpostgres.Run(ctx, "postgres:16-alpine",
			tcpostgres.WithDatabase("jointoken"), tcpostgres.WithUsername("test"), tcpostgres.WithPassword("test"),
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

		fe = stack.frontend("frontend-join", freePort())
		Eventually(func() error {
			_, err := stack.busOf(fe)
			return err
		}, settle, poll).Should(Succeed(), "the frontend did not come up")

		dir = filepath.Join(stack.root, name)
		backend := filepath.Join(dir, "backends", "mock")
		Expect(os.MkdirAll(backend, 0o755)).To(Succeed())
		Expect(os.MkdirAll(filepath.Join(dir, "models"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(backend, "run.sh"), []byte(fmt.Sprintf("#!/bin/sh\nexec %s \"$@\"\n", stack.mock)), 0o755)).To(Succeed())
	})

	JustAfterEach(func() {
		if CurrentSpecReport().Failed() && stack != nil {
			stack.dumpLogs()
		}
	})

	It("mints a token, serves its scripts without spending it, and refuses a script for another token", func() {
		code, body := stack.api(fe, http.MethodPost, "/api/cluster/join-tokens", map[string]any{"routes": []string{"websocket"}, "label": name})
		Expect(code).To(Equal(http.StatusCreated), string(body))
		var minted struct {
			ID       string `json:"id"`
			Token    string `json:"token"`
			Commands struct {
				InstallScript string `json:"install_script"`
			} `json:"commands"`
		}
		Expect(json.Unmarshal(body, &minted)).To(Succeed())
		token, tokID = minted.Token, minted.ID
		Expect(minted.Commands.InstallScript).To(ContainSubstring(fe.url() + "/join.sh?t="))

		// The scripts are public: no credential, and no use is spent.
		for _, s := range []string{"/join.sh", "/join.ps1", "/join.yaml"} {
			Expect(call(http.MethodGet, s+"?t="+token, "", "")).To(Equal(http.StatusOK), s)
		}
		Expect(call(http.MethodGet, "/join.sh?t=ljt_0000000000000000.nope", "", "")).To(Equal(http.StatusNotFound))
		_, listed := stack.api(fe, http.MethodGet, "/api/cluster/join-tokens", nil)
		Expect(string(listed)).To(ContainSubstring(`"state":"unused"`))
		Expect(string(listed)).ToNot(ContainSubstring(token))
	})

	It("joins with the token alone, keeps its credential, and answers a chat", func() {
		stack.start(name, dir, []string{"LOCALAI_HEARTBEAT_INTERVAL=3s"}, workerArgs("--join-token", token)...)
		Eventually(func() string {
			n, _ := node(name)
			return n.Status + "/" + n.Attached
		}, settle, poll).Should(Equal("healthy/tunnel"))
		Eventually(func() error { return stack.chat(fe) }, settle, poll).Should(Succeed())

		cred, err := os.ReadFile(filepath.Join(dir, "data", "node-credential.json"))
		Expect(err).ToNot(HaveOccurred())
		Expect(string(cred)).To(ContainSubstring("node_token"))
		// Some filesystems (a network share) do not keep permission bits: check the
		// mode only where a probe file keeps the one it was given.
		probe := filepath.Join(dir, "mode-probe")
		Expect(os.WriteFile(probe, nil, 0o600)).To(Succeed())
		if pi, err := os.Stat(probe); err == nil && pi.Mode().Perm() == 0o600 {
			info, err := os.Stat(filepath.Join(dir, "data", "node-credential.json"))
			Expect(err).ToNot(HaveOccurred())
			Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)))
		}

		_, listed := stack.api(fe, http.MethodGet, "/api/cluster/join-tokens", nil)
		Expect(string(listed)).To(ContainSubstring(`"state":"used_up"`))
		Expect(string(listed)).To(ContainSubstring(tokID))
	})

	It("refuses the registration token on the node routes, and a spent token for a second worker", func() {
		n, ok := node(name)
		Expect(ok).To(BeTrue())
		Expect(call(http.MethodPost, "/api/node/"+n.ID+"/heartbeat", stackRegToken, `{}`)).To(Equal(http.StatusUnauthorized))
		Expect(call(http.MethodPost, "/api/node/"+n.ID+"/drain", stackRegToken, "")).To(Equal(http.StatusUnauthorized))
		Expect(call(http.MethodPost, "/api/node/register", "", `{"name":"intruder","join_token":"`+token+`"}`)).To(Equal(http.StatusUnauthorized))
		_, found := node("intruder")
		Expect(found).To(BeFalse())
	})

	It("restarts with no token and joins again with its own credential", func() {
		for _, p := range stack.procs {
			if p.name == name {
				p.killGroup()
			}
		}
		stack.start(name, dir, []string{"LOCALAI_HEARTBEAT_INTERVAL=3s"}, workerArgs()...)
		Eventually(func() string {
			n, _ := node(name)
			return n.Status + "/" + n.Attached
		}, settle, poll).Should(Equal("healthy/tunnel"))
		Eventually(func() error { return stack.chat(fe) }, settle, poll).Should(Succeed())
	})
})
