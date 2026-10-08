//go:build linux

package distributed_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/testcontainers/testcontainers-go"
	tcnats "github.com/testcontainers/testcontainers-go/modules/nats"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// The scenario below changes the carrier of a cluster of real processes: two
// frontends and two workers, with PostgreSQL and NATS in containers. It
// follows one cluster through the whole life of a change: from NATS to the
// tunnel and back, a refused change, a forced change, a frontend and a worker
// that die during a change, and an abort. The specs depend on each other, so
// the container is Ordered.
//
// It runs with `make test-e2e-distributed-switch`. It needs docker and a Go
// toolchain (or LOCALAI_E2E_BIN and LOCALAI_E2E_MOCK_BACKEND), and skips when
// one is missing. The plain `make test-e2e-distributed` does not run it,
// because it starts many processes and takes a few minutes.
var _ = Describe("Carrier switch with real processes", Ordered, Label("CarrierSwitch"), func() {
	var (
		stack *switchStack
		feA   *switchFrontend
		feB   *switchFrontend
		dual  *switchWorker
		tun   *switchWorker
	)

	const (
		settle  = 90 * time.Second
		poll    = 500 * time.Millisecond
		dualN   = "worker-dual"
		tunnelN = "worker-tunnel"
	)

	// request sends a change and returns the status and the body.
	request := func(fe *switchFrontend, body map[string]any) (int, []byte) {
		return stack.api(fe, http.MethodPost, "/api/cluster/carrier", body)
	}
	// stableOn waits until the cluster is stable on the carrier, as one frontend sees it.
	stableOn := func(fe *switchFrontend, carrier string) {
		GinkgoHelper()
		Eventually(func() string {
			r, err := stack.carrier(fe)
			if err != nil {
				return "error: " + err.Error()
			}
			return r.State + "/" + r.Active
		}, settle, poll).Should(Equal("stable/" + carrier))
	}
	waitInState := func(fe *switchFrontend, state string) {
		GinkgoHelper()
		Eventually(func() string {
			r, err := stack.carrier(fe)
			if err != nil {
				return "error: " + err.Error()
			}
			return r.State
		}, settle, poll).Should(Equal(state))
	}
	chatOK := func(fes ...*switchFrontend) {
		GinkgoHelper()
		for _, fe := range fes {
			Eventually(func() error { return stack.chat(fe) }, settle, poll).Should(Succeed())
		}
	}
	dualBackendPID := func() int {
		GinkgoHelper()
		var pids []int
		Eventually(func() int {
			pids = backendPIDs(dual)
			return len(pids)
		}, settle, poll).Should(BeNumerically(">=", 1), "the dual-capable worker runs no backend")
		return pids[0]
	}

	BeforeAll(func() {
		if testing := os.Getenv("LOCALAI_E2E_SKIP_SWITCH"); testing != "" {
			Skip("LOCALAI_E2E_SKIP_SWITCH is set")
		}
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
			tcpostgres.WithDatabase("switch"), tcpostgres.WithUsername("test"), tcpostgres.WithPassword("test"),
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

		// The first frontend seeds the row of the carrier, so it starts alone.
		feA = stack.frontend("frontend-a", switchFreePort())
		Eventually(func() error {
			_, err := stack.carrier(feA)
			return err
		}, 120*time.Second, poll).Should(Succeed(), "frontend A did not come up")
		feB = stack.frontend("frontend-b", switchFreePort())
		Eventually(func() error {
			_, err := stack.carrier(feB)
			return err
		}, 120*time.Second, poll).Should(Succeed(), "frontend B did not come up")
	})

	JustAfterEach(func() {
		if CurrentSpecReport().Failed() && stack != nil {
			stack.dumpLogs()
		}
	})

	It("starts on NATS because the first frontend was given a NATS address", func() {
		r, err := stack.carrier(feA)
		Expect(err).ToNot(HaveOccurred())
		Expect(r.Active).To(Equal("nats"))
		Expect(r.State).To(Equal("stable"))
		Eventually(func() int {
			r, _ := stack.carrier(feA)
			return len(r.Replicas)
		}, settle, poll).Should(Equal(2))

		// Short waits keep the run fast. They are the least the API accepts.
		code, body := stack.api(feA, http.MethodPut, "/api/cluster/settings", map[string]any{
			"prepare_timeout": "45s", "transition_window": "45s", "max_drain": "10s",
		})
		Expect(code).To(Equal(http.StatusOK), string(body))
	})

	It("loads a model on NATS and answers through both frontends", func() {
		dual = stack.worker(dualN, feA.url(), true)
		Eventually(func() bool { return stack.workerAttached(feA, dualN, "nats") }, settle, poll).Should(BeTrue())
		Eventually(func() string {
			n, _ := stack.node(feB, dualN)
			return n.Status
		}, settle, poll).Should(Equal("healthy"))
		chatOK(feA, feB)
	})

	It("answers a dry run to the tunnel and changes nothing", func() {
		before, err := stack.carrier(feA)
		Expect(err).ToNot(HaveOccurred())

		code, body := request(feA, map[string]any{"target": "tunnel", "dry_run": true})
		Expect(code).To(Equal(http.StatusOK), string(body))
		var r carrierReport
		Expect(json.Unmarshal(body, &r)).To(Succeed())
		Expect(r.OK).To(BeTrue(), string(body))
		Expect(r.Blockers).To(BeEmpty())
		Expect(r.Replicas).To(HaveLen(2), "the replica list is what the admin confirms")

		after, err := stack.carrier(feB)
		Expect(err).ToNot(HaveOccurred())
		Expect(after.Epoch).To(Equal(before.Epoch))
		Expect(after.Active).To(Equal("nats"))
	})

	It("rejects a target that is not a carrier", func() {
		code, _ := request(feA, map[string]any{"target": "carrier-pigeon"})
		Expect(code).To(Equal(http.StatusBadRequest))
	})

	It("changes to the tunnel while both frontends serve chat, and the worker follows without a restart", func() {
		pid := dualBackendPID()
		workerPID := dual.pid()
		before, _ := stack.carrier(feA)

		load := stack.startChatLoad(feA, feB)
		code, body := request(feA, map[string]any{"target": "tunnel"})
		Expect(code).To(Equal(http.StatusAccepted), string(body))

		stableOn(feA, "tunnel")
		stableOn(feB, "tunnel")
		Eventually(func() bool { return stack.workerAttached(feA, dualN, "tunnel") }, settle, poll).Should(BeTrue())
		// Let the load run across the drain too, then look at the whole run.
		Eventually(func() bool {
			r, err := stack.carrier(feA)
			return err == nil && r.Epoch > before.Epoch && contains(workerAttached(r, dualN), "tunnel")
		}, settle, poll).Should(BeTrue())
		Eventually(func() []string {
			r, _ := stack.carrier(feA)
			return workerAttached(r, dualN)
		}, settle, poll).Should(Equal([]string{"tunnel"}), "the old carrier closes after the drain")

		ok, errs := load.finish()
		Expect(errs).To(BeEmpty(), "chat failed during the change")
		Expect(ok).To(BeNumerically(">", 20))

		Expect(dual.alive()).To(BeTrue())
		Expect(dual.pid()).To(Equal(workerPID))
		Expect(backendPIDs(dual)).To(ContainElement(pid), "the backend was restarted")
		chatOK(feA, feB)
	})

	It("takes a tunnel-only worker, which has no address, on the new carrier", func() {
		tun = stack.worker(tunnelN, feB.url(), false)
		Eventually(func() bool { return stack.workerAttached(feA, tunnelN, "tunnel") }, settle, poll).Should(BeTrue())
		n, ok := stack.node(feA, tunnelN)
		Expect(ok).To(BeTrue())
		Expect(n.Status).To(Equal("healthy"))
	})

	It("refuses a change back to NATS because the tunnel-only worker cannot follow", func() {
		code, body := request(feA, map[string]any{"target": "nats", "dry_run": true})
		Expect(code).To(Equal(http.StatusOK), string(body))
		var r carrierReport
		Expect(json.Unmarshal(body, &r)).To(Succeed())
		Expect(r.OK).To(BeFalse())
		var found bool
		for _, b := range r.Blockers {
			if b.Kind == "worker" || b.ID != "" {
				w, _ := r.worker(tunnelN)
				found = found || b.ID == w.ID
				Expect(b.Forceable).To(BeTrue())
			}
		}
		Expect(found).To(BeTrue(), "the blockers do not name the tunnel-only worker: %s", body)

		code, body = request(feA, map[string]any{"target": "nats"})
		Expect(code).To(Equal(http.StatusUnprocessableEntity), string(body))
		r2, err := stack.carrier(feA)
		Expect(err).ToNot(HaveOccurred())
		Expect(r2.Active).To(Equal("tunnel"))
		Expect(r2.State).To(Equal("stable"))
	})

	It("forces the change back: the tunnel-only worker is left unroutable but is not removed", func() {
		pid := dualBackendPID()
		tunnelNode, ok := stack.node(feA, tunnelN)
		Expect(ok).To(BeTrue())

		load := stack.startChatLoad(feA, feB)
		code, body := request(feA, map[string]any{"target": "nats", "force": true})
		Expect(code).To(Equal(http.StatusAccepted), string(body))
		stableOn(feA, "nats")
		stableOn(feB, "nats")
		Eventually(func() []string {
			r, _ := stack.carrier(feA)
			return workerAttached(r, dualN)
		}, settle, poll).Should(Equal([]string{"nats"}))

		// The tunnel-only worker holds a replica of the model, and the frontends
		// dial a replica by its address. At the flip, a request that picked that
		// replica is refused by the connection: the worker was named in the
		// blockers and the force accepted it. Any other failure is a bug.
		ok2, errs := load.finish()
		for _, err := range errs {
			Expect(err.Error()).To(ContainSubstring("connection refused"), "chat failed during the forced change")
		}
		Expect(len(errs)).To(BeNumerically("<=", 10), "the unroutable worker kept receiving requests")
		Expect(ok2).To(BeNumerically(">", 10))
		Expect(backendPIDs(dual)).To(ContainElement(pid))

		// The worker that could not follow reports why, and is still registered.
		Eventually(func() string {
			r, _ := stack.carrier(feA)
			w, _ := r.worker(tunnelN)
			return w.FollowError
		}, settle, poll).ShouldNot(BeEmpty())
		// It stays in the list for as long as it heartbeats.
		Consistently(func() string {
			n, ok := stack.node(feA, tunnelN)
			if !ok {
				return "removed"
			}
			return n.ID
		}, 15*time.Second, time.Second).Should(Equal(tunnelNode.ID))
		// Once it is unroutable no request is sent to it.
		Eventually(func() error {
			for range 10 {
				for _, fe := range []*switchFrontend{feA, feB} {
					if err := stack.chat(fe); err != nil {
						return err
					}
				}
			}
			return nil
		}, settle, poll).Should(Succeed())
	})

	It("restarts the tunnel-only worker as a dual-capable one, and it joins NATS", func() {
		tun.kill()
		tun.killGroup()
		// The same node name registers again, now with an address.
		tun = stack.worker(tunnelN, feA.url(), true)
		Eventually(func() bool { return stack.workerAttached(feA, tunnelN, "nats") }, settle, poll).Should(BeTrue())
	})

	It("resolves a change when a frontend dies during prepare, and the frontend rebuilds from the row", func() {
		// Frozen, B cannot report that it is ready. Killed while frozen, it
		// never does, and the change has to end on its own.
		feB.signal(syscall.SIGSTOP)
		code, body := request(feA, map[string]any{"target": "tunnel", "force": true})
		Expect(code).To(Equal(http.StatusAccepted), string(body))
		waitInState(feA, "prepare")
		feB.signal(syscall.SIGCONT)
		feB.kill()

		// A forced change goes on without the replica that is not ready; an
		// unforced one is aborted. Either way the row leaves prepare.
		Eventually(func() string {
			r, err := stack.carrier(feA)
			if err != nil {
				return "error"
			}
			return r.State
		}, 3*time.Minute, poll).Should(Equal("stable"))
		r, _ := stack.carrier(feA)
		active := r.Active
		chatOK(feA)

		stack.restart(feB)
		Eventually(func() error {
			_, err := stack.carrier(feB)
			return err
		}, 120*time.Second, poll).Should(Succeed())
		rb, err := stack.carrier(feB)
		Expect(err).ToNot(HaveOccurred())
		Expect(rb.Active).To(Equal(active), "the restarted frontend must follow the row, not its flags")
		chatOK(feA, feB)
		// Back on NATS for the next specs. A change to the carrier that is
		// already active is refused, so go only when needed.
		if active != "nats" {
			Eventually(func() int {
				c, _ := request(feA, map[string]any{"target": "nats", "force": true})
				return c
			}, settle, poll).Should(Equal(http.StatusAccepted))
			stableOn(feA, "nats")
			stableOn(feB, "nats")
		}
	})

	It("survives a worker that dies while it follows, and the restarted worker attaches to the active carrier", func() {
		code, body := request(feA, map[string]any{"target": "tunnel", "force": true})
		Expect(code).To(Equal(http.StatusAccepted), string(body))
		// The commit makes the tunnel active. The worker learns it from its next
		// heartbeat and then waits a random time of up to two seconds before it
		// attaches, so it is killed in the middle of the follow.
		Eventually(func() string {
			r, _ := stack.carrier(feA)
			return r.Active
		}, settle, 100*time.Millisecond).Should(Equal("tunnel"))
		dual.kill()
		dual.killGroup()
		stableOn(feA, "tunnel")

		stack.restartWorker(dual)
		Eventually(func() bool { return stack.workerAttached(feA, dualN, "tunnel") }, settle, poll).Should(BeTrue())
		chatOK(feA, feB)
	})

	It("aborts a change in prepare and stays on the carrier it had", func() {
		before, err := stack.carrier(feA)
		Expect(err).ToNot(HaveOccurred())
		target := "nats"
		if before.Active == "nats" {
			target = "tunnel"
		}

		feB.signal(syscall.SIGSTOP)
		DeferCleanup(func() { feB.signal(syscall.SIGCONT) })
		code, body := request(feA, map[string]any{"target": target, "force": true})
		Expect(code).To(Equal(http.StatusAccepted), string(body))
		waitInState(feA, "prepare")

		code, body = request(feA, map[string]any{"abort": true})
		Expect(code).To(Equal(http.StatusOK), string(body))
		feB.signal(syscall.SIGCONT)

		stableOn(feA, before.Active)
		stableOn(feB, before.Active)
		after, _ := stack.carrier(feA)
		Expect(after.Epoch).To(BeNumerically(">", before.Epoch), "an abort is a change of the row")
		chatOK(feA, feB)

		// There is nothing left to abort.
		code, _ = request(feA, map[string]any{"abort": true})
		Expect(code).To(Equal(http.StatusConflict))
	})

	It("keeps the NATS address it was given: saving one does not switch", func() {
		code, body := stack.api(feA, http.MethodPut, "/api/cluster/settings", map[string]any{"nats_url": stack.natsURL})
		Expect(code).To(Equal(http.StatusOK), string(body))
		before, _ := stack.carrier(feA)
		Consistently(func() string {
			r, _ := stack.carrier(feA)
			return r.State + "/" + r.Active
		}, 6*time.Second, time.Second).Should(Equal(before.State + "/" + before.Active))
	})
})

func workerAttached(r carrierReport, name string) []string {
	w, _ := r.worker(name)
	return w.Attached
}
