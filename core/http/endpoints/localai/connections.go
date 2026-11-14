package localai

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/connections"
	"github.com/mudler/xlog"
)

// Handlers of the remote connection routes. They are stateless: every call
// goes to the connections service, which reads the shared store, so any
// frontend of a cluster gives the same answer and nothing is cached here.
//
// The API key is write-only. It is read from a request body, handed to the
// service and never returned, logged or put in an error message.

// connectionBodyLimit caps a request body. The largest body is an adopt or
// create call that lists models, and a few thousand of them fit in it.
const connectionBodyLimit = 1 << 20

// ConnectionsProvider returns the connections service, or nil when its store
// could not be opened. It is called for every request, because the service is
// built during startup, after the routes exist.
type ConnectionsProvider func() connections.Service

// ConnectionTestRequest is the body of POST /api/connections/test.
type ConnectionTestRequest struct {
	Kind   connections.Kind `json:"kind"`
	URL    string           `json:"url"`
	APIKey string           `json:"api_key"` // write-only
}

// ConnectionModel is a model that an admin picked from a remote.
type ConnectionModel struct {
	ID       string   `json:"id"`
	Usecases []string `json:"usecases"`
}

// ConnectionCreateRequest is the body of POST /api/connections.
type ConnectionCreateRequest struct {
	Kind   connections.Kind  `json:"kind"`
	Name   string            `json:"name"`
	URL    string            `json:"url"`
	APIKey string            `json:"api_key"` // write-only
	Models []ConnectionModel `json:"models"`
}

// ConnectionUpdateRequest is the body of PATCH /api/connections/{id}. An
// absent field is left as it is. An empty api_key removes the key.
type ConnectionUpdateRequest struct {
	Name   *string `json:"name,omitempty"`
	URL    *string `json:"url,omitempty"`
	APIKey *string `json:"api_key,omitempty"` // write-only
}

// ConnectionAdoptRequest is the body of POST /api/connections/{id}/adopt.
type ConnectionAdoptRequest struct {
	Models []ConnectionModel `json:"models"`
}

// ConnectionSkipped is a model that was not turned into an entry.
type ConnectionSkipped struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// ConnectionResultResponse is the body of a create or an adopt.
type ConnectionResultResponse struct {
	Connection connections.Connection `json:"connection"`
	Created    []string               `json:"created"`
	Skipped    []ConnectionSkipped    `json:"skipped"`
}

// ConnectionListResponse is the body of GET /api/connections.
type ConnectionListResponse struct {
	Data []connections.Connection `json:"data"`
}

func connectionError(c echo.Context, code int, msg string) error {
	return c.JSON(code, schema.ErrorResponse{Error: &schema.APIError{Message: msg, Code: code, Type: "connection_error"}})
}

// connectionService resolves the service for one request. It writes the 503
// answer itself when there is none.
func connectionService(c echo.Context, get ConnectionsProvider) (connections.Service, bool) {
	if get != nil {
		if svc := get(); svc != nil {
			return svc, true
		}
	}
	_ = connectionError(c, http.StatusServiceUnavailable, "remote connections are unavailable: the connection store could not be opened (see the server log)")
	return nil, false
}

// mapConnectionError turns a service error into the HTTP answer. Only the
// message of an invalid-input error reaches the caller; any other error is
// logged and answered with a generic message.
func mapConnectionError(c echo.Context, op string, err error) error {
	return mapConnectionErrorMsg(c, op, err, "the connection operation failed; see the server log")
}

// mapConnectionErrorMsg is mapConnectionError with the text of the 500 answer
// chosen by the caller, for the operations that can fail half way.
func mapConnectionErrorMsg(c echo.Context, op string, err error, internal string) error {
	var inv *connections.InvalidError
	switch {
	case errors.As(err, &inv):
		return connectionError(c, http.StatusBadRequest, inv.Message)
	case errors.Is(err, connections.ErrInvalid):
		return connectionError(c, http.StatusBadRequest, "invalid connection request")
	case errors.Is(err, connections.ErrNotFound):
		return connectionError(c, http.StatusNotFound, "connection not found")
	case errors.Is(err, connections.ErrConflict):
		return connectionError(c, http.StatusConflict, "a connection with that name already exists")
	}
	xlog.Error("Remote connection operation failed", "op", op, "error", err)
	return connectionError(c, http.StatusInternalServerError, internal)
}

