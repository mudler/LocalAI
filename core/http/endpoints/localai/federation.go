package localai

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/remotes"
	"github.com/mudler/xlog"
)

// Handlers of the federation routes. A group is one model name served by
// this server and other LocalAI instances (usually remotes over P2P); it is
// stored as a model config, so the handlers keep nothing and any frontend
// of a cluster gives the same answer, except for the requests in flight,
// which each frontend counts for itself.

// FederationProvider returns the federation service, or nil when the
// remotes store could not be opened.
type FederationProvider func() remotes.Federation

// FederationGroupRequest is the body of POST /api/federation/groups.
type FederationGroupRequest struct {
	// Name is the model name clients use for the group.
	Name string `json:"name"`
	// Policy is least_busy (default) or round_robin.
	Policy config.FailoverPolicy `json:"policy,omitempty"`
	// Members are the model names that serve the group: a model of this
	// server or an entry of a remote. At least two.
	Members []string `json:"members"`
}

// FederationGroupUpdateRequest is the body of PATCH
// /api/federation/groups/{name}. An absent field is left as it is.
type FederationGroupUpdateRequest struct {
	Policy  *config.FailoverPolicy `json:"policy,omitempty"`
	Members *[]string              `json:"members,omitempty"`
}

// FederationDrainRequest is the body of POST
// /api/federation/groups/{name}/drain.
type FederationDrainRequest struct {
	Model string `json:"model"`
	// Drain true stops new requests to the model; false starts them again.
	Drain bool `json:"drain"`
}

func federationService(c echo.Context, get FederationProvider) (remotes.Federation, bool) {
	if get != nil {
		if f := get(); f != nil {
			return f, true
		}
	}
	_ = remotesError(c, http.StatusServiceUnavailable, "federation is unavailable: the remote store could not be opened (see the server log)")
	return nil, false
}

func mapFederationError(c echo.Context, op string, err error) error {
	var inv *remotes.InvalidError
	switch {
	case errors.As(err, &inv):
		return remotesError(c, http.StatusBadRequest, inv.Message)
	case errors.Is(err, remotes.ErrGroupNotFound):
		return remotesError(c, http.StatusNotFound, "federation group not found")
	case errors.Is(err, remotes.ErrConflict):
		return remotesError(c, http.StatusConflict, "a model with that name already exists")
	}
	xlog.Error("Federation operation failed", "op", op, "error", err)
	return remotesError(c, http.StatusInternalServerError, "the federation operation failed; see the server log")
}

// GetFederationEndpoint returns the instances and the groups.
//
//	@Summary	Show the federation
//	@Description	Lists the instances that can serve a group (this server, id "local", and every remote of kind localai with its P2P tunnel state and its entries) and the groups. A group is a model name served by several of them with the policy least_busy or round_robin; each member has its health, whether it is drained, whether it is constrained (reached through a relay that limits the connection, so it takes a request only when no other member can) and the requests of this server in flight. Requests in flight are counted by each frontend for itself. Admin only.
//	@Tags		remotes
//	@Produce	json
//	@Success	200	{object}	remotes.FederationView
//	@Failure	503	{object}	schema.ErrorResponse
//	@Router		/api/federation [get]
func GetFederationEndpoint(get FederationProvider) echo.HandlerFunc {
	return func(c echo.Context) error {
		f, ok := federationService(c, get)
		if !ok {
			return nil
		}
		v, err := f.View(c.Request().Context())
		if err != nil {
			return mapFederationError(c, "view", err)
		}
		return c.JSON(http.StatusOK, v)
	}
}

// CreateFederationGroupEndpoint makes a group.
//
//	@Summary	Create a federation group
//	@Description	Makes a model named "name" whose requests go to one of "members" (at least two existing models: models of this server or entries of remotes) by "policy": least_busy (default; the member with the fewest requests of this server in flight) or round_robin. A member that fails is marked down and the request moves to the next member. The group is a model config with a balanced failover chain, so it shows under /api/failover too. Admin only.
//	@Tags		remotes
//	@Accept		json
//	@Produce	json
//	@Param		request	body		FederationGroupRequest	true	"The group"
//	@Success	201		{object}	remotes.Group
//	@Failure	400		{object}	schema.ErrorResponse
//	@Failure	409		{object}	schema.ErrorResponse
//	@Failure	413		{object}	schema.ErrorResponse
//	@Failure	503		{object}	schema.ErrorResponse
//	@Router		/api/federation/groups [post]
func CreateFederationGroupEndpoint(get FederationProvider) echo.HandlerFunc {
	return func(c echo.Context) error {
		f, ok := federationService(c, get)
		if !ok {
			return nil
		}
		var req FederationGroupRequest
		if !decodeRemoteBody(c, &req) {
			return nil
		}
		g, err := f.CreateGroup(c.Request().Context(), remotes.GroupRequest{Name: req.Name, Policy: req.Policy, Members: req.Members})
		if err != nil {
			return mapFederationError(c, "create", err)
		}
		return c.JSON(http.StatusCreated, g)
	}
}

