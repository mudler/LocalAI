package cloudproxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/privacy"
	"github.com/mudler/LocalAI/core/trace"
)

// A passthrough forward traces the request body; a zero-retention request
// must leave no such trace. The forward fails early (no upstream), which
// still runs the trace of the deferred recorder.
func TestForwardViaBackend_ZeroRetentionLeavesNoTrace(t *testing.T) {
	g := NewWithT(t)
	appConfig := config.NewApplicationConfig()
	appConfig.EnableTracing = true
	appConfig.TracingMaxItems = 100
	cfg := &config.ModelConfig{Name: "proxy-model", Backend: "cloud-proxy"}
	forward := func(level privacy.Level, body string) {
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		req = req.WithContext(privacy.WithLevel(req.Context(), level))
		c := echo.New().NewContext(req, httptest.NewRecorder())
		g.Expect(ForwardViaBackend(c, cfg, []byte(body), nil, appConfig)).To(HaveOccurred())
	}
	has := func(text string) bool {
		for _, bt := range trace.GetBackendTraces() {
			if strings.Contains(bt.Summary, text) || strings.Contains(bt.Body, text) {
				return true
			}
		}
		return false
	}
	forward(privacy.Normal, `{"messages":[{"content":"NORMAL-SENTINEL"}]}`)
	g.Eventually(func() bool { return has("NORMAL-SENTINEL") }, 2*time.Second, 20*time.Millisecond).Should(BeTrue(), "a normal forward is traced")
	forward(privacy.ZeroRetention, `{"messages":[{"content":"ZERO-SENTINEL"}]}`)
	g.Consistently(func() bool { return has("ZERO-SENTINEL") }, 300*time.Millisecond, 20*time.Millisecond).Should(BeFalse())
}
