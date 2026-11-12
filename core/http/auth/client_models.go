// SPDX-License-Identifier: MIT

package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/schema"
)

// clientModelListPath is the one route that names no model. A client key may
// call it, and the answer is the list of the key.
const clientModelListPath = "/v1/models"

// clientModelGateKey holds, in the request context, the function that decides
// whether a client key may use a model name.
const clientModelGateKey = "client_model_gate"

// ClientModelDenial is the refusal of the model gate: the HTTP status and the
// message for the caller.
type ClientModelDenial struct {
	Status  int
	Message string
}

func (d *ClientModelDenial) Error() string { return d.Message }

// CheckClientModel is the one place that decides which model a client key may
// use. A handler calls it with the name it resolved, after it bound the request
// with its own binder, so the answer cannot differ from the model that runs.
// The middleware RequireClientModels only checks early and cheaply. A request
// of another kind of credential passes untouched. A client key refuses when
// the gate is missing (the middleware is not installed), so an unprotected
// route stays closed instead of open. A name that is empty is refused with 400.
func CheckClientModel(c echo.Context, names ...string) *ClientModelDenial {
	k := ClientKey(c)
	if k == nil {
		return nil
	}
	gate, _ := c.Get(clientModelGateKey).(func(string) *ClientModelDenial)
	if gate == nil || !k.ClientAllows(FeatureClientInference) {
		return &ClientModelDenial{Status: http.StatusForbidden, Message: "this key cannot use a model on this route"}
	}
	if len(names) == 0 {
		return &ClientModelDenial{Status: http.StatusBadRequest, Message: "name a model: this key has no default model"}
	}
	for _, name := range names {
		if d := gate(name); d != nil {
			return d
		}
	}
	return nil
}

// ClientModelDenialResponse writes the refusal as an API error.
func ClientModelDenialResponse(c echo.Context, d *ClientModelDenial) error {
	return clientModelRefusal(c, d.Status, d.Message)
}

// RequireClientModels returns a global middleware for client keys that hold
// client_inference. It is the cheap, early half of the model check: a request
// of such a key must name a model, every model it names (path, query, JSON body,
// form) must be in the list of the key, and a request whose shape it cannot read
// the way a handler binder does is refused (a body that is not JSON, a form or
// a multipart form, trailing data after the JSON value, a model field twice or
// in another case). The deciding check is CheckClientModel, which a handler
// calls with the model it resolved; this middleware installs the gate it uses.
// A model that no longer passes check is refused too, since a configuration can
// gain tools after the key was made. The model list route is the exception: the
// handler filters its answer by the list.
//
// Only the name of the model the request addresses is checked. A realtime
// pipeline calls its own stages, and those names are not the client's choice.
// A request that is not of a client key passes untouched. check may be nil.
func RequireClientModels(check ClientModelCheck) echo.MiddlewareFunc {
	inference := map[string]bool{}
	for _, rf := range RouteFeatureRegistry {
		if rf.Feature == FeatureClientInference {
			inference[rf.Method+" "+rf.Pattern] = true
		}
	}
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			k := ClientKey(c)
			if k == nil || !k.ClientAllows(FeatureClientInference) {
				return next(c)
			}
			// Another client feature on a route it owns has no model to name.
			if !inference[c.Request().Method+" "+c.Path()] {
				return next(c)
			}
			gate := newClientModelGate(k, check)
			c.Set(clientModelGateKey, gate)
			if c.Request().Method == http.MethodGet && c.Path() == clientModelListPath {
				return next(c)
			}
			names, status, err := requestedModels(c)
			if err != nil {
				return clientModelRefusal(c, status, err.Error())
			}
			if len(names) == 0 {
				return clientModelRefusal(c, http.StatusBadRequest, "name a model: this key has no default model")
			}
			for _, name := range names {
				if d := gate(name); d != nil {
					return ClientModelDenialResponse(c, d)
				}
			}
			return next(c)
		}
	}
}

func newClientModelGate(k *UserAPIKey, check ClientModelCheck) func(string) *ClientModelDenial {
	return func(name string) *ClientModelDenial {
		if !k.AllowsModel(name) {
			return &ClientModelDenial{Status: http.StatusForbidden, Message: "this key cannot use the model: " + name}
		}
		if check != nil {
			if err := check(name); err != nil {
				return &ClientModelDenial{Status: http.StatusForbidden, Message: "this key cannot use the model " + name + ": " + err.Error()}
			}
		}
		return nil
	}
}

// InstallClientModelGate installs the gate that CheckClientModel uses, and
// nothing else: it reads no part of the request. RequireClientModels does the
// same and adds the early check. It exists so a test can show that the check
// in the handler decides alone.
func InstallClientModelGate(check ClientModelCheck) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if k := ClientKey(c); k != nil && k.ClientAllows(FeatureClientInference) {
				c.Set(clientModelGateKey, newClientModelGate(k, check))
			}
			return next(c)
		}
	}
}