// GetFederationGroupEndpoint returns one group.
//
//	@Summary	Get a federation group
//	@Tags		remotes
//	@Produce	json
//	@Param		name	path		string	true	"Group name"
//	@Success	200		{object}	remotes.Group
//	@Failure	404		{object}	schema.ErrorResponse
//	@Failure	503		{object}	schema.ErrorResponse
//	@Router		/api/federation/groups/{name} [get]
func GetFederationGroupEndpoint(get FederationProvider) echo.HandlerFunc {
	return func(c echo.Context) error {
		f, ok := federationService(c, get)
		if !ok {
			return nil
		}
		g, err := f.Group(c.Request().Context(), c.Param("name"))
		if err != nil {
			return mapFederationError(c, "get", err)
		}
		return c.JSON(http.StatusOK, g)
	}
}

// UpdateFederationGroupEndpoint changes the policy or the members.
//
//	@Summary	Update a federation group
//	@Description	An absent field is left as it is. A new member list keeps the drain flag of the members that stay; the probe, trip and recovery settings of the group file are kept. Admin only.
//	@Tags		remotes
//	@Accept		json
//	@Produce	json
//	@Param		name	path		string							true	"Group name"
//	@Param		request	body		FederationGroupUpdateRequest	true	"Fields to change"
//	@Success	200		{object}	remotes.Group
//	@Failure	400		{object}	schema.ErrorResponse
//	@Failure	404		{object}	schema.ErrorResponse
//	@Failure	413		{object}	schema.ErrorResponse
//	@Failure	503		{object}	schema.ErrorResponse
//	@Router		/api/federation/groups/{name} [patch]
func UpdateFederationGroupEndpoint(get FederationProvider) echo.HandlerFunc {
	return func(c echo.Context) error {
		f, ok := federationService(c, get)
		if !ok {
			return nil
		}
		var req FederationGroupUpdateRequest
		if !decodeRemoteBody(c, &req) {
			return nil
		}
		g, err := f.UpdateGroup(c.Request().Context(), c.Param("name"), remotes.GroupUpdate{Policy: req.Policy, Members: req.Members})
		if err != nil {
			return mapFederationError(c, "update", err)
		}
		return c.JSON(http.StatusOK, g)
	}
}

// DrainFederationMemberEndpoint drains a member or puts it back.
//
//	@Summary	Drain a member of a federation group
//	@Description	With drain true the member takes no new request; requests in flight finish and its health checks go on. With drain false it takes requests again. The last member that takes requests cannot be drained. Admin only.
//	@Tags		remotes
//	@Accept		json
//	@Produce	json
//	@Param		name	path		string					true	"Group name"
//	@Param		request	body		FederationDrainRequest	true	"Member and drain flag"
//	@Success	200		{object}	remotes.Group
//	@Failure	400		{object}	schema.ErrorResponse
//	@Failure	404		{object}	schema.ErrorResponse
//	@Failure	413		{object}	schema.ErrorResponse
//	@Failure	503		{object}	schema.ErrorResponse
//	@Router		/api/federation/groups/{name}/drain [post]
func DrainFederationMemberEndpoint(get FederationProvider) echo.HandlerFunc {
	return func(c echo.Context) error {
		f, ok := federationService(c, get)
		if !ok {
			return nil
		}
		var req FederationDrainRequest
		if !decodeRemoteBody(c, &req) {
			return nil
		}
		g, err := f.Drain(c.Request().Context(), c.Param("name"), req.Model, req.Drain)
		if err != nil {
			return mapFederationError(c, "drain", err)
		}
		return c.JSON(http.StatusOK, g)
	}
}

// DeleteFederationGroupEndpoint removes a group. Its members stay.
//
//	@Summary	Delete a federation group
//	@Description	Removes the group's model config. The members stay as they are. Admin only.
//	@Tags		remotes
//	@Param		name	path	string	true	"Group name"
//	@Success	204
//	@Failure	404	{object}	schema.ErrorResponse
//	@Failure	503	{object}	schema.ErrorResponse
//	@Router		/api/federation/groups/{name} [delete]
func DeleteFederationGroupEndpoint(get FederationProvider) echo.HandlerFunc {
	return func(c echo.Context) error {
		f, ok := federationService(c, get)
		if !ok {
			return nil
		}
		if err := f.DeleteGroup(c.Request().Context(), c.Param("name")); err != nil {
			return mapFederationError(c, "delete", err)
		}
		return c.NoContent(http.StatusNoContent)
	}
}
