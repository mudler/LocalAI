package localai

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/remotes"
	"github.com/mudler/xlog"
)

// Handlers of the remote routes. They are stateless: every call
// goes to the remotes service, which reads the shared store, so any
// frontend of a cluster gives the same answer and nothing is cached here.
//
// The API key is write-only. It is read from a request body, handed to the
// service and never returned, logged or put in an error message.

// remoteBodyLimit caps a request body. The largest body is an adopt or
// create call that lists models, and a few thousand of them fit in it.
const remoteBodyLimit = 1 << 20

// RemotesProvider returns the remotes service, or nil when its store
// could not be opened. It is called for every request, because the service is
// built during startup, after the routes exist.
type RemotesProvider func() remotes.Service

// RemoteTestRequest is the body of POST /api/remotes/test.
type RemoteTestRequest struct {
	Kind   remotes.Kind `json:"kind"`
	URL    string       `json:"url"`
	APIKey string       `json:"api_key"` // write-only
}

// RemoteCreateRequest is the body of POST /api/remotes.
type RemoteCreateRequest struct {
	Kind   remotes.Kind            `json:"kind"`
	Name   string                  `json:"name"`
	URL    string                  `json:"url"`
	APIKey string                  `json:"api_key"` // write-only
	Models []remotes.UpstreamModel `json:"models"`
}

// RemoteUpdateRequest is the body of PATCH /api/remotes/{id}. An
// absent field is left as it is. An empty api_key removes the key.
type RemoteUpdateRequest struct {
	Name   *string `json:"name,omitempty"`
	URL    *string `json:"url,omitempty"`
	APIKey *string `json:"api_key,omitempty"` // write-only
}

// RemoteAdoptRequest is the body of POST /api/remotes/{id}/adopt.
type RemoteAdoptRequest struct {
	Models []remotes.UpstreamModel `json:"models"`
}

// RemoteResultResponse is the body of a create or an adopt. Warning is set
// when the call was saved only in part: the remote exists, the lists say
// what was done, and the text says what failed. It never holds a secret.
type RemoteResultResponse struct {
	Remote  remotes.Remote `json:"remote"`
	Created []string       `json:"created"`
	Skipped []remotes.Skip `json:"skipped"`
	Warning string         `json:"warning,omitempty"`
}

// Warnings of a partial create or adopt.
const (
	remoteCreateWarning = "The remote was saved, but some models could not be added. Check the list of models and the server log, then add the missing models again."
	remoteAdoptWarning  = "Some models could not be added. Check the list of models and the server log, then add the missing models again."
)

// RemoteListResponse is the body of GET /api/remotes.
type RemoteListResponse struct {
	Data []remotes.Remote `json:"data"`
}

func remotesError(c echo.Context, code int, msg string) error {
	return c.JSON(code, schema.ErrorResponse{Error: &schema.APIError{Message: msg, Code: code, Type: "remote_error"}})
}

// remoteService resolves the service for one request. It writes the 503
// answer itself when there is none.
func remoteService(c echo.Context, get RemotesProvider) (remotes.Service, bool) {
	if get != nil {
		if svc := get(); svc != nil {
			return svc, true
		}
	}
	_ = remotesError(c, http.StatusServiceUnavailable, "remotes are unavailable: the remote store could not be opened (see the server log)")
	return nil, false
}

// mapRemoteError turns a service error into the HTTP answer. Only the
// message of an invalid-input error reaches the caller; any other error is
// logged and answered with a generic message.
func mapRemoteError(c echo.Context, op string, err error) error {
	return mapRemoteErrorMsg(c, op, err, "the remote operation failed; see the server log")
}

// mapRemoteErrorMsg is mapRemoteError with the text of the 500 answer
// chosen by the caller, for the operations that can fail half way.
func mapRemoteErrorMsg(c echo.Context, op string, err error, internal string) error {
	var inv *remotes.InvalidError
	switch {
	case errors.As(err, &inv):
		return remotesError(c, http.StatusBadRequest, inv.Message)
	case errors.Is(err, remotes.ErrInvalid):
		return remotesError(c, http.StatusBadRequest, "invalid remote request")
	case errors.Is(err, remotes.ErrNotFound):
		return remotesError(c, http.StatusNotFound, "remote not found")
	case errors.Is(err, remotes.ErrConflict):
		return remotesError(c, http.StatusConflict, "a remote with that name already exists")
	}
	xlog.Error("Remote operation failed", "op", op, "error", err)
	return remotesError(c, http.StatusInternalServerError, internal)
}

