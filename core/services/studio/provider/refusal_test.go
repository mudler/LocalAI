package provider_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/core/services/studio/provider"
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
	}
}
