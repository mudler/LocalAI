package distributed_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/services/agents"
	clustersvc "github.com/mudler/LocalAI/core/services/cluster"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/pkg/httpclient"
	"github.com/mudler/LocalAI/tests/e2e/distributed/cluster"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const machineAuthTimeout = 30 * time.Second

type machineRegistration struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	APIToken    string `json:"api_token"`
	TunnelToken string `json:"tunnel_token"`
}

type machineTrafficObserver struct {
	server *httptest.Server
	mu     sync.Mutex

	registrations  int
	heartbeats     map[string]int
	inferenceToken string
	inferenceCode  int
}

func newMachineTrafficObserver(target string) *machineTrafficObserver {
	targetURL, err := url.Parse(target)
	Expect(err).ToNot(HaveOccurred())
	observer := &machineTrafficObserver{heartbeats: map[string]int{}}
	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	observer.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observer.mu.Lock()
		switch {
		case r.URL.Path == "/api/node/register":
			observer.registrations++
		case strings.HasPrefix(r.URL.Path, "/api/node/") && strings.HasSuffix(r.URL.Path, "/heartbeat"):
			nodeID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/node/"), "/heartbeat")
			observer.heartbeats[nodeID]++
		}
		observer.mu.Unlock()

		if r.URL.Path != "/v1/chat/completions" {
			proxy.ServeHTTP(w, r)
			return
		}

		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		recorder := &statusCapturingWriter{ResponseWriter: w, status: http.StatusOK}
		proxy.ServeHTTP(recorder, r)
		observer.mu.Lock()
		observer.inferenceToken = token
		observer.inferenceCode = recorder.status
		observer.mu.Unlock()
	}))
	return observer
}

type statusCapturingWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusCapturingWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusCapturingWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (o *machineTrafficObserver) registrationCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.registrations
}

func (o *machineTrafficObserver) heartbeatCount(nodeID string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.heartbeats[nodeID]
}

func (o *machineTrafficObserver) inferenceCredential() (string, int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.inferenceToken, o.inferenceCode
}

func registerMachine(baseURL, headerToken, bodyToken, name, nodeType string) (int, machineRegistration, string) {
	GinkgoHelper()
	body, err := json.Marshal(map[string]any{
		"name":      name,
		"node_type": nodeType,
		"token":     bodyToken,
	})
	Expect(err).ToNot(HaveOccurred())
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/node/register", bytes.NewReader(body))
	Expect(err).ToNot(HaveOccurred())
	req.Header.Set("Content-Type", "application/json")
	if headerToken != "" {
		req.Header.Set("Authorization", "Bearer "+headerToken)
	}
	resp, err := httpclient.NewWithTimeout(machineAuthTimeout).Do(req)
	Expect(err).ToNot(HaveOccurred())
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	Expect(err).ToNot(HaveOccurred())
	var registration machineRegistration
	if resp.StatusCode == http.StatusCreated {
		Expect(json.Unmarshal(raw, &registration)).To(Succeed())
	}
	return resp.StatusCode, registration, string(raw)
}