// decodeRemoteBody reads one JSON object from the body, within the size
// limit. It writes the error answer and returns false when the body is
// unusable.
func decodeRemoteBody(c echo.Context, dst any) bool {
	body := http.MaxBytesReader(c.Response(), c.Request().Body, remoteBodyLimit)
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
		_ = remotesError(c, http.StatusRequestEntityTooLarge, "request body is too large")
		return false
	}
	// The decoder message can name a field, never a value of the body.
	_ = remotesError(c, http.StatusBadRequest, "request body must be a JSON object with the fields of this route")
	return false
}

func remoteResult(r remotes.Result) RemoteResultResponse {
	out := RemoteResultResponse{Remote: r.Remote, Created: r.Created, Skipped: r.Skipped}
	if out.Created == nil {
		out.Created = []string{}
	}
	if out.Skipped == nil {
		out.Skipped = []remotes.Skip{}
	}
	return out
}

// partialResult answers a create or adopt that was saved only in part: the
// status of success, the partial result and a warning, so a client does not
// repeat a create into a name conflict. The cause goes to the log only.
func partialResult(c echo.Context, op string, code int, res remotes.Result, err error, warning string) error {
	xlog.Error("Remote operation was saved only in part", "op", op, "error", err)
	out := remoteResult(res)
	out.Warning = warning
	return c.JSON(code, out)
}

// CheckRemoteEndpoint lists the models of a remote without saving anything.
//
//	@Summary	Test a remote
//	@Description	Lists the models of a remote without saving anything. A failed test is a 200 answer with ok=false and the failed step. Admin only.
//	@Tags		remotes
//	@Accept		json
//	@Produce	json
//	@Param		request	body		RemoteTestRequest	true	"Remote to test; api_key is write-only, for example \"<key>\""
//	@Success	200		{object}	remotes.TestResult
//	@Failure	400		{object}	schema.ErrorResponse
//	@Failure	413		{object}	schema.ErrorResponse
//	@Failure	503		{object}	schema.ErrorResponse
//	@Router		/api/remotes/test [post]
func CheckRemoteEndpoint(get RemotesProvider) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc, ok := remoteService(c, get)
		if !ok {
			return nil
		}
		var req RemoteTestRequest
		if !decodeRemoteBody(c, &req) {
			return nil
		}
		res, err := svc.Test(c.Request().Context(), req.Kind, req.URL, req.APIKey)
		if err != nil {
			return mapRemoteError(c, "test", err)
		}
		return c.JSON(http.StatusOK, res)
	}
}

// CreateRemoteEndpoint saves a remote and adopts the chosen models.
//
//	@Summary	Create a remote
//	@Description	Saves a remote and turns each chosen model into a model config (an entry) that carries only the remote id. The key is stored sealed and is never returned. Models that cannot be adopted are listed in "skipped" with a reason. At most 500 models per call. When the remote is saved but some models could not be added, the answer is still 201 with "warning" set; read the lists and add the missing models with the adopt route, not with a second create. Admin only.
//	@Tags		remotes
//	@Accept		json
//	@Produce	json
//	@Param		request	body		RemoteCreateRequest	true	"Remote to create; api_key is write-only, for example \"<key>\""
//	@Success	201		{object}	RemoteResultResponse
//	@Failure	400		{object}	schema.ErrorResponse
//	@Failure	409		{object}	schema.ErrorResponse
//	@Failure	413		{object}	schema.ErrorResponse
//	@Failure	503		{object}	schema.ErrorResponse
//	@Router		/api/remotes [post]
func CreateRemoteEndpoint(get RemotesProvider) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc, ok := remoteService(c, get)
		if !ok {
			return nil
		}
		var req RemoteCreateRequest
		if !decodeRemoteBody(c, &req) {
			return nil
		}
		res, err := svc.Create(c.Request().Context(), remotes.CreateRequest{
			Kind: req.Kind, Name: req.Name, URL: req.URL, APIKey: req.APIKey, Models: req.Models,
		})
		if errors.Is(err, remotes.ErrPartial) && res.Remote.ID != "" {
			return partialResult(c, "create", http.StatusCreated, res, err, remoteCreateWarning)
		}
		if err != nil {
			return mapRemoteError(c, "create", err)
		}
		return c.JSON(http.StatusCreated, remoteResult(res))
	}
}

