package localai_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	. "github.com/mudler/LocalAI/core/http/endpoints/localai"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/pkg/system"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Backend Logs Endpoints", func() {
	var (
		app         *echo.Echo
		tempDir     string
		modelLoader *model.ModelLoader
	)

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "backend-logs-test-*")
		Expect(err).NotTo(HaveOccurred())

		modelsPath := filepath.Join(tempDir, "models")
		Expect(os.MkdirAll(modelsPath, 0750)).To(Succeed())

		systemState, err := system.GetSystemState(
			system.WithModelPath(modelsPath),
		)
		Expect(err).NotTo(HaveOccurred())

		modelLoader = model.NewModelLoader(systemState)

		app = echo.New()
		app.GET("/api/backend-logs", ListBackendLogsEndpoint(modelLoader))
		app.GET("/api/backend-logs/:modelId", GetBackendLogsEndpoint(modelLoader))
		app.POST("/api/backend-logs/:modelId/clear", ClearBackendLogsEndpoint(modelLoader))
		app.GET("/ws/backend-logs/:modelId", BackendLogsWebSocketEndpoint(modelLoader))
	})

	AfterEach(func() {
		os.RemoveAll(tempDir)
	})

	Context("REST endpoints", func() {
		It("should return empty list of models with logs", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/backend-logs", nil)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusOK))

			var models []string
			Expect(json.Unmarshal(rec.Body.Bytes(), &models)).To(Succeed())
			Expect(models).To(BeEmpty())
		})

		It("should list models that have logs", func() {
			modelLoader.BackendLogs().AppendLine("my-model", "stdout", "hello")

			req := httptest.NewRequest(http.MethodGet, "/api/backend-logs", nil)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusOK))

			var models []string
			Expect(json.Unmarshal(rec.Body.Bytes(), &models)).To(Succeed())
			Expect(models).To(ContainElement("my-model"))
		})

		It("should return log lines for a model", func() {
			modelLoader.BackendLogs().AppendLine("my-model", "stdout", "line one")
			modelLoader.BackendLogs().AppendLine("my-model", "stderr", "line two")

			req := httptest.NewRequest(http.MethodGet, "/api/backend-logs/my-model", nil)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusOK))

			var lines []model.BackendLogLine
			Expect(json.Unmarshal(rec.Body.Bytes(), &lines)).To(Succeed())
			Expect(lines).To(HaveLen(2))
			Expect(lines[0].Text).To(Equal("line one"))
			Expect(lines[0].Stream).To(Equal("stdout"))
			Expect(lines[1].Text).To(Equal("line two"))
			Expect(lines[1].Stream).To(Equal("stderr"))
		})

		It("should return empty log lines for unknown model", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/backend-logs/unknown-model", nil)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusOK))
		})

		It("should clear logs for a model", func() {
			modelLoader.BackendLogs().AppendLine("my-model", "stdout", "hello")

			req := httptest.NewRequest(http.MethodPost, "/api/backend-logs/my-model/clear", nil)
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusNoContent))

			// Verify logs are cleared
			req = httptest.NewRequest(http.MethodGet, "/api/backend-logs/my-model", nil)
			rec = httptest.NewRecorder()
			app.ServeHTTP(rec, req)

			var lines []model.BackendLogLine
			Expect(json.Unmarshal(rec.Body.Bytes(), &lines)).To(Succeed())
			Expect(lines).To(BeEmpty())
		})
	})

	Context("WebSocket endpoint", func() {
		It("should send initial lines and stream new lines", func() {
			// Seed some existing lines before connecting
			modelLoader.BackendLogs().AppendLine("ws-model", "stdout", "existing line")

			// Start a real HTTP server for WebSocket
			srv := httptest.NewServer(app)
			defer srv.Close()

			// Dial the WebSocket
			wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/backend-logs/ws-model"
			dialer := websocket.Dialer{HandshakeTimeout: 2 * time.Second}
			conn, _, err := dialer.Dial(wsURL, nil)
			Expect(err).NotTo(HaveOccurred())
			defer conn.Close()

			// Read the initial message
			var initialMsg map[string]any
			err = conn.ReadJSON(&initialMsg)
			Expect(err).NotTo(HaveOccurred())
			Expect(initialMsg["type"]).To(Equal("initial"))

			initialLines, ok := initialMsg["lines"].([]any)
			Expect(ok).To(BeTrue())
			Expect(initialLines).To(HaveLen(1))

			firstLine := initialLines[0].(map[string]any)
			Expect(firstLine["text"]).To(Equal("existing line"))

			// Now append a new line and verify it streams through
			modelLoader.BackendLogs().AppendLine("ws-model", "stderr", "streamed line")

			var lineMsg map[string]any
			conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			err = conn.ReadJSON(&lineMsg)
			Expect(err).NotTo(HaveOccurred())
			Expect(lineMsg["type"]).To(Equal("line"))

			lineData, ok := lineMsg["line"].(map[string]any)
			Expect(ok).To(BeTrue())
			Expect(lineData["text"]).To(Equal("streamed line"))
			Expect(lineData["stream"]).To(Equal("stderr"))
		})

		It("should handle connection close gracefully", func() {
			srv := httptest.NewServer(app)
			defer srv.Close()

			wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/backend-logs/close-model"
			dialer := websocket.Dialer{HandshakeTimeout: 2 * time.Second}
			conn, _, err := dialer.Dial(wsURL, nil)
			Expect(err).NotTo(HaveOccurred())

			// Read initial message
			var initialMsg map[string]any
			err = conn.ReadJSON(&initialMsg)
			Expect(err).NotTo(HaveOccurred())
			Expect(initialMsg["type"]).To(Equal("initial"))

			// Close the connection from client side
			conn.Close()

			// Give the server goroutine time to detect the close
			time.Sleep(50 * time.Millisecond)

			// No panic or hang — the test passing is the assertion
		})
	})
})
