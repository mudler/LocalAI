package localai

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/services/failover"
)

type FailoverChainsResponse struct {
	Chains []failover.ChainStatus `json:"chains"`
}

type FailoverPinRequest struct {
	Target string `json:"target"`
}

func failoverError(c echo.Context, code int, msg string) error {
	return c.JSON(code, schema.ErrorResponse{Error: &schema.APIError{Message: msg, Code: code, Type: "failover_error"}})
}

// ListFailoverChainsEndpoint lists failover chains and the health of their targets
//
//	@Summary	List failover chains and the health of their targets
//	@Tags		failover
//	@Produce	json
//	@Success	200	{object}	FailoverChainsResponse
//	@Router		/api/failover [get]
func ListFailoverChainsEndpoint(fm *failover.Manager) echo.HandlerFunc {
	return func(c echo.Context) error {
		return c.JSON(http.StatusOK, FailoverChainsResponse{Chains: fm.Status()})
	}
}

// GetFailoverChainEndpoint returns one failover chain
//
//	@Summary	Get one failover chain
//	@Tags		failover
//	@Produce	json
//	@Param		chain	path		string	true	"Chain name"
//	@Success	200		{object}	failover.ChainStatus
//	@Failure	404		{object}	schema.ErrorResponse
//	@Router		/api/failover/{chain} [get]
func GetFailoverChainEndpoint(fm *failover.Manager) echo.HandlerFunc {
	return func(c echo.Context) error {
		st, ok := fm.ChainStatus(c.Param("chain"))
		if !ok {
			return failoverError(c, http.StatusNotFound, fmt.Sprintf("failover chain %q not found", c.Param("chain")))
		}
		return c.JSON(http.StatusOK, st)
	}
}

// PinFailoverTargetEndpoint forces a chain to one target
//
//	@Summary	Pin a failover chain to one target
//	@Tags		failover
//	@Accept		json
//	@Produce	json
//	@Param		chain	path		string				true	"Chain name"
//	@Param		request	body		FailoverPinRequest	true	"Target to pin"
//	@Success	200		{object}	failover.ChainStatus
//	@Failure	400		{object}	schema.ErrorResponse
//	@Failure	404		{object}	schema.ErrorResponse
//	@Router		/api/failover/{chain}/pin [post]
func PinFailoverTargetEndpoint(fm *failover.Manager) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req FailoverPinRequest
		if err := c.Bind(&req); err != nil || req.Target == "" {
			return failoverError(c, http.StatusBadRequest, "request body must set \"target\"")
		}
		chain := c.Param("chain")
		if err := fm.Pin(chain, req.Target); err != nil {
			return pinError(c, err)
		}
		st, _ := fm.ChainStatus(chain)
		return c.JSON(http.StatusOK, st)
	}
}

// UnpinFailoverTargetEndpoint removes a pin
//
//	@Summary	Remove the pin from a failover chain
//	@Tags		failover
//	@Produce	json
//	@Param		chain	path		string	true	"Chain name"
//	@Success	200		{object}	failover.ChainStatus
//	@Failure	404		{object}	schema.ErrorResponse
//	@Router		/api/failover/{chain}/pin [delete]
func UnpinFailoverTargetEndpoint(fm *failover.Manager) echo.HandlerFunc {
	return func(c echo.Context) error {
		chain := c.Param("chain")
		if err := fm.Unpin(chain); err != nil {
			return pinError(c, err)
		}
		st, _ := fm.ChainStatus(chain)
		return c.JSON(http.StatusOK, st)
	}
}

func pinError(c echo.Context, err error) error {
	switch {
	case errors.Is(err, failover.ErrChainNotFound):
		return failoverError(c, http.StatusNotFound, err.Error())
	case errors.Is(err, failover.ErrTargetNotInChain):
		return failoverError(c, http.StatusBadRequest, err.Error())
	}
	return failoverError(c, http.StatusInternalServerError, err.Error())
}

// FailoverEventsEndpoint streams failover events
//
//	@Summary	Stream failover events (server-sent events)
//	@Description	The first event is "snapshot" with the full state, then "chain.switched" and "target.state" events.
//	@Tags		failover
//	@Produce	text/event-stream
//	@Success	200
//	@Router		/api/failover/events [get]
func FailoverEventsEndpoint(fm *failover.Manager) echo.HandlerFunc {
	return func(c echo.Context) error {
		// Subscribe before the snapshot so no event falls between the two.
		events, cancel := fm.Subscribe(64)
		defer cancel()
		w := c.Response()
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		send := func(name string, v any) error {
			data, err := json.Marshal(v)
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data); err != nil {
				return err
			}
			w.Flush()
			return nil
		}
		if err := send("snapshot", FailoverChainsResponse{Chains: fm.Status()}); err != nil {
			return nil
		}
		keepalive := time.NewTicker(15 * time.Second)
		defer keepalive.Stop()
		for {
			select {
			case <-c.Request().Context().Done():
				return nil
			case <-keepalive.C:
				if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
					return nil
				}
				w.Flush()
			case ev, ok := <-events:
				if !ok {
					return nil
				}
				if err := send(string(ev.Type), ev); err != nil {
					return nil
				}
			}
		}
	}
}
