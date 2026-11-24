package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/pkg/clientip"
)

// The login throttle keys on echo's RealIP. With the shared resolver set as
// the IPExtractor, a client cannot dodge the limit by rotating a forged
// X-Forwarded-For, and clients behind a trusted proxy are throttled apart.
func TestRateLimitKeysOnResolvedClient(t *testing.T) {
	run := func(trusted string, peer string, xffs []string) []int {
		r, err := clientip.Parse(trusted)
		if err != nil {
			t.Fatal(err)
		}
		e := echo.New()
		e.IPExtractor = r.Extractor()
		e.Use(rateLimitMiddleware(newRateLimiter(time.Minute, 2)))
		e.POST("/login", func(c echo.Context) error { return c.NoContent(http.StatusOK) })
		var codes []int
		for _, x := range xffs {
			req := httptest.NewRequest(http.MethodPost, "/login", nil)
			req.RemoteAddr = peer
			req.Header.Set("X-Forwarded-For", x)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			codes = append(codes, rec.Code)
		}
		return codes
	}

	// Untrusted peer rotating forged addresses is still one client.
	got := run("", "192.0.2.10:1", []string{"1.1.1.1", "2.2.2.2", "3.3.3.3"})
	if got[2] != http.StatusTooManyRequests {
		t.Fatalf("forged X-Forwarded-For bypassed the limit: %v", got)
	}
	// Behind a trusted proxy, distinct clients get distinct budgets.
	got = run("10.0.0.0/8", "10.0.0.5:1", []string{"203.0.113.1", "203.0.113.2", "203.0.113.3", "203.0.113.1", "203.0.113.1"})
	want := []int{200, 200, 200, 200, 429}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}
