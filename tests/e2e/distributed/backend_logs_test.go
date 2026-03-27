package distributed_test

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/core/services/nodes"
	"github.com/mudler/LocalAI/pkg/model"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pgdriver "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var _ = Describe("Distributed Backend Log Streaming", Label("Distributed"), func() {

	Context("Worker HTTP log endpoints", func() {
		var (
			logStore   *model.BackendLogStore
			serverAddr string
			cleanup    func()
			token      string
		)

		BeforeEach(func() {
			token = "test-secret-token"
			logStore = model.NewBackendLogStore(1000)

			// Populate test log lines
			logStore.AppendLine("model-a", "stdout", "loading model...")
			logStore.AppendLine("model-a", "stderr", "warning: something")
			logStore.AppendLine("model-a", "stdout", "model loaded successfully")
			logStore.AppendLine("model-b", "stdout", "hello from model-b")

			var err error
			serverAddr, cleanup, err = startTestFileTransferServerWithLogs(token, logStore)
			Expect(err).ToNot(HaveOccurred())
		})

		AfterEach(func() {
			if cleanup != nil {
				cleanup()
			}
		})

		It("should list models with logs via GET /v1/backend-logs", func() {
			req, err := http.NewRequest("GET", fmt.Sprintf("http://%s/v1/backend-logs", serverAddr), nil)
			Expect(err).ToNot(HaveOccurred())
			req.Header.Set("Authorization", "Bearer "+token)

			resp, err := http.DefaultClient.Do(req)
			Expect(err).ToNot(HaveOccurred())
			defer resp.Body.Close()

			Expect(resp.StatusCode).To(Equal(http.StatusOK))

			var models []string
			Expect(json.NewDecoder(resp.Body).Decode(&models)).To(Succeed())
			Expect(models).To(ContainElement("model-a"))
			Expect(models).To(ContainElement("model-b"))
		})

		It("should return log lines for a model via GET /v1/backend-logs/{modelId}", func() {
			req, err := http.NewRequest("GET", fmt.Sprintf("http://%s/v1/backend-logs/model-a", serverAddr), nil)
			Expect(err).ToNot(HaveOccurred())
			req.Header.Set("Authorization", "Bearer "+token)

			resp, err := http.DefaultClient.Do(req)
			Expect(err).ToNot(HaveOccurred())
			defer resp.Body.Close()

			Expect(resp.StatusCode).To(Equal(http.StatusOK))

			var lines []model.BackendLogLine
			Expect(json.NewDecoder(resp.Body).Decode(&lines)).To(Succeed())
			Expect(lines).To(HaveLen(3))
			Expect(lines[0].Stream).To(Equal("stdout"))
			Expect(lines[0].Text).To(Equal("loading model..."))
			Expect(lines[1].Stream).To(Equal("stderr"))
			Expect(lines[1].Text).To(Equal("warning: something"))
			Expect(lines[2].Stream).To(Equal("stdout"))
			Expect(lines[2].Text).To(Equal("model loaded successfully"))
		})

		It("should return empty array for unknown model", func() {
			req, err := http.NewRequest("GET", fmt.Sprintf("http://%s/v1/backend-logs/nonexistent", serverAddr), nil)
			Expect(err).ToNot(HaveOccurred())
			req.Header.Set("Authorization", "Bearer "+token)

			resp, err := http.DefaultClient.Do(req)
			Expect(err).ToNot(HaveOccurred())
			defer resp.Body.Close()

			Expect(resp.StatusCode).To(Equal(http.StatusOK))

			var lines []model.BackendLogLine
			Expect(json.NewDecoder(resp.Body).Decode(&lines)).To(Succeed())
			Expect(lines).To(BeEmpty())
		})

		It("should reject requests without bearer token", func() {
			req, err := http.NewRequest("GET", fmt.Sprintf("http://%s/v1/backend-logs", serverAddr), nil)
			Expect(err).ToNot(HaveOccurred())
			// No Authorization header

			resp, err := http.DefaultClient.Do(req)
			Expect(err).ToNot(HaveOccurred())
			defer resp.Body.Close()

			Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized))
		})

		It("should reject requests with wrong bearer token", func() {
			req, err := http.NewRequest("GET", fmt.Sprintf("http://%s/v1/backend-logs", serverAddr), nil)
			Expect(err).ToNot(HaveOccurred())
			req.Header.Set("Authorization", "Bearer wrong-token")

			resp, err := http.DefaultClient.Do(req)
			Expect(err).ToNot(HaveOccurred())
			defer resp.Body.Close()

			Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized))
		})

		It("should handle URL-encoded model IDs", func() {
			// Add a model with special characters in the name
			logStore.AppendLine("my-org/model:latest", "stdout", "special model log")

			encodedModelID := url.PathEscape("my-org/model:latest")
			req, err := http.NewRequest("GET", fmt.Sprintf("http://%s/v1/backend-logs/%s", serverAddr, encodedModelID), nil)
			Expect(err).ToNot(HaveOccurred())
			req.Header.Set("Authorization", "Bearer "+token)

			resp, err := http.DefaultClient.Do(req)
			Expect(err).ToNot(HaveOccurred())
			defer resp.Body.Close()

			Expect(resp.StatusCode).To(Equal(http.StatusOK))

			var lines []model.BackendLogLine
			Expect(json.NewDecoder(resp.Body).Decode(&lines)).To(Succeed())
			Expect(lines).To(HaveLen(1))
			Expect(lines[0].Text).To(Equal("special model log"))
		})
	})

	Context("Worker WebSocket log streaming", func() {
		var (
			logStore   *model.BackendLogStore
			serverAddr string
			cleanup    func()
			token      string
		)

		BeforeEach(func() {
			token = "test-ws-token"
			logStore = model.NewBackendLogStore(1000)

			// Pre-populate some lines
			logStore.AppendLine("ws-model", "stdout", "line-1")
			logStore.AppendLine("ws-model", "stderr", "line-2")

			var err error
			serverAddr, cleanup, err = startTestFileTransferServerWithLogs(token, logStore)
			Expect(err).ToNot(HaveOccurred())
		})

		AfterEach(func() {
			if cleanup != nil {
				cleanup()
			}
		})

		It("should stream initial lines and new lines via WebSocket", func() {
			wsURL := fmt.Sprintf("ws://%s/v1/backend-logs/ws-model/ws", serverAddr)
			dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
			headers := http.Header{}
			headers.Set("Authorization", "Bearer "+token)

			conn, resp, err := dialer.Dial(wsURL, headers)
			Expect(err).ToNot(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusSwitchingProtocols))
			defer conn.Close()

			// Read initial message
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			var initialMsg map[string]json.RawMessage
			err = conn.ReadJSON(&initialMsg)
			Expect(err).ToNot(HaveOccurred())

			var msgType string
			Expect(json.Unmarshal(initialMsg["type"], &msgType)).To(Succeed())
			Expect(msgType).To(Equal("initial"))

			var initialLines []model.BackendLogLine
			Expect(json.Unmarshal(initialMsg["lines"], &initialLines)).To(Succeed())
			Expect(initialLines).To(HaveLen(2))
			Expect(initialLines[0].Text).To(Equal("line-1"))
			Expect(initialLines[1].Text).To(Equal("line-2"))

			// Now append a new line and verify it arrives via WebSocket
			logStore.AppendLine("ws-model", "stdout", "line-3-realtime")

			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			var lineMsg map[string]json.RawMessage
			err = conn.ReadJSON(&lineMsg)
			Expect(err).ToNot(HaveOccurred())

			Expect(json.Unmarshal(lineMsg["type"], &msgType)).To(Succeed())
			Expect(msgType).To(Equal("line"))

			var streamedLine model.BackendLogLine
			Expect(json.Unmarshal(lineMsg["line"], &streamedLine)).To(Succeed())
			Expect(streamedLine.Text).To(Equal("line-3-realtime"))
			Expect(streamedLine.Stream).To(Equal("stdout"))
		})

		It("should reject WebSocket connection without token", func() {
			wsURL := fmt.Sprintf("ws://%s/v1/backend-logs/ws-model/ws", serverAddr)
			dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}

			_, resp, err := dialer.Dial(wsURL, nil)
			Expect(err).To(HaveOccurred())
			if resp != nil {
				Expect(resp.StatusCode).To(Equal(http.StatusUnauthorized))
			}
		})

		It("should handle client disconnect gracefully", func() {
			wsURL := fmt.Sprintf("ws://%s/v1/backend-logs/ws-model/ws", serverAddr)
			dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
			headers := http.Header{}
			headers.Set("Authorization", "Bearer "+token)

			conn, _, err := dialer.Dial(wsURL, headers)
			Expect(err).ToNot(HaveOccurred())

			// Read initial message
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			var initialMsg map[string]json.RawMessage
			Expect(conn.ReadJSON(&initialMsg)).To(Succeed())

			// Close connection abruptly
			conn.Close()

			// Append more lines — should not panic
			logStore.AppendLine("ws-model", "stdout", "after-disconnect")
			time.Sleep(100 * time.Millisecond)
			// If we got here without panic, the test passes
		})

		It("should stream lines only for the requested model", func() {
			logStore.AppendLine("other-model", "stdout", "other model log")

			wsURL := fmt.Sprintf("ws://%s/v1/backend-logs/ws-model/ws", serverAddr)
			dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
			headers := http.Header{}
			headers.Set("Authorization", "Bearer "+token)

			conn, _, err := dialer.Dial(wsURL, headers)
			Expect(err).ToNot(HaveOccurred())
			defer conn.Close()

			// Read initial message
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			var initialMsg map[string]json.RawMessage
			Expect(conn.ReadJSON(&initialMsg)).To(Succeed())

			// Append line to a different model
			logStore.AppendLine("other-model", "stdout", "should not appear")
			// Append line to our model
			logStore.AppendLine("ws-model", "stdout", "should appear")

			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			var lineMsg map[string]json.RawMessage
			Expect(conn.ReadJSON(&lineMsg)).To(Succeed())

			var streamedLine model.BackendLogLine
			Expect(json.Unmarshal(lineMsg["line"], &streamedLine)).To(Succeed())
			Expect(streamedLine.Text).To(Equal("should appear"))
		})
	})

	Context("Frontend proxy REST endpoints", func() {
		var (
			pgInfra     *TestInfra
			db          *gorm.DB
			registry    *nodes.NodeRegistry
			logStore    *model.BackendLogStore
			workerAddr  string
			workerClean func()
			token       string
		)

		BeforeEach(func() {
			pgInfra = SetupInfra("localai_backend_logs_test")

			var err error
			db, err = gorm.Open(pgdriver.Open(pgInfra.PGURL), &gorm.Config{
				Logger: logger.Default.LogMode(logger.Silent),
			})
			Expect(err).ToNot(HaveOccurred())

			registry, err = nodes.NewNodeRegistry(db)
			Expect(err).ToNot(HaveOccurred())

			token = "proxy-test-token"
			logStore = model.NewBackendLogStore(1000)
			logStore.AppendLine("remote-model", "stdout", "remote log line 1")
			logStore.AppendLine("remote-model", "stderr", "remote log line 2")

			workerAddr, workerClean, err = startTestFileTransferServerWithLogs(token, logStore)
			Expect(err).ToNot(HaveOccurred())
		})

		AfterEach(func() {
			if workerClean != nil {
				workerClean()
			}
		})

		It("should proxy backend-logs list from worker via node ID", func() {
			// Register a node with HTTPAddress pointing to our test worker server
			node := &nodes.BackendNode{
				Name:        "log-test-node",
				Address:     "127.0.0.1:50051", // gRPC address (unused here)
				HTTPAddress: workerAddr,
			}
			Expect(registry.Register(node, true)).To(Succeed())

			// Create an Echo test server with the proxy endpoint
			e := echo.New()
			e.GET("/api/nodes/:id/backend-logs", localai.NodeBackendLogsListEndpoint(registry, token))

			req := httptest.NewRequest("GET", fmt.Sprintf("/api/nodes/%s/backend-logs", node.ID), nil)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusOK))

			var models []string
			Expect(json.NewDecoder(rec.Body).Decode(&models)).To(Succeed())
			Expect(models).To(ContainElement("remote-model"))
		})

		It("should proxy backend-logs lines from worker via node ID", func() {
			node := &nodes.BackendNode{
				Name:        "log-lines-node",
				Address:     "127.0.0.1:50051",
				HTTPAddress: workerAddr,
			}
			Expect(registry.Register(node, true)).To(Succeed())

			e := echo.New()
			e.GET("/api/nodes/:id/backend-logs/:modelId", localai.NodeBackendLogsLinesEndpoint(registry, token))

			req := httptest.NewRequest("GET", fmt.Sprintf("/api/nodes/%s/backend-logs/remote-model", node.ID), nil)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusOK))

			var lines []model.BackendLogLine
			Expect(json.NewDecoder(rec.Body).Decode(&lines)).To(Succeed())
			Expect(lines).To(HaveLen(2))
			Expect(lines[0].Text).To(Equal("remote log line 1"))
			Expect(lines[1].Text).To(Equal("remote log line 2"))
		})

		It("should return 404 for unknown node ID", func() {
			e := echo.New()
			e.GET("/api/nodes/:id/backend-logs", localai.NodeBackendLogsListEndpoint(registry, token))

			req := httptest.NewRequest("GET", "/api/nodes/nonexistent-id/backend-logs", nil)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusNotFound))
		})
	})

	Context("Frontend WebSocket proxy (end-to-end)", func() {
		var (
			wsInfra     *TestInfra
			db          *gorm.DB
			registry    *nodes.NodeRegistry
			logStore    *model.BackendLogStore
			workerAddr  string
			workerClean func()
			token       string
			echoServer  *http.Server
			echoAddr    string
		)

		BeforeEach(func() {
			wsInfra = SetupInfra("localai_ws_proxy_test")

			var err error
			db, err = gorm.Open(pgdriver.Open(wsInfra.PGURL), &gorm.Config{
				Logger: logger.Default.LogMode(logger.Silent),
			})
			Expect(err).ToNot(HaveOccurred())

			registry, err = nodes.NewNodeRegistry(db)
			Expect(err).ToNot(HaveOccurred())

			token = "ws-proxy-token"
			logStore = model.NewBackendLogStore(1000)
			logStore.AppendLine("proxy-model", "stdout", "initial line from worker")

			workerAddr, workerClean, err = startTestFileTransferServerWithLogs(token, logStore)
			Expect(err).ToNot(HaveOccurred())

			// Start Echo server with the WebSocket proxy route
			e := echo.New()
			e.GET("/ws/nodes/:id/backend-logs/:modelId", localai.NodeBackendLogsWSEndpoint(registry, token))

			lis, err := net.Listen("tcp", "127.0.0.1:0")
			Expect(err).ToNot(HaveOccurred())
			echoAddr = lis.Addr().String()
			echoServer = &http.Server{Handler: e}
			go echoServer.Serve(lis)
		})

		AfterEach(func() {
			if echoServer != nil {
				echoServer.Close()
			}
			if workerClean != nil {
				workerClean()
			}
		})

		It("should proxy WebSocket log stream from worker through frontend", func() {
			// Register node
			node := &nodes.BackendNode{
				Name:        "ws-proxy-node",
				Address:     "127.0.0.1:50051",
				HTTPAddress: workerAddr,
			}
			Expect(registry.Register(node, true)).To(Succeed())

			// Connect WebSocket to the frontend proxy
			wsURL := fmt.Sprintf("ws://%s/ws/nodes/%s/backend-logs/proxy-model", echoAddr, node.ID)
			dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
			conn, _, err := dialer.Dial(wsURL, nil)
			Expect(err).ToNot(HaveOccurred())
			defer conn.Close()

			// Read initial message (proxied from worker)
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			var initialMsg map[string]json.RawMessage
			Expect(conn.ReadJSON(&initialMsg)).To(Succeed())

			var msgType string
			Expect(json.Unmarshal(initialMsg["type"], &msgType)).To(Succeed())
			Expect(msgType).To(Equal("initial"))

			var initialLines []model.BackendLogLine
			Expect(json.Unmarshal(initialMsg["lines"], &initialLines)).To(Succeed())
			Expect(initialLines).To(HaveLen(1))
			Expect(initialLines[0].Text).To(Equal("initial line from worker"))

			// Append a new line on the worker's log store
			logStore.AppendLine("proxy-model", "stderr", "realtime via proxy")

			// Read the streamed line through the proxy
			conn.SetReadDeadline(time.Now().Add(5 * time.Second))
			var lineMsg map[string]json.RawMessage
			Expect(conn.ReadJSON(&lineMsg)).To(Succeed())

			Expect(json.Unmarshal(lineMsg["type"], &msgType)).To(Succeed())
			Expect(msgType).To(Equal("line"))

			var streamedLine model.BackendLogLine
			Expect(json.Unmarshal(lineMsg["line"], &streamedLine)).To(Succeed())
			Expect(streamedLine.Text).To(Equal("realtime via proxy"))
			Expect(streamedLine.Stream).To(Equal("stderr"))
		})

		It("should return error for unknown node in WebSocket proxy", func() {
			wsURL := fmt.Sprintf("ws://%s/ws/nodes/nonexistent-node/backend-logs/some-model", echoAddr)
			dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}

			_, resp, err := dialer.Dial(wsURL, nil)
			Expect(err).To(HaveOccurred())
			if resp != nil {
				// Should get a non-101 status (404 or similar)
				Expect(resp.StatusCode).ToNot(Equal(http.StatusSwitchingProtocols))
			}
		})
	})
})

// startTestFileTransferServerWithLogs starts the real nodes.StartFileTransferServerWithListener
// with a BackendLogStore, using a temporary staging directory.
// Returns the server address, cleanup function, and error.
func startTestFileTransferServerWithLogs(token string, logStore *model.BackendLogStore) (string, func(), error) {
	stagingDir, err := os.MkdirTemp("", "logs-test-staging-*")
	if err != nil {
		return "", nil, err
	}

	// Listen on a free port and pass the listener directly to avoid TOCTOU race.
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		os.RemoveAll(stagingDir)
		return "", nil, err
	}
	addr := lis.Addr().String()

	server, err := nodes.StartFileTransferServerWithListener(lis, stagingDir, stagingDir, stagingDir, token, logStore)
	if err != nil {
		os.RemoveAll(stagingDir)
		return "", nil, err
	}

	cleanup := func() {
		nodes.ShutdownFileTransferServer(server)
		os.RemoveAll(stagingDir)
	}

	return addr, cleanup, nil
}

