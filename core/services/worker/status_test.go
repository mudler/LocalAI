//go:build launcherstatus

// SPDX-License-Identifier: MIT
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/mudler/LocalAI/core/cli/workerregistry"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/pkg/natsauth"
	"github.com/mudler/LocalAI/pkg/workerstatus"
	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// This bounded production-component fixture executes the approval observer and
// composite readiness used by Run, plus the actual credential RefreshLoop. It
// does not execute all of Run or the full frontend/model/inference LW08 fixture.
var _ = Describe("Worker status", func() {
	It("observes pending approval, credentials, real lifecycle subscriptions, heartbeat rejection and NATS recovery", func() {
		dir := GinkgoT().TempDir()
		operator, err := nkeys.CreateOperator()
		Expect(err).NotTo(HaveOccurred())
		defer operator.Wipe()
		op, err := operator.PublicKey()
		Expect(err).NotTo(HaveOccurred())
		operatorJWT, err := jwt.NewOperatorClaims(op).Encode(operator)
		Expect(err).NotTo(HaveOccurred())
		account, err := nkeys.CreateAccount()
		Expect(err).NotTo(HaveOccurred())
		defer account.Wipe()
		ap, err := account.PublicKey()
		Expect(err).NotTo(HaveOccurred())
		accountJWT, err := jwt.NewAccountClaims(ap).Encode(operator)
		Expect(err).NotTo(HaveOccurred())
		seed, err := account.Seed()
		Expect(err).NotTo(HaveOccurred())
		auth := natsauth.Config{AccountSeed: string(seed)}
		token, userSeed, err := auth.MintWorkerJWT("status-node", "backend")
		Expect(err).NotTo(HaveOccurred())
		claims, err := jwt.DecodeUserClaims(token)
		Expect(err).NotTo(HaveOccurred())
		claims.Expires = 0
		token, err = claims.Encode(account)
		Expect(err).NotTo(HaveOccurred())
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		Expect(err).NotTo(HaveOccurred())
		port := listener.Addr().(*net.TCPAddr).Port
		Expect(listener.Close()).To(Succeed())
		config := filepath.Join(dir, "nats.conf")
		Expect(os.WriteFile(config, []byte(fmt.Sprintf("listen: 127.0.0.1:%d\noperator: %s\nresolver: MEMORY\nresolver_preload: { %s: %s }\n", port, operatorJWT, ap, accountJWT)), 0600)).To(Succeed())
		binary, err := exec.LookPath("nats-server")
		Expect(err).NotTo(HaveOccurred(), "status verifier supplies pinned nats-server")
		var server *exec.Cmd
		start := func() {
			server = exec.Command(binary, "-c", config)
			Expect(server.Start()).To(Succeed())
			Eventually(func() bool {
				c, e := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
				if e != nil {
					return false
				}
				c.Close()
				return true
			}, 5*time.Second).Should(BeTrue())
		}
		stop := func() {
			if server != nil {
				Expect(server.Process.Signal(os.Interrupt)).To(Succeed())
				Expect(server.Wait()).To(Succeed())
				server = nil
			}
		}
		start()
		DeferCleanup(stop)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		path, generation, err := workerstatus.NewSession(dir)
		Expect(err).NotTo(HaveOccurred())
		publisher, err := workerstatus.NewPublisher(ctx, path, generation, cancel)
		Expect(err).NotTo(HaveOccurred())
		var approved, issued, rejected atomic.Bool
		frontend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer registration-secret" {
				w.WriteHeader(401)
				return
			}
			if r.URL.Path == "/api/node/register" || r.URL.Path == "/api/node/status-node/status" {
				res := workerregistry.RegisterResponse{ID: "status-node", Status: "pending"}
				if approved.Load() {
					res.Status = "healthy"
				}
				if issued.Load() && r.Method == http.MethodPost {
					res.NatsJWT = token
					res.NatsUserSeed = userSeed
				}
				_ = json.NewEncoder(w).Encode(res)
				return
			}
			if rejected.Load() {
				w.WriteHeader(403)
				return
			}
			w.WriteHeader(204)
		}))
		defer frontend.Close()
		client := &workerregistry.RegistrationClient{FrontendURL: frontend.URL, RegistrationToken: "registration-secret", ObserveRegistration: func(res *workerregistry.RegisterResponse, err error) {
			if res == nil {
				publisher.Registration("error", false, err)
				return
			}
			publisher.Registration(res.Status, res.NatsJWT != "" && res.NatsUserSeed != "", err)
		}}
		tracker := workerstatus.Tracker{ExpectedPID: os.Getpid(), Generation: generation, Started: time.Now()}
		classify := func() string {
			s, e := workerstatus.Read(path, generation)
			if e != nil {
				return "unavailable"
			}
			return tracker.Classify(s, time.Now())
		}
		manager := workerregistry.NewNATSCredentialManager(func(ctx context.Context) (*workerregistry.RegisterResponse, error) {
			return client.RegisterFull(ctx, nil)
		}, true)
		acquired := make(chan error, 1)
		go func() { _, e := manager.Acquire(ctx); acquired <- e }()
		Eventually(classify, 3*time.Second).Should(Equal("pending"))
		approved.Store(true)
		Eventually(classify, 5*time.Second).Should(Equal("credentials"))
		issued.Store(true)
		Eventually(acquired, 10*time.Second).Should(Receive(BeNil()))
		bus, err := messaging.New(fmt.Sprintf("nats://127.0.0.1:%d", port), messaging.WithUserJWTProvider(manager.Provider()))
		Expect(err).NotTo(HaveOccurred())
		defer bus.Close()
		readiness := &nodes.WorkerReadiness{}
		Expect(readiness.Check()).To(Succeed())
		Expect(classify()).NotTo(Equal("ready"))
		supervisor := &backendSupervisor{nodeID: "status-node"}
		Expect(supervisor.registerLifecycleVerbs(newNATSControlServer(bus, "status-node"))).To(Succeed())
		armWorkerStatus(publisher, readiness, bus, supervisor)
		Expect(classify()).To(Equal("heartbeat"))
		publisher.Heartbeat(client.Heartbeat(ctx, "status-node", nil))
		Expect(classify()).To(Equal("ready"))
		// Execute the production observer, independently of RefreshLoop. Static
		// overrides use this identical observer; it never touches credentials.
		go observeApproval(ctx, publisher, client, "status-node", 20*time.Millisecond)
		refreshed := make(chan error, 1)
		go func() { refreshed <- manager.RefreshLoop(ctx) }()
		approved.Store(false)
		Eventually(classify, 3*time.Second).Should(Equal("pending"))
		publisher.Heartbeat(client.Heartbeat(ctx, "status-node", nil))
		Expect(bus.IsConnected()).To(BeTrue())
		Expect(classify()).To(Equal("pending"))
		approved.Store(true)
		Eventually(classify, 3*time.Second).Should(Equal("ready"))
		supervisor.mu.Lock()
		supervisor.processes = map[string]*backendProcess{"dead": {addr: "127.0.0.1:1", serving: true}}
		supervisor.mu.Unlock()
		publisher.Update(nil)
		Expect(classify()).To(Equal("readiness"))
		supervisor.mu.Lock()
		supervisor.processes = nil
		supervisor.mu.Unlock()
		publisher.Update(nil)
		Expect(classify()).To(Equal("ready"))
		rejected.Store(true)
		publisher.Heartbeat(client.Heartbeat(ctx, "status-node", nil))
		Expect(classify()).To(Equal("heartbeat"))
		rejected.Store(false)
		publisher.Heartbeat(client.Heartbeat(ctx, "status-node", nil))
		Expect(classify()).To(Equal("ready"))
		stop()
		Eventually(func() bool { return bus.IsConnected() }, 5*time.Second).Should(BeFalse())
		publisher.Update(nil)
		Expect(classify()).To(Equal("nats"))
		start()
		Eventually(func() bool { return bus.IsConnected() }, 10*time.Second).Should(BeTrue())
		publisher.Update(nil)
		publisher.Heartbeat(client.Heartbeat(ctx, "status-node", nil))
		Expect(classify()).To(Equal("ready"))
		// A real permission denial must not be promoted by a successful local probe.
		_, err = bus.Subscribe("nodes.other-node.backend.install", func([]byte) {})
		Expect(err).To(MatchError(ContainSubstring("denied by NATS server")))
		Expect(errors.Is(err, nats.ErrPermissionViolation)).To(BeTrue())
		Expect(bus.SubscriptionsReady()).To(BeFalse())
		stop()
		start()
		Eventually(bus.SubscriptionsReady, 10*time.Second).Should(BeTrue())
		data, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).NotTo(ContainSubstring(token))
		Expect(string(data)).NotTo(ContainSubstring(userSeed))
		Expect(string(data)).NotTo(ContainSubstring("registration-secret"))
		Expect(os.Chmod(path, 0644)).To(Succeed())
		publisher.Update(nil)
		Eventually(ctx.Done()).Should(BeClosed())
		Eventually(refreshed).Should(Receive(BeNil()))
		_, err = os.Stat(path)
		Expect(os.IsNotExist(err)).To(BeTrue())
	})
})

