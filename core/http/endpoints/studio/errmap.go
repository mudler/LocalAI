package studio

import (
	"errors"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
)

// msgNotJSON is the answer to a body that does not parse.
const msgNotJSON = "the body is not valid JSON"

// Error types of the chats API. The UI and clients match on them.
const (
	typeInvalid      = "invalid_request_error"
	typeNotFound     = "not_found"
	typeConflict     = "conflict"
	typeServerError  = "server_error"
	typeUpstream     = "upstream_error"
	typeAuthz        = "authorization_error"
	typeFilesOff     = "files_off"
	typeAgentsOff    = "agents_off"
	typeNotifyOff    = "notifications_off"
	typeTriggersOff  = "triggers_off"
	typeConnectorOff = "connectors_off"
)

// errRule is one row of an error table: the errors that match (errors.Is), the
// status and type of the answer, and its text. The text is msg when it is set;
// else the text of the error, less the prefix trim, or less the sentinel strip
// and a following ": " when strip is set.
type errRule struct {
	is     []error
	status int
	typ    string
	msg    string
	trim   string
	strip  error
}

// fixed answers with msg whatever the text of the error is, so that the answer
// never holds a detail of the failure.
func fixed(status int, typ, msg string, is ...error) errRule {
	return errRule{is: is, status: status, typ: typ, msg: msg}
}

// verbatim answers with the text of the error, less the prefix trim.
func verbatim(status int, typ, trim string, is ...error) errRule {
	return errRule{is: is, status: status, typ: typ, trim: trim}
}

// sentinelText answers with the text of the error after its sentinel. The
// text is written for the person who reads it.
func sentinelText(status int, typ string, sentinel error) errRule {
	return errRule{is: []error{sentinel}, status: status, typ: typ, strip: sentinel}
}

func (r errRule) text(err error) string {
	switch {
	case r.msg != "":
		return r.msg
	case r.strip != nil:
		return strings.TrimPrefix(strings.TrimPrefix(err.Error(), r.strip.Error()), ": ")
	}
	return strings.TrimPrefix(err.Error(), r.trim)
}

func (r errRule) matches(err error) bool {
	for _, target := range r.is {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}

// mapErr answers with the first rule that matches err. When none does it calls
// next, the mapper of the layer below.
func mapErr(c echo.Context, err error, rules []errRule, next func(echo.Context, error) error) error {
	for _, r := range rules {
		if r.matches(err) {
			return fail(c, r.status, r.typ, r.text(err))
		}
	}
	return next(c, err)
}

// bind reads the JSON body of the request into a T. When it cannot, it writes
// the answer and returns false: 413 for a body over the limit that the handler
// set with http.MaxBytesReader, else 400 with msg.
func bind[T any](c echo.Context, msg string) (T, bool) {
	var req T
	err := c.Bind(&req)
	if err == nil {
		return req, true
	}
	var tooBig *http.MaxBytesError
	var httpErr *echo.HTTPError
	if errors.As(err, &tooBig) || (errors.As(err, &httpErr) && httpErr.Code == http.StatusRequestEntityTooLarge) {
		_ = fail(c, http.StatusRequestEntityTooLarge, typeInvalid, "the body is too large")
	} else {
		_ = fail(c, http.StatusBadRequest, typeInvalid, msg)
	}
	return req, false
}