// decodeConnectionBody reads one JSON object from the body, within the size
// limit. It writes the error answer and returns false when the body is
// unusable.
func decodeConnectionBody(c echo.Context, dst any) bool {
	body := http.MaxBytesReader(c.Response(), c.Request().Body, connectionBodyLimit)
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()
	err := dec.Decode(dst)
	if err == nil {
		// Nothing but whitespace may follow the object.
		if _, terr := dec.Token(); terr != io.EOF {
			err = errors.New("trailing data")
		}
	}
	if err == nil {
		return true
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		_ = connectionError(c, http.StatusRequestEntityTooLarge, "request body is too large")
		return false
	}
	// The decoder message can name a field, never a value of the body.
	_ = connectionError(c, http.StatusBadRequest, "request body must be a JSON object with the fields of this route")
	return false
}

func upstreamModels(in []ConnectionModel) []connections.UpstreamModel {
	out := make([]connections.UpstreamModel, 0, len(in))
	for _, m := range in {
		out = append(out, connections.UpstreamModel{ID: m.ID, Usecases: m.Usecases})
	}
	return out
}

func connectionResult(r connections.Result) ConnectionResultResponse {
	out := ConnectionResultResponse{Connection: r.Connection, Created: r.Created, Skipped: make([]ConnectionSkipped, 0, len(r.Skipped))}
	if out.Created == nil {
		out.Created = []string{}
	}
	for _, s := range r.Skipped {
		out.Skipped = append(out.Skipped, ConnectionSkipped{ID: s.ID, Reason: s.Reason})
	}
	return out
}

// CheckConnectionEndpoint lists the models of a remote without saving anything.
//
//	@Summary	Test a remote connection
//	@Description	Lists the models of a remote without saving anything. A failed test is a 200 answer with ok=false and the failed step. Admin only.
//	@Tags		connections
//	@Accept		json
//	@Produce	json
//	@Param		request	body		ConnectionTestRequest	true	"Remote to test; api_key is write-only, for example \"<key>\""
//	@Success	200		{object}	connections.TestResult
//	@Failure	400		{object}	schema.ErrorResponse
//	@Failure	413		{object}	schema.ErrorResponse
//	@Failure	503		{object}	schema.ErrorResponse
//	@Router		/api/connections/test [post]
func CheckConnectionEndpoint(get ConnectionsProvider) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc, ok := connectionService(c, get)
		if !ok {
			return nil
		}
		var req ConnectionTestRequest
		if !decodeConnectionBody(c, &req) {
			return nil
		}
		res, err := svc.Test(c.Request().Context(), req.Kind, req.URL, req.APIKey)
		if err != nil {
			return mapConnectionError(c, "test", err)
		}
		return c.JSON(http.StatusOK, res)
	}
}

// CreateConnectionEndpoint saves a connection and adopts the chosen models.
//
//	@Summary	Create a remote connection
//	@Description	Saves a connection and turns each chosen model into a model config (an entry) that carries only the connection id. The key is stored sealed and is never returned. Models that cannot be adopted are listed in "skipped" with a reason. Admin only.
//	@Tags		connections
//	@Accept		json
//	@Produce	json
//	@Param		request	body		ConnectionCreateRequest	true	"Connection to create; api_key is write-only, for example \"<key>\""
//	@Success	201		{object}	ConnectionResultResponse
//	@Failure	400		{object}	schema.ErrorResponse
//	@Failure	409		{object}	schema.ErrorResponse
//	@Failure	413		{object}	schema.ErrorResponse
//	@Failure	503		{object}	schema.ErrorResponse
//	@Router		/api/connections [post]
func CreateConnectionEndpoint(get ConnectionsProvider) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc, ok := connectionService(c, get)
		if !ok {
			return nil
		}
		var req ConnectionCreateRequest
		if !decodeConnectionBody(c, &req) {
			return nil
		}
		res, err := svc.Create(c.Request().Context(), connections.CreateRequest{
			Kind: req.Kind, Name: req.Name, URL: req.URL, APIKey: req.APIKey, Models: upstreamModels(req.Models),
		})
		if err != nil {
			return mapConnectionError(c, "create", err)
		}
		return c.JSON(http.StatusCreated, connectionResult(res))
	}
}