// ListRemotesEndpoint lists the saved remotes.
//
//	@Summary	List remotes
//	@Description	Lists the saved remotes. The key is never part of the answer; "key_set" says whether one is stored. Admin only.
//	@Tags		remotes
//	@Produce	json
//	@Success	200	{object}	RemoteListResponse
//	@Failure	503	{object}	schema.ErrorResponse
//	@Router		/api/remotes [get]
func ListRemotesEndpoint(get RemotesProvider) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc, ok := remoteService(c, get)
		if !ok {
			return nil
		}
		list, err := svc.List(c.Request().Context())
		if err != nil {
			return mapRemoteError(c, "list", err)
		}
		if list == nil {
			list = []remotes.Remote{}
		}
		return c.JSON(http.StatusOK, RemoteListResponse{Data: list})
	}
}

// GetRemoteEndpoint returns one remote.
//
//	@Summary	Get a remote
//	@Tags		remotes
//	@Produce	json
//	@Param		id	path		string	true	"Remote id"
//	@Success	200	{object}	remotes.Remote
//	@Failure	404	{object}	schema.ErrorResponse
//	@Failure	503	{object}	schema.ErrorResponse
//	@Router		/api/remotes/{id} [get]
func GetRemoteEndpoint(get RemotesProvider) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc, ok := remoteService(c, get)
		if !ok {
			return nil
		}
		conn, err := svc.Get(c.Request().Context(), c.Param("id"))
		if err != nil {
			return mapRemoteError(c, "get", err)
		}
		return c.JSON(http.StatusOK, conn)
	}
}

// UpdateRemoteEndpoint changes the name, address or key of a remote.
//
//	@Summary	Update a remote
//	@Description	An absent field is left as it is; an empty api_key removes the key (not allowed for the anthropic kind). The key must be printable ASCII. A new key restarts every loaded model that names the remote and a new url patches the entries the remote owns. A new key or url also refreshes the remote, so status, last_error, new and unavailable in the answer are current; a rename does not. When the change is saved but patching some entries failed, the answer is a 500 error that says so, not the saved remote: read it again with GET. Admin only.
//	@Tags		remotes
//	@Accept		json
//	@Produce	json
//	@Param		id		path		string					true	"Remote id"
//	@Param		request	body		RemoteUpdateRequest	true	"Fields to change; api_key is write-only, for example \"<key>\""
//	@Success	200		{object}	remotes.Remote
//	@Failure	400		{object}	schema.ErrorResponse
//	@Failure	404		{object}	schema.ErrorResponse
//	@Failure	409		{object}	schema.ErrorResponse
//	@Failure	413		{object}	schema.ErrorResponse
//	@Failure	503		{object}	schema.ErrorResponse
//	@Failure	500	{object}	schema.ErrorResponse
//	@Router		/api/remotes/{id} [patch]
func UpdateRemoteEndpoint(get RemotesProvider) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc, ok := remoteService(c, get)
		if !ok {
			return nil
		}
		var req RemoteUpdateRequest
		if !decodeRemoteBody(c, &req) {
			return nil
		}
		conn, err := svc.Update(c.Request().Context(), c.Param("id"), remotes.UpdateRequest{Name: req.Name, URL: req.URL, APIKey: req.APIKey})
		if err != nil {
			// The service returns the saved remote together with an
			// error when patching some entries failed. The status carries the
			// failure; the caller reads the saved state with GET.
			if errors.Is(err, remotes.ErrPartial) {
				return mapRemoteErrorMsg(c, "update", err, "the change was saved, but some entries of the remote could not be updated; read the remote again and see the server log")
			}
			return mapRemoteError(c, "update", err)
		}
		return c.JSON(http.StatusOK, conn)
	}
}