func clientModelRefusal(c echo.Context, status int, msg string) error {
	return c.JSON(status, schema.ErrorResponse{
		Error: &schema.APIError{Message: msg, Code: status, Type: "authorization_error"},
	})
}

// errAmbiguousModel is returned for a request whose model cannot be read the
// way the handler binder reads it.
var errAmbiguousModel = errors.New("the request names its model in a way that cannot be read without doubt")

// requestedModels returns every model name the request carries: the path
// parameter, the query, a JSON body and a form field. The handler picks one of
// them, and which one is its business, so the middleware asks for all of them.
// It refuses, with an HTTP status, any shape that the binder of the handler
// could read in another way than this function does: a content type other than
// JSON, urlencoded form or multipart form (the binder also reads XML, which
// this function does not parse), a JSON body that is not one object, data after
// the JSON value, a model key twice or in another case, and a model given
// twice in the query. The body is put back for the handler.
func requestedModels(c echo.Context) ([]string, int, error) {
	var out []string
	add := func(m string) {
		if m != "" {
			out = append(out, m)
		}
	}
	add(c.Param("model"))
	req := c.Request()
	if q := req.URL.Query()["model"]; len(q) > 1 {
		return nil, http.StatusBadRequest, errAmbiguousModel
	} else if len(q) == 1 {
		add(q[0])
	}
	for key := range req.URL.Query() {
		if key != "model" && strings.EqualFold(key, "model") {
			return nil, http.StatusBadRequest, errAmbiguousModel
		}
	}
	// The binder matches the media type by prefix and case-sensitively, so the
	// same is done here: whatever it would read as another type is refused.
	ct := req.Header.Get(echo.HeaderContentType)
	switch {
	case strings.HasPrefix(ct, echo.MIMEMultipartForm), strings.HasPrefix(ct, echo.MIMEApplicationForm):
		if err := formModels(c, add); err != nil {
			return nil, http.StatusBadRequest, err
		}
		return out, 0, nil
	}
	if req.Body == nil || req.Body == http.NoBody {
		return out, 0, nil
	}
	body, err := io.ReadAll(req.Body)
	req.Body = io.NopCloser(bytes.NewReader(body))
	if err != nil {
		return nil, http.StatusBadRequest, errors.New("the request body could not be read")
	}
	// The binder reads no body when there is none, whatever the content type.
	if len(body) == 0 {
		return out, 0, nil
	}
	if !strings.HasPrefix(ct, echo.MIMEApplicationJSON) {
		return nil, http.StatusUnsupportedMediaType, errors.New("a key of a paired client sends JSON, a form or a multipart form")
	}
	name, err := jsonModel(body)
	if err != nil {
		return nil, http.StatusBadRequest, err
	}
	add(name)
	return out, 0, nil
}

// formModels reads the model of a urlencoded or multipart form and refuses a
// key that spells model in another case, since the binder falls back to a match
// without case, and a model given twice.
func formModels(c echo.Context, add func(string)) error {
	req := c.Request()
	if strings.HasPrefix(req.Header.Get(echo.HeaderContentType), echo.MIMEMultipartForm) {
		if err := req.ParseMultipartForm(32 << 20); err != nil {
			return errors.New("the form could not be read")
		}
	} else if err := req.ParseForm(); err != nil {
		return errors.New("the form could not be read")
	}
	check := func(values map[string][]string) error {
		for key, v := range values {
			if !strings.EqualFold(key, "model") {
				continue
			}
			if key != "model" || len(v) != 1 {
				return errAmbiguousModel
			}
			add(v[0])
		}
		return nil
	}
	if req.MultipartForm != nil {
		if err := check(req.MultipartForm.Value); err != nil {
			return err
		}
	}
	return check(req.PostForm)
}

// jsonModel returns the model of a JSON body the way the binder reads it:
// one object and nothing after it, with the key model matched without case. A
// key twice, a model that is not a string, or data after the object are refused.
func jsonModel(body []byte) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	tok, err := dec.Token()
	if err != nil {
		return "", errors.New("the request body is not valid JSON")
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return "", errors.New("the request body must be a JSON object")
	}
	var name string
	seen := 0
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return "", errors.New("the request body is not valid JSON")
		}
		key, _ := keyTok.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return "", errors.New("the request body is not valid JSON")
		}
		if !strings.EqualFold(key, "model") {
			continue
		}
		seen++
		if seen > 1 {
			return "", errAmbiguousModel
		}
		if string(raw) == "null" {
			continue
		}
		if err := json.Unmarshal(raw, &name); err != nil {
			// A model that is not a string is not a name from the list.
			return "", errors.New("the model must be a string")
		}
	}
	if _, err := dec.Token(); err != nil { // the closing brace
		return "", errors.New("the request body is not valid JSON")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return "", errors.New("the request body has data after the JSON value")
	}
	return name, nil
}
