// SPDX-License-Identifier: MIT
package ghapi

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/pkg/addons/contract"
)

const testToken = "ghp_SECRETTOKENVALUE"

// refuse serves one fixed refusal and returns a client whose log is captured.
func refuse(t *testing.T, status int, headers map[string]string, body string) (*Client, *bytes.Buffer) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		for k, v := range headers {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	var buf bytes.Buffer
	c := &Client{Base: srv.URL, HTTP: srv.Client(), Logger: slog.New(slog.NewTextHandler(&buf, nil))}
	return c, &buf
}

func TestRefusalLogHasTheReasonAndTheHeaders(t *testing.T) {
	c, buf := refuse(t, 403, map[string]string{
		"X-RateLimit-Remaining":         "4999",
		"X-RateLimit-Resource":          "core",
		"X-GitHub-Request-Id":           "ABCD:1234",
		"X-Accepted-GitHub-Permissions": "issues=write",
	}, `{"message":"Resource not accessible by personal access token","documentation_url":"https://docs.github.com/rest/issues"}`)
	ctx := WithTool(context.Background(), "create-issue")
	_, err := c.SendJSON(ctx, "POST", testToken, "/repos/owner/repo/issues", map[string]string{"body": "PRIVATE-ISSUE-TEXT"}, nil)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"tool=create-issue", "status=403", "path=/repos/owner/repo/issues",
		`message="Resource not accessible by personal access token"`,
		"documentation_url=https://docs.github.com/rest/issues",
		"x-ratelimit-remaining=4999", "x-ratelimit-resource=core",
		"x-github-request-id=ABCD:1234", `x-accepted-github-permissions="issues=write"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "retry-after") {
		t.Errorf("log names a header that was absent:\n%s", out)
	}
	if strings.Contains(err.Error(), "not accessible") {
		t.Errorf("the error carries the text of GitHub: %v", err)
	}
}

func TestSecondaryLimitIsToldApartAndReturnsTheWait(t *testing.T) {
	c, buf := refuse(t, 403, map[string]string{"Retry-After": "42"}, `{"message":"You have exceeded a secondary rate limit."}`)
	_, err := c.Get(WithTool(context.Background(), "get-issue"), testToken, "/repos/owner/repo/issues/1", nil, "")
	var limited *contract.ErrRateLimited
	if !errors.As(err, &limited) || limited.Seconds() != 42 {
		t.Fatalf("want a rate limit of 42 seconds, got %v", err)
	}
	out := buf.String()
	for _, want := range []string{"tool=get-issue", "status=403", "secondary rate limit", "retry-after=42"} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q:\n%s", want, out)
		}
	}
}

func TestValidationErrorsAreLogged(t *testing.T) {
	c, buf := refuse(t, 422, nil, `{"message":"Validation Failed","errors":[{"resource":"Issue","field":"title","code":"missing_field"}]}`)
	_, err := c.SendJSON(context.Background(), "POST", testToken, "/repos/owner/repo/issues", map[string]string{}, nil)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "missing_field") || !strings.Contains(out, "status=422") {
		t.Errorf("log lacks the validation errors:\n%s", out)
	}
}

func TestLogNeverHoldsTheTokenTheBodyOrTheQuery(t *testing.T) {
	c, buf := refuse(t, 404, nil, `{"message":"Not Found"}`)
	q := url.Values{"q": {"QUERY-SECRET"}}
	_, _ = c.Get(context.Background(), testToken, "/search/code", q, "")
	_, _ = c.SendJSON(context.Background(), "POST", testToken, "/repos/owner/repo/issues", map[string]string{"body": "PRIVATE-ISSUE-TEXT"}, nil)
	out := buf.String()
	for _, bad := range []string{testToken, "SECRETTOKEN", "Bearer", "Authorization", "PRIVATE-ISSUE-TEXT", "QUERY-SECRET"} {
		if strings.Contains(out, bad) {
			t.Errorf("log holds %q:\n%s", bad, out)
		}
	}
	if strings.Count(out, "github: call refused") != 2 {
		t.Errorf("want one line per refusal:\n%s", out)
	}
}

func TestServerErrorsAndSuccessAreNotLogged(t *testing.T) {
	c, buf := refuse(t, 502, nil, `{"message":"Bad Gateway"}`)
	_, _ = c.Get(context.Background(), testToken, "/user", nil, "")
	c, buf2 := refuse(t, 200, nil, `{}`)
	_, _ = c.Get(context.Background(), testToken, "/user", nil, "")
	if buf.Len() != 0 || buf2.Len() != 0 {
		t.Errorf("unexpected log: %q %q", buf.String(), buf2.String())
	}
}

func TestLogTextStripsControlCharactersAndCuts(t *testing.T) {
	if got := logText("a\nb\x1b[31mc\r"); strings.ContainsAny(got, "\n\r\x1b") {
		t.Errorf("control characters kept: %q", got)
	}
	long := strings.Repeat("é", 400) // 800 bytes
	got := logText(long)
	if len(got) > maxLogText+len("...(cut)") || !strings.HasSuffix(got, "...(cut)") {
		t.Errorf("not cut: %d bytes", len(got))
	}
	if body := strings.TrimSuffix(got, "...(cut)"); strings.ContainsRune(body, '\uFFFD') {
		t.Errorf("cut inside a character")
	}
	if logText("short") != "short" {
		t.Errorf("short text changed")
	}

	c, buf := refuse(t, 403, nil, `{"message":"`+strings.Repeat("x", 2000)+`\nINJECTED level=ERROR"}`)
	_, _ = c.Get(context.Background(), testToken, "/user", nil, "")
	if buf.Len() > 1500 || strings.Count(buf.String(), "\n") != 1 {
		t.Errorf("line not bounded or split (%d bytes):\n%s", buf.Len(), buf.String())
	}
}