// DeleteRemoteEndpoint removes a remote.
//
//	@Summary	Delete a remote
//	@Description	With entries=keep (the default) the entries stay as plain models without the remote and its key; with entries=remove they are deleted. When some entries could not be handled, the remote stays and the answer is a 500 error; a retry finishes the job. Admin only.
//	@Tags		remotes
//	@Produce	json
//	@Param		id		path	string	true	"Remote id"
//	@Param		entries	query	string	false	"What to do with the entries"	Enums(keep, remove)	default(keep)
//	@Success	204
//	@Failure	400	{object}	schema.ErrorResponse
//	@Failure	404	{object}	schema.ErrorResponse
//	@Failure	503	{object}	schema.ErrorResponse
//	@Failure	500	{object}	schema.ErrorResponse
//	@Router		/api/remotes/{id} [delete]
func DeleteRemoteEndpoint(get RemotesProvider) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc, ok := remoteService(c, get)
		if !ok {
			return nil
		}
		var remove bool
		switch c.QueryParam("entries") {
		case "", "keep":
		case "remove":
			remove = true
		default:
			return remotesError(c, http.StatusBadRequest, `entries must be "keep" or "remove"`)
		}
		if err := svc.Delete(c.Request().Context(), c.Param("id"), remove); err != nil {
			// A failure half way leaves the remote and the entries that
			// were not handled in place, so a retry can finish the job.
			return mapRemoteErrorMsg(c, "delete", err, "the remote was not fully removed; some entries could not be handled, retry or see the server log")
		}
		return c.NoContent(http.StatusNoContent)
	}
}

// RefreshRemoteEndpoint asks the remote for its models.
//
//	@Summary	Refresh a remote
//	@Description	Asks the remote for its models and records the answer: status, "new" (found, not adopted) and "unavailable" (adopted, no longer listed). It never writes an entry. Admin only.
//	@Tags		remotes
//	@Produce	json
//	@Param		id	path		string	true	"Remote id"
//	@Success	200	{object}	remotes.Remote
//	@Failure	404	{object}	schema.ErrorResponse
//	@Failure	503	{object}	schema.ErrorResponse
//	@Router		/api/remotes/{id}/refresh [post]
func RefreshRemoteEndpoint(get RemotesProvider) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc, ok := remoteService(c, get)
		if !ok {
			return nil
		}
		conn, err := svc.Refresh(c.Request().Context(), c.Param("id"))
		if err != nil {
			return mapRemoteError(c, "refresh", err)
		}
		return c.JSON(http.StatusOK, conn)
	}
}

// AdoptRemoteModelsEndpoint turns models of a remote into entries.
//
//	@Summary	Adopt models of a remote
//	@Description	Turns each chosen model into a model config (an entry) that carries only the remote id. Models that cannot be adopted are listed in "skipped" with a reason. At most 500 models per call. When some models were added but the rest failed, the answer is still 200 with "warning" set. Admin only.
//	@Tags		remotes
//	@Accept		json
//	@Produce	json
//	@Param		id		path		string					true	"Remote id"
//	@Param		request	body		RemoteAdoptRequest	true	"Models to adopt"
//	@Success	200		{object}	RemoteResultResponse
//	@Failure	400		{object}	schema.ErrorResponse
//	@Failure	404		{object}	schema.ErrorResponse
//	@Failure	413		{object}	schema.ErrorResponse
//	@Failure	503		{object}	schema.ErrorResponse
//	@Router		/api/remotes/{id}/adopt [post]
func AdoptRemoteModelsEndpoint(get RemotesProvider) echo.HandlerFunc {
	return func(c echo.Context) error {
		svc, ok := remoteService(c, get)
		if !ok {
			return nil
		}
		var req RemoteAdoptRequest
		if !decodeRemoteBody(c, &req) {
			return nil
		}
		res, err := svc.Adopt(c.Request().Context(), c.Param("id"), req.Models)
		if errors.Is(err, remotes.ErrPartial) && res.Remote.ID != "" {
			return partialResult(c, "adopt", http.StatusOK, res, err, remoteAdoptWarning)
		}
		if err != nil {
			return mapRemoteError(c, "adopt", err)
		}
		return c.JSON(http.StatusOK, remoteResult(res))
	}
}