// ListConnectionsEndpoint lists the saved connections.
//
//	@Summary	List remote connections
//	@Description	Lists the saved connections. The key is never part of the answer; "key_set" says whether one is stored. Admin only.
//	@Tags		connections
//	@Produce	json
//	@Success	200	{object}	ConnectionListResponse
//	@Failure	503	{object}	schema.ErrorResponse
//	@Router		/api/connections [get]
func ListConnectionsEndpoint(get ConnectionsProvider) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc, ok := connectionService(c, get)
		if !ok {
			return nil
		}
		list, err := svc.List(c.Request().Context())
		if err != nil {
			return mapConnectionError(c, "list", err)
		}
		if list == nil {
			list = []connections.Connection{}
		}
		return c.JSON(http.StatusOK, ConnectionListResponse{Data: list})
	}
}

// GetConnectionEndpoint returns one connection.
//
//	@Summary	Get a remote connection
//	@Tags		connections
//	@Produce	json
//	@Param		id	path		string	true	"Connection id"
//	@Success	200	{object}	connections.Connection
//	@Failure	404	{object}	schema.ErrorResponse
//	@Failure	503	{object}	schema.ErrorResponse
//	@Router		/api/connections/{id} [get]
func GetConnectionEndpoint(get ConnectionsProvider) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc, ok := connectionService(c, get)
		if !ok {
			return nil
		}
		conn, err := svc.Get(c.Request().Context(), c.Param("id"))
		if err != nil {
			return mapConnectionError(c, "get", err)
		}
		return c.JSON(http.StatusOK, conn)
	}
}

// UpdateConnectionEndpoint changes the name, address or key of a connection.
//
//	@Summary	Update a remote connection
//	@Description	An absent field is left as it is; an empty api_key removes the key (not allowed for the anthropic kind). A new key restarts the loaded entries and a new url patches the entries the connection owns. When the change is saved but patching some entries failed, the answer is a 500 error that says so, not the saved connection: read it again with GET. Admin only.
//	@Tags		connections
//	@Accept		json
//	@Produce	json
//	@Param		id		path		string					true	"Connection id"
//	@Param		request	body		ConnectionUpdateRequest	true	"Fields to change; api_key is write-only, for example \"<key>\""
//	@Success	200		{object}	connections.Connection
//	@Failure	400		{object}	schema.ErrorResponse
//	@Failure	404		{object}	schema.ErrorResponse
//	@Failure	409		{object}	schema.ErrorResponse
//	@Failure	413		{object}	schema.ErrorResponse
//	@Failure	503		{object}	schema.ErrorResponse
//	@Failure	500	{object}	schema.ErrorResponse
//	@Router		/api/connections/{id} [patch]
func UpdateConnectionEndpoint(get ConnectionsProvider) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc, ok := connectionService(c, get)
		if !ok {
			return nil
		}
		var req ConnectionUpdateRequest
		if !decodeConnectionBody(c, &req) {
			return nil
		}
		conn, err := svc.Update(c.Request().Context(), c.Param("id"), connections.UpdateRequest{Name: req.Name, URL: req.URL, APIKey: req.APIKey})
		if err != nil {
			// The service returns the saved connection together with an
			// error when patching some entries failed. The status carries the
			// failure; the caller reads the saved state with GET.
			if conn.ID != "" && !errors.Is(err, connections.ErrInvalid) && !errors.Is(err, connections.ErrNotFound) && !errors.Is(err, connections.ErrConflict) {
				return mapConnectionErrorMsg(c, "update", err, "the change was saved, but some entries of the connection could not be updated; read the connection again and see the server log")
			}
			return mapConnectionError(c, "update", err)
		}
		return c.JSON(http.StatusOK, conn)
	}
}

