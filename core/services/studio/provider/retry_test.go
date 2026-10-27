package provider_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mudler/cogito"

	"github.com/mudler/LocalAI/core/services/studio/provider"
)

func post(t *testing.T, c *http.Client, body string) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://studio.invalid/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return c.Do(req)
}

// A model that keeps failing must end the turn, not keep the loop retrying for
// minutes: after a few failures in a row the client says so for good.
func TestAModelThatKeepsFailingStopsTheRetries(t *testing.T) {
	var calls atomic.Int32
	tr := provider.NewTransport()
	tr.SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "backend down", http.StatusInternalServerError)
	}))
	c := tr.Client(nil)
	var err error
	for i := 0; i < provider.MaxModelFailures+3; i++ {
		var resp *http.Response
		if resp, err = post(t, c, `{}`); err != nil {
			break
		}
		_ = resp.Body.Close()
	}
	if err == nil {
		t.Fatalf("%d failed requests in a row were all let through", calls.Load())
	}
	if !cogito.IsPermanent(err) || !strings.Contains(err.Error(), "failed") {
		t.Fatalf("err = %v, want a permanent error that says the model failed", err)
	}
	if got := int(calls.Load()); got != provider.MaxModelFailures {
		t.Fatalf("%d requests reached the model, want %d", got, provider.MaxModelFailures)
	}
}

func TestASuccessResetsTheCountOfFailures(t *testing.T) {
	var n atomic.Int32
	tr := provider.NewTransport()
	tr.SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Fail MaxModelFailures-1 times, succeed once, and do it again: never refused.
		if n.Add(1)%int32(provider.MaxModelFailures) != 0 {
			http.Error(w, "busy", http.StatusBadGateway)
			return
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	c := tr.Client(nil)
	for i := 0; i < provider.MaxModelFailures*3; i++ {
		resp, err := post(t, c, `{}`)
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		_ = resp.Body.Close()
	}
}

// Real providers reject a request that names a tool twice.
func TestAToolNamedTwiceIsSentOnce(t *testing.T) {
	var got string
	tr := provider.NewTransport()
	tr.SetHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
	}))
	body := `{"model":"m","tools":[{"type":"function","function":{"name":"a"}},{"type":"function","function":{"name":"b"}},{"type":"function","function":{"name":"a","description":"second"}}]}`
	resp, err := post(t, tr.Client(nil), body)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if strings.Count(got, `"name":"a"`) != 1 || !strings.Contains(got, `"name":"b"`) || !strings.Contains(got, `"model":"m"`) {
		t.Fatalf("the model got %s", got)
	}
	// A body with no repeated tool is passed on untouched.
	plain := `{"model":"m","tools":[{"type":"function","function":{"name":"a"}}]}`
	resp, err = post(t, tr.Client(nil), plain)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if got != plain {
		t.Fatalf("a clean body was changed: %s", got)
	}
}