func tunnelDial(baseURL, nodeID, token string, cookies []*http.Cookie) (*websocket.Conn, int, string) {
	GinkgoHelper()
	endpoint := "ws" + strings.TrimPrefix(baseURL, "http") + clustersvc.ConnectPath + "?id=" + url.QueryEscape(nodeID)
	header := http.Header{}
	if token != "" {
		header.Set("Authorization", "Bearer "+token)
	}
	for _, cookie := range cookies {
		header.Add("Cookie", cookie.String())
	}
	dialer := websocket.Dialer{HandshakeTimeout: machineAuthTimeout}
	conn, resp, err := dialer.Dial(endpoint, header)
	if err == nil {
		return conn, http.StatusSwitchingProtocols, ""
	}
	if resp == nil {
		return nil, 0, err.Error()
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return nil, resp.StatusCode, string(raw)
}

func requestWithBearer(method, endpoint, token string) int {
	GinkgoHelper()
	req, err := http.NewRequest(method, endpoint, nil)
	Expect(err).ToNot(HaveOccurred())
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := httpclient.NewWithTimeout(machineAuthTimeout).Do(req)
	Expect(err).ToNot(HaveOccurred())
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

var _ = Describe("Authenticated distributed binaries", Label("Distributed"), Label("Cluster"), func() {
	It("keeps browser, registration, tunnel, and agent credentials in their own trust domains", func() {
		const (
			registrationToken = "distributed-machine-secret"
			adminEmail        = "auth-admin@e2e.local"
		)
		priorAPIToken, hadAPIToken := os.LookupEnv("LOCALAI_API_TOKEN")
		Expect(os.Setenv("LOCALAI_API_TOKEN", "ambient-credential-must-not-reach-workers")).To(Succeed())
		DeferCleanup(func() {
			if hadAPIToken {
				_ = os.Setenv("LOCALAI_API_TOKEN", priorAPIToken)
			} else {
				_ = os.Unsetenv("LOCALAI_API_TOKEN")
			}
		})

		var traffic *machineTrafficObserver
		c, dsn := startClusterOnFreshDB(1, 1, func(o *cluster.Options) {
			o.RegistrationToken = registrationToken
			o.AdminEmail = adminEmail
			o.RequireNodeApproval = true
			o.DistributedRequireAuth = true
			o.AgentWorkers = 1
			withMockModel("agent-auth-model")(o)
			o.WorkerFrontendURL = func(_ int, _ string, frontends []string) string {
				if traffic == nil {
					traffic = newMachineTrafficObserver(frontends[0])
				}
				return traffic.server.URL
			}
		})
		DeferCleanup(traffic.server.Close)
		baseURL := c.FrontendURL(0)

		// The browser signs in with the ordinary WebUI flow. Its cookie opens
		// admin APIs and the SPA remains available while machine auth is enabled.
		browser, err := c.AdminSession(0)
		Expect(err).ToNot(HaveOccurred())
		spa, err := browser.Get(baseURL + "/app")
		Expect(err).ToNot(HaveOccurred())
		Expect(spa.StatusCode).To(Equal(http.StatusOK))
		spaBody, err := io.ReadAll(spa.Body)
		Expect(err).ToNot(HaveOccurred())
		_ = spa.Body.Close()
		Expect(strings.ToLower(string(spaBody))).To(ContainSubstring("<html"))
		var initialRoster []node
		Expect(c.GetJSON(browser, 0, "/api/nodes", &initialRoster)).To(Succeed())
		anonymousNodes, err := httpclient.NewWithTimeout(machineAuthTimeout).Get(baseURL + "/api/nodes")
		Expect(err).ToNot(HaveOccurred())
		Expect(anonymousNodes.StatusCode).To(Equal(http.StatusUnauthorized))
		_ = anonymousNodes.Body.Close()

		// The global WebUI middleware delegates the node namespace to its own
		// machine gate. These are the route handler's errors, not the WebUI auth
		// envelope, and a correct machine credential needs no browser cookie.
		status, _, responseBody := registerMachine(baseURL, "", "", "missing-token", nodes.NodeTypeBackend)
		Expect(status).To(Equal(http.StatusUnauthorized))
		Expect(responseBody).To(ContainSubstring("missing or invalid Authorization header"))
		status, _, responseBody = registerMachine(baseURL, "wrong", "wrong", "wrong-token", nodes.NodeTypeBackend)
		Expect(status).To(Equal(http.StatusUnauthorized))
		Expect(responseBody).To(ContainSubstring("invalid registration token"))

		status, backendProbe, responseBody := registerMachine(baseURL, registrationToken, registrationToken, "auth-backend-probe", nodes.NodeTypeBackend)
		Expect(status).To(Equal(http.StatusCreated), responseBody)
		Expect(backendProbe.Status).To(Equal(nodes.StatusPending))
		Expect(backendProbe.TunnelToken).ToNot(BeEmpty())
		Expect(backendProbe.APIToken).To(BeEmpty(), "a backend worker must never receive a user API key")

		status, agentProbe, responseBody := registerMachine(baseURL, registrationToken, registrationToken, "auth-agent-probe", nodes.NodeTypeAgent)
		Expect(status).To(Equal(http.StatusCreated), responseBody)
		Expect(agentProbe.Status).To(Equal(nodes.StatusPending))
		Expect(agentProbe.TunnelToken).ToNot(BeEmpty())
		Expect(agentProbe.TunnelToken).ToNot(Equal(backendProbe.TunnelToken), "registration must mint a unique tunnel credential per node")
		Expect(agentProbe.APIToken).To(BeEmpty(), "a pending agent must not receive an inference credential")

		// A WebUI session, the deployment registration secret, a missing token,
		// and a random token are all invalid tunnel credentials. The valid node
		// token authenticates the pending node but is forbidden until approval.
		browserURL, err := url.Parse(baseURL)
		Expect(err).ToNot(HaveOccurred())
		_, code, _ := tunnelDial(baseURL, backendProbe.ID, "", browser.Jar.Cookies(browserURL))
		Expect(code).To(Equal(http.StatusUnauthorized), "a browser session must not authenticate a worker tunnel")
		_, code, _ = tunnelDial(baseURL, backendProbe.ID, registrationToken, nil)
		Expect(code).To(Equal(http.StatusUnauthorized), "the shared registration token must not impersonate a node")
		_, code, _ = tunnelDial(baseURL, backendProbe.ID, "wrong-tunnel-token", nil)
		Expect(code).To(Equal(http.StatusUnauthorized))
		_, code, responseBody = tunnelDial(baseURL, backendProbe.ID, backendProbe.TunnelToken, nil)
		Expect(code).To(Equal(http.StatusForbidden), responseBody)

		approval := machineRegistration{}
		status, err = c.PostJSON(browser, 0, "/api/nodes/"+backendProbe.ID+"/approve", map[string]any{}, &approval)
		Expect(err).ToNot(HaveOccurred())
		Expect(status).To(Equal(http.StatusOK))
		conn, code, responseBody := tunnelDial(baseURL, backendProbe.ID, backendProbe.TunnelToken, nil)
		Expect(code).To(Equal(http.StatusSwitchingProtocols), responseBody)
		Expect(conn).ToNot(BeNil())
		Expect(conn.Close()).To(Succeed())

		// Approval is the first point at which an agent is issued a user key.
		// Re-registration is the machine-facing response that hands it to the
		// worker. The key can call inference APIs, but cannot cross the admin gate.
		status, err = c.PostJSON(browser, 0, "/api/nodes/"+agentProbe.ID+"/approve", map[string]any{}, &approval)
		Expect(err).ToNot(HaveOccurred())
		Expect(status).To(Equal(http.StatusOK))
		status, approvedAgent, responseBody := registerMachine(baseURL, registrationToken, registrationToken, "auth-agent-probe", nodes.NodeTypeAgent)
		Expect(status).To(Equal(http.StatusCreated), responseBody)
		Expect(approvedAgent.Status).To(Equal(nodes.StatusHealthy))
		Expect(approvedAgent.APIToken).ToNot(BeEmpty())
		Expect(requestWithBearer(http.MethodGet, baseURL+"/v1/models", approvedAgent.APIToken)).To(Equal(http.StatusOK))
		Expect(requestWithBearer(http.MethodGet, baseURL+"/api/nodes", approvedAgent.APIToken)).To(Equal(http.StatusForbidden))

		// The two actual worker binaries registered with the same machine secret.
		// They begin pending, continue heartbeating, and acquire their tunnels
		// only after the browser administrator approves them.
		probe := newRosterProbe(c, browser, 0)
		Eventually(func() string { return probe.statusOf(c.WorkerName(0)) }, nodeRosterTimeout, nodeRosterPoll).
			Should(Equal(nodes.StatusPending), probe.describe)
		backendID := probe.idOf(c.WorkerName(0))
		Expect(backendID).ToNot(BeEmpty())
		firstHeartbeat := probe.heartbeatOf(c.WorkerName(0))
		Eventually(func() time.Time { return probe.heartbeatOf(c.WorkerName(0)) }, "30s", "1s").
			Should(BeTemporally(">", firstHeartbeat), "the real backend worker did not send an authenticated heartbeat")

		status, err = c.PostJSON(browser, 0, "/api/nodes/"+backendID+"/approve", map[string]any{}, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(status).To(Equal(http.StatusOK))

		Eventually(func() string { return probe.statusOf(c.AgentWorkerName(0)) }, nodeRosterTimeout, nodeRosterPoll).
			Should(Equal(nodes.StatusPending), probe.describe)
		agentID := probe.idOf(c.AgentWorkerName(0))
		Expect(agentID).ToNot(BeEmpty())
		Eventually(func() int { return traffic.heartbeatCount(agentID) }, "15s", "250ms").
			Should(BeNumerically(">", 0), "the pending real agent worker never called /heartbeat")
		Expect(traffic.registrationCount()).To(BeNumerically(">", 0),
			"the heartbeat counter is path-specific and must not be populated by registration retries")
		db := openClusterDB(dsn)
		owners := newTunnelOwners(db)
		Consistently(func() string { return owners.ownerOf(agentID) }, "2s", "200ms").
			Should(BeEmpty(), "a pending agent worker opened its tunnel before approval")
		status, err = c.PostJSON(browser, 0, "/api/nodes/"+agentID+"/approve", map[string]any{}, nil)
		Expect(err).ToNot(HaveOccurred())
		Expect(status).To(Equal(http.StatusOK))

		Eventually(func() string { return owners.ownerOf(backendID) }, nodeRosterTimeout, nodeRosterPoll).
			ShouldNot(BeEmpty(), owners.describe)
		Eventually(func() string { return owners.ownerOf(agentID) }, nodeRosterTimeout, nodeRosterPoll).
			ShouldNot(BeEmpty(), owners.describe)

		var backendNode nodes.BackendNode
		Expect(db.First(&backendNode, "id = ?", backendID).Error).To(Succeed())
		Expect(backendNode.AuthUserID).To(BeEmpty())
		Expect(backendNode.APIKeyID).To(BeEmpty())

		var agentNode nodes.BackendNode
		Eventually(func() string {
			if err := db.First(&agentNode, "id = ?", agentID).Error; err != nil {
				return ""
			}
			return agentNode.APIKeyID
		}, nodeRosterTimeout, nodeRosterPoll).ShouldNot(BeEmpty(), "the real approved agent worker was not provisioned an API key")
		var agentUser auth.User
		Expect(db.First(&agentUser, "id = ?", agentNode.AuthUserID).Error).To(Succeed())
		Expect(agentUser.Provider).To(Equal(auth.ProviderAgentWorker))
		Expect(agentUser.Subject).To(Equal(agentID))
		Expect(agentUser.Role).To(Equal(auth.RoleUser))
		var agentKey auth.UserAPIKey
		Expect(db.First(&agentKey, "id = ?", agentNode.APIKeyID).Error).To(Succeed())
		Expect(agentKey.UserID).To(Equal(agentUser.ID))
		Expect(agentKey.Role).To(Equal(auth.RoleUser))
		var permissions auth.UserPermission
		Expect(db.First(&permissions, "user_id = ?", agentUser.ID).Error).To(Succeed())
		Expect(permissions.Permissions).To(Equal(auth.PermissionMap{auth.FeatureCollections: true}), fmt.Sprintf("unexpected agent-worker scope: %#v", permissions.Permissions))

		// Seed a config with no per-agent API key. The public chat endpoint then
		// dispatches it to the compiled agent-worker, whose only possible
		// inference credential is the plaintext token returned to that process
		// by its approved re-registration.
		var adminUser auth.User
		Expect(db.First(&adminUser, "email = ?", adminEmail).Error).To(Succeed())
		agentCfg := agents.AgentConfig{Name: "credential-probe", Model: "agent-auth-model", MaxIterations: 1}
		agentCfgJSON, err := json.Marshal(agentCfg)
		Expect(err).ToNot(HaveOccurred())
		agentStore, err := agents.NewAgentStore(db)
		Expect(err).ToNot(HaveOccurred())
		Expect(agentStore.SaveConfig(&agents.AgentConfigRecord{
			UserID: adminUser.ID, Name: agentCfg.Name, ConfigJSON: string(agentCfgJSON), Status: agents.StatusActive,
		})).To(Succeed())

		var chatReply map[string]any
		Eventually(func() int {
			code, postErr := c.PostJSON(browser, 0, "/api/agents/credential-probe/chat", map[string]any{"message": "prove the worker credential"}, &chatReply)
			if postErr != nil {
				return 0
			}
			return code
		}, nodeRosterTimeout, nodeRosterPoll).Should(Equal(http.StatusAccepted))
		Expect(chatReply["message_id"]).ToNot(BeEmpty())

		var deliveredToken string
		Eventually(func() int {
			deliveredToken, status = traffic.inferenceCredential()
			return status
		}, tunnelInferenceTimeout, "500ms").Should(Equal(http.StatusOK),
			"the real agent-worker did not complete an authenticated inference request")
		Expect(deliveredToken).ToNot(BeEmpty(), "dropping RegisterResponse.APIToken must fail this assertion")
		Expect(deliveredToken).ToNot(Equal("ambient-credential-must-not-reach-workers"))
		Expect(auth.HashAPIKey(deliveredToken, "e2e-cluster-hmac-secret")).To(Equal(agentKey.KeyHash),
			"the bearer used for inference was not the credential provisioned for this real agent-worker")
		Expect(requestWithBearer(http.MethodGet, baseURL+"/api/nodes", deliveredToken)).To(Equal(http.StatusForbidden),
			"the exact credential used by the real worker for inference crossed the admin gate")

		// Fail-closed is a property of the live process configuration, not an
		// assumption made from the token having happened to be non-empty.
		for _, proc := range []struct {
			kind cluster.ProcKind
			name string
		}{
			{cluster.ProcFrontend, "frontend"},
			{cluster.ProcWorker, "backend worker"},
			{cluster.ProcAgentWorker, "agent worker"},
		} {
			env, err := c.ProcessEnviron(proc.kind, 0)
			Expect(err).ToNot(HaveOccurred())
			Expect(env).To(ContainElement("LOCALAI_DISTRIBUTED_REQUIRE_AUTH=true"), proc.name)
			Expect(env).ToNot(ContainElement(HavePrefix("LOCALAI_API_TOKEN=")), proc.name)
		}
	})
})
