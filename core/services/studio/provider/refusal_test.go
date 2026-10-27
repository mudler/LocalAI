package provider_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/mudler/cogito"

	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/mudler/LocalAI/core/services/studio/session"
)

func TestARefusedRequestIsReportedWithAMarkerTheLoopRecognizes(t *testing.T) {
	for code, mark := range map[int]string{http.StatusForbidden: session.MarkNotAllowed, http.StatusTooManyRequests: session.MarkQuota} {
		tr := provider.NewTransport()
		tr.SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }))
		_, err := get(t, tr.Client(nil), context.Background(), "http://studio.invalid/v1/chat/completions")
		if err == nil || !strings.Contains(err.Error(), mark) {
			t.Errorf("status %d gave error %v, want one carrying %q", code, err, mark)
		}
		// No retry fixes a refusal: the model client must stop at once.
		if !cogito.IsPermanent(err) {
			t.Errorf("status %d: the error is not marked permanent, so the agent loop would retry it", code)
		}
	}
}

// A model whose last load failed is held in a cooldown, and the route says so.
// Retrying inside the loop only waits for the same failure, so the turn must end
// with the reason instead of staying running.
func TestAModelThatCannotLoadEndsTheTurnAtOnce(t *testing.T) {
	tr := provider.NewTransport()
	tr.SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.Header().Set(model.LoadFailedHeader, "true")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"code":503,"message":"model \"m\" load is in cooldown after a recent failure; retry after 30s"}}`))
	}))
	_, err := get(t, tr.Client(nil), context.Background(), "http://studio.invalid/v1/chat/completions")
	if err == nil || !cogito.IsPermanent(err) {
		t.Fatalf("error = %v, want a permanent one", err)
	}
	if !strings.Contains(err.Error(), "could not be loaded") {
		t.Errorf("error %q does not say the model could not be loaded", err)
	}
}

func TestAnOverloadedModelIsStillRetried(t *testing.T) {
	tr := provider.NewTransport()
	tr.SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	resp, err := get(t, tr.Client(nil), context.Background(), "http://studio.invalid/v1/chat/completions")
	if err != nil {
		t.Fatalf("a plain 503 became an error: %v", err)
	}
	_ = resp.Body.Close()
}
