package studio

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/schema"
	studiosvc "github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/triggers"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// answer runs a mapper on err and decodes the one JSON document it writes.
func answer(mapper func(echo.Context, error) error, err error) (int, schema.APIError) {
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/api/chats/x", nil), rec)
	Expect(mapper(c, err)).To(Succeed())
	var body schema.ErrorResponse
	Expect(json.NewDecoder(rec.Body).Decode(&body)).To(Succeed())
	Expect(body.Error).NotTo(BeNil())
	return rec.Code, *body.Error
}

var _ = Describe("the rate limit of a connector call", func() {
	It("sends the clamped wait in Retry-After", func() {
		for in, want := range map[int]string{0: "1", -5: "1", 30: "30", contract.MaxRetryAfter + 500: strconv.Itoa(contract.MaxRetryAfter)} {
			rec := httptest.NewRecorder()
			c := echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/api/chats/x", nil), rec)
			Expect(failConnector(c, &contract.ErrRateLimited{RetryAfter: in})).To(Succeed())
			Expect(rec.Code).To(Equal(http.StatusTooManyRequests))
			Expect(rec.Header().Get("Retry-After")).To(Equal(want), "raw wait %d", in)
		}
	})
})

var _ = Describe("the studio endpoint helpers", func() {
	It("gives the status, type and text of each row of the error tables", func() {
		tests := []struct {
			name   string
			mapper func(echo.Context, error) error
			err    error
			status int
			typ    string
			msg    string
		}{
			{"an unknown sandbox profile", failFor, studiosvc.ErrSandboxProfileUnknown, 400, "sandbox_profile_unknown", "the sandbox has no such profile"},
			{"a sandbox profile the policy closed", failFor, studiosvc.ErrSandboxProfileNotAllowed, 403, "sandbox_profile_not_allowed", "an administrator did not give you this sandbox profile"},
			{"no sandbox with profiles", failFor, studiosvc.ErrNoSandboxProfiles, 409, "no_sandbox_profiles", "the commands of this chat do not run in a sandbox with profiles"},
			{"a missing conversation", failFor, session.ErrNotFound, 404, "not_found", "conversation not found"},
			{"a wrapped sentinel still matches", failFor, fmt.Errorf("opening: %w", session.ErrNotFound), 404, "not_found", "conversation not found"},
			{"a busy chat", failFor, studiosvc.ErrChatBusy, 409, "conflict", "a turn is running; stop it first"},
			{"an agent over its folder budget", failFor, fmt.Errorf("secret /path: %w", studiosvc.ErrAgentFolderLimit), 409, "agent_folder_limit", "This agent cannot start because its file access exceeds the folder limit. Ask an administrator to reduce the shared folders available to you, then try again."},
			{"an agent whose folder is missing", failFor, fmt.Errorf("opening: %w", studiosvc.ErrAgentFolderMissing), 409, "agent_folder_missing", "The folder of this agent is missing. Choose a folder in the agent's settings."},
			{"a refusal keeps its text less the package prefix", failFor, fmt.Errorf("%w", studiosvc.ErrNothingToRegenerate), 400, "invalid_request_error", "the chat has no message to answer again"},
			{"an attachment over the limit", failFor, workspace.ErrTooLarge, 413, "invalid_request_error", "an attachment is too large"},
			{"an unknown error hides its text", failFor, errors.New("secret detail"), 500, "server_error", "the request failed"},
			{"a missing file is a missing path", failFile, workspace.ErrReadOnly, 404, "not_found", "not found"},
			{"a file over the limit", failFile, workspace.ErrTooLarge, 413, "invalid_request_error", "the file is too large"},
			{"the body limit of the server", failFile, echo.NewHTTPError(http.StatusRequestEntityTooLarge), 413, "invalid_request_error", "the file is too large"},
			{"an unknown file error hides its text", failFile, errors.New("disk path /x"), 500, "server_error", "the request failed"},
			{"an action of the owner of a shared agent", failFor, studiosvc.ErrAgentNotOwner, 403, "authorization_error", "only the owner of this agent can do this"},
			{"an agent that is no longer shared", failFor, studiosvc.ErrAgentNotShared, 409, "agent_not_shared", "this agent is no longer shared with you"},
			{"a server that does not share agents", failFor, fmt.Errorf("%w: no policy", studiosvc.ErrAgentSharingOff), 409, "sharing_disabled", "agents cannot be shared on this server"},
			{"a missing trigger", failTrigger, triggers.ErrNotFound, 404, "not_found", "trigger not found"},
			{"a bad trigger says why", failTrigger, fmt.Errorf("%w: the schedule is empty", studiosvc.ErrBadTrigger), 400, "invalid_request_error", "the schedule is empty"},
			{"triggers off", failTrigger, studiosvc.ErrTriggersOff, 409, "triggers_off", studiosvc.ErrTriggersOff.Error()},
			{"a trigger error falls to the chat table", failTrigger, session.ErrNotFound, 404, "not_found", "conversation not found"},
			{"an app refusal keeps its reason", failApp, fmt.Errorf("%w: not declared", studiosvc.ErrMCPAppRefused), 403, "mcp_app_refused", "not declared"},
			{"a host denial for a connector", failConnector, fmt.Errorf("verifying: %w", connectors.ErrForbidden), 403, "authorization_error", "not allowed"},
			{"a host denial does not name the part that failed", failConnector, fmt.Errorf("agent \"secret-agent\": %w", connectors.ErrForbidden), 403, "authorization_error", "not allowed"},
			{"an app refusal with no reason", failApp, studiosvc.ErrMCPAppRefused, 403, "mcp_app_refused", ""},
			{"an MCP content failure", failMCPContent, fmt.Errorf("%w: server said no", studiosvc.ErrMCPContent), 502, "mcp_error", "server said no"},
		}
		for _, tt := range tests {
			status, apiErr := answer(tt.mapper, tt.err)
			Expect(status).To(Equal(tt.status), tt.name)
			Expect(apiErr.Type).To(Equal(tt.typ), tt.name)
			Expect(apiErr.Code).To(BeEquivalentTo(tt.status), tt.name)
			Expect(apiErr.Message).To(ContainSubstring(tt.msg), tt.name)
			Expect(apiErr.Message).NotTo(ContainSubstring("secret-agent"), tt.name)
		}
	})

	It("bind answers 400 for bad JSON and 413 for a body over the limit", func() {
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
		got, ok, _, _ := run(`{"name":"a"}`, 0)
		Expect(ok).To(BeTrue())
		Expect(got.Name).To(Equal("a"))
		_, ok, status, apiErr := run(`{"name":`, 0)
		Expect(ok).To(BeFalse())
		Expect(status).To(Equal(http.StatusBadRequest))
		Expect(apiErr.Message).To(Equal("the body is not valid JSON"))
		_, ok, status, apiErr = run(`{"name":"`+strings.Repeat("x", 100)+`"}`, 16)
		Expect(ok).To(BeFalse())
		Expect(status).To(Equal(http.StatusRequestEntityTooLarge))
		Expect(apiErr.Type).To(Equal("invalid_request_error"))
	})
})

var _ = Describe("the feature guard", func() {
	off := func(c echo.Context) error { return fail(c, http.StatusConflict, "thing_off", "the thing is off") }

	It("returns the host of a feature that is on and writes nothing", func() {
		rec := httptest.NewRecorder()
		c := echo.New().NewContext(httptest.NewRequest(http.MethodGet, "/api/chats/x", nil), rec)
		host := &struct{ name string }{"on"}
		got, ok := feature(c, host, off)
		Expect(ok).To(BeTrue())
		Expect(got).To(BeIdenticalTo(host))
		Expect(rec.Body.Len()).To(BeZero())
	})

	It("writes the answer of a feature that is off", func() {
		code, body := answer(func(c echo.Context, _ error) error {
			got, ok := feature[struct{}](c, nil, off)
			Expect(ok).To(BeFalse())
			Expect(got).To(BeNil())
			return nil
		}, nil)
		Expect(code).To(Equal(http.StatusConflict))
		Expect(body.Type).To(Equal("thing_off"))
		Expect(body.Message).To(Equal("the thing is off"))
	})
})
