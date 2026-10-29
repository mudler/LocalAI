package studio

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/schema"
	studiosvc "github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

// answer runs a mapper on err and decodes the one JSON document it writes.
func answer(t *testing.T, mapper func(echo.Context, error) error, err error) (int, schema.APIError) {
	t.Helper()
	g := gomega.NewWithT(t)
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/api/chats/x", nil), rec)
	g.Expect(mapper(c, err)).To(gomega.Succeed())
	var body schema.ErrorResponse
	g.Expect(json.NewDecoder(rec.Body).Decode(&body)).To(gomega.Succeed())
	g.Expect(body.Error).NotTo(gomega.BeNil())
	return rec.Code, *body.Error
}

func TestErrorTablesGiveTheStatusTypeAndTextOfEachRow(t *testing.T) {
	tests := []struct {
		name   string
		mapper func(echo.Context, error) error
		err    error
		status int
		typ    string
		msg    string
	}{
		{"a missing conversation", failFor, session.ErrNotFound, 404, "not_found", "conversation not found"},
		{"a wrapped sentinel still matches", failFor, fmt.Errorf("opening: %w", session.ErrNotFound), 404, "not_found", "conversation not found"},
		{"a busy chat", failFor, studiosvc.ErrChatBusy, 409, "conflict", "a turn is running; stop it first"},
		{"a refusal keeps its text less the package prefix", failFor, fmt.Errorf("%w", studiosvc.ErrNothingToRegenerate), 400, "invalid_request_error", "the chat has no message to answer again"},
		{"an attachment over the limit", failFor, workspace.ErrTooLarge, 413, "invalid_request_error", "an attachment is too large"},
		{"an unknown error hides its text", failFor, errors.New("secret detail"), 500, "server_error", "the request failed"},
		{"a missing file is a missing path", failFile, workspace.ErrReadOnly, 404, "not_found", "not found"},
		{"a file over the limit", failFile, workspace.ErrTooLarge, 413, "invalid_request_error", "the file is too large"},
		{"the body limit of the server", failFile, echo.NewHTTPError(http.StatusRequestEntityTooLarge), 413, "invalid_request_error", "the file is too large"},
		{"an unknown file error hides its text", failFile, errors.New("disk path /x"), 500, "server_error", "the request failed"},
		{"a missing trigger", failTrigger, triggers.ErrNotFound, 404, "not_found", "trigger not found"},
		{"a bad trigger says why", failTrigger, fmt.Errorf("%w: the schedule is empty", studiosvc.ErrBadTrigger), 400, "invalid_request_error", "the schedule is empty"},
		{"triggers off", failTrigger, studiosvc.ErrTriggersOff, 409, "triggers_off", studiosvc.ErrTriggersOff.Error()},
		{"a trigger error falls to the chat table", failTrigger, session.ErrNotFound, 404, "not_found", "conversation not found"},
		{"an app refusal keeps its reason", failApp, fmt.Errorf("%w: not declared", studiosvc.ErrMCPAppRefused), 403, "mcp_app_refused", "not declared"},
		{"an app refusal with no reason", failApp, studiosvc.ErrMCPAppRefused, 403, "mcp_app_refused", ""},
		{"an MCP content failure", failMCPContent, fmt.Errorf("%w: server said no", studiosvc.ErrMCPContent), 502, "mcp_error", "server said no"},
	}
	g := gomega.NewWithT(t)
	for _, tt := range tests {
		status, apiErr := answer(t, tt.mapper, tt.err)
		g.Expect(status).To(gomega.Equal(tt.status), tt.name)
		g.Expect(apiErr.Type).To(gomega.Equal(tt.typ), tt.name)
		g.Expect(apiErr.Code).To(gomega.BeEquivalentTo(tt.status), tt.name)
		g.Expect(apiErr.Message).To(gomega.ContainSubstring(tt.msg), tt.name)
	}
}

func TestBindAnswers400ForBadJSONAnd413ForABodyOverTheLimit(t *testing.T) {
	type body struct {
		Name string `json:"name"`
	}
	run := func(raw string, limit int64) (body, bool, int, schema.APIError) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		c := echo.New().NewContext(req, rec)
		if limit > 0 {
			c.Request().Body = http.MaxBytesReader(rec, c.Request().Body, limit)
		}
		got, ok := bind[body](c, "the body is not valid JSON")
		var out schema.ErrorResponse
		if !ok {
			_ = json.NewDecoder(rec.Body).Decode(&out)
			return got, ok, rec.Code, *out.Error
		}
		return got, ok, rec.Code, schema.APIError{}
	}
	g := gomega.NewWithT(t)
	got, ok, _, _ := run(`{"name":"a"}`, 0)
	g.Expect(ok).To(gomega.BeTrue())
	g.Expect(got.Name).To(gomega.Equal("a"))
	_, ok, status, apiErr := run(`{"name":`, 0)
	g.Expect(ok).To(gomega.BeFalse())
	g.Expect(status).To(gomega.Equal(http.StatusBadRequest))
	g.Expect(apiErr.Message).To(gomega.Equal("the body is not valid JSON"))
	_, ok, status, apiErr = run(`{"name":"`+strings.Repeat("x", 100)+`"}`, 16)
	g.Expect(ok).To(gomega.BeFalse())
	g.Expect(status).To(gomega.Equal(http.StatusRequestEntityTooLarge))
	g.Expect(apiErr.Type).To(gomega.Equal("invalid_request_error"))
}