var _ = Describe("Worker status production approval observer", func() {
	It("observes static credential revocation without registration or credential mutation", func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		path, generation, err := workerstatus.NewSession(GinkgoT().TempDir())
		Expect(err).NotTo(HaveOccurred())
		publisher, err := workerstatus.NewPublisher(ctx, path, generation, cancel)
		Expect(err).NotTo(HaveOccurred())
		publisher.Registration("healthy", true, nil)
		var revoked atomic.Bool
		var writes atomic.Int64
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				writes.Add(1)
				w.WriteHeader(405)
				return
			}
			state := "healthy"
			if revoked.Load() {
				state = "pending"
			}
			_ = json.NewEncoder(w).Encode(workerregistry.RegisterResponse{ID: "static", Status: state})
		}))
		defer server.Close()
		client := &workerregistry.RegistrationClient{FrontendURL: server.URL}
		done := make(chan struct{})
		go func() { defer close(done); observeApproval(ctx, publisher, client, "static", 10*time.Millisecond) }()
		revoked.Store(true)
		Eventually(func() string {
			s, e := workerstatus.Read(path, generation)
			if e != nil {
				return ""
			}
			return s.Registration
		}).Should(Equal("pending"))
		publisher.Heartbeat(nil)
		s, err := workerstatus.Read(path, generation)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Registration).To(Equal("pending"))
		Expect(s.Credentials).To(BeTrue())
		Expect(writes.Load()).To(BeZero())
		cancel()
		Eventually(done).Should(BeClosed())
	})
})