// DeleteConnectionEndpoint removes a connection.
//
//	@Summary	Delete a remote connection
//	@Description	With entries=keep (the default) the entries stay as plain models without the connection and its key; with entries=remove they are deleted. When some entries could not be handled, the connection stays and the answer is a 500 error; a retry finishes the job. Admin only.
//	@Tags		connections
//	@Produce	json
//	@Param		id		path	string	true	"Connection id"
//	@Param		entries	query	string	false	"What to do with the entries"	Enums(keep, remove)	default(keep)
//	@Success	204
//	@Failure	400	{object}	schema.ErrorResponse
//	@Failure	404	{object}	schema.ErrorResponse
//	@Failure	503	{object}	schema.ErrorResponse
//	@Failure	500	{object}	schema.ErrorResponse
//	@Router		/api/connections/{id} [delete]
func DeleteConnectionEndpoint(get ConnectionsProvider) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc, ok := connectionService(c, get)
		if !ok {
			return nil
		}
		var remove bool
		switch c.QueryParam("entries") {
		case "", "keep":
		case "remove":
			remove = true
		default:
			return connectionError(c, http.StatusBadRequest, `entries must be "keep" or "remove"`)
		}
		if err := svc.Delete(c.Request().Context(), c.Param("id"), remove); err != nil {
			// A failure half way leaves the connection and the entries that
			// were not handled in place, so a retry can finish the job.
			return mapConnectionErrorMsg(c, "delete", err, "the connection was not fully removed; some entries could not be handled, retry or see the server log")
		}
		return c.NoContent(http.StatusNoContent)
	}
}

// RefreshConnectionEndpoint asks the remote for its models.
//
//	@Summary	Refresh a remote connection
//	@Description	Asks the remote for its models and records the answer: status, "new" (found, not adopted) and "unavailable" (adopted, no longer listed). It never writes an entry. Admin only.
//	@Tags		connections
//	@Produce	json
//	@Param		id	path		string	true	"Connection id"
//	@Success	200	{object}	connections.Connection
//	@Failure	404	{object}	schema.ErrorResponse
//	@Failure	503	{object}	schema.ErrorResponse
//	@Router		/api/connections/{id}/refresh [post]
func RefreshConnectionEndpoint(get ConnectionsProvider) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc, ok := connectionService(c, get)
		if !ok {
			return nil
		}
		conn, err := svc.Refresh(c.Request().Context(), c.Param("id"))
		if err != nil {
			return mapConnectionError(c, "refresh", err)
		}
		return c.JSON(http.StatusOK, conn)
	}
}

// AdoptConnectionModelsEndpoint turns models of a remote into entries.
//
//	@Summary	Adopt models of a remote connection
//	@Description	Turns each chosen model into a model config (an entry) that carries only the connection id. Models that cannot be adopted are listed in "skipped" with a reason. Admin only.
//	@Tags		connections
//	@Accept		json
//	@Produce	json
//	@Param		id		path		string					true	"Connection id"
//	@Param		request	body		ConnectionAdoptRequest	true	"Models to adopt"
//	@Success	200		{object}	ConnectionResultResponse
//	@Failure	400		{object}	schema.ErrorResponse
//	@Failure	404		{object}	schema.ErrorResponse
//	@Failure	413		{object}	schema.ErrorResponse
//	@Failure	503		{object}	schema.ErrorResponse
//	@Router		/api/connections/{id}/adopt [post]
func AdoptConnectionModelsEndpoint(get ConnectionsProvider) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc, ok := connectionService(c, get)
		if !ok {
			return nil
		}
		var req ConnectionAdoptRequest
		if !decodeConnectionBody(c, &req) {
			return nil
		}
		res, err := svc.Adopt(c.Request().Context(), c.Param("id"), upstreamModels(req.Models))
		if err != nil {
			return mapConnectionError(c, "adopt", err)
		}
		return c.JSON(http.StatusOK, connectionResult(res))
	}
}
