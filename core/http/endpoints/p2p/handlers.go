package p2p

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/http/auth"
	"github.com/mudler/LocalAI/core/schema"
)

// maxBody bounds the body of every P2P route.
const maxBody = 64 << 10

// InviteList is the answer of GET /api/p2p/invites.
type InviteList struct {
	Invites []InviteView `json:"invites"`
}

// DeviceList is the answer of GET /api/p2p/devices.
type DeviceList struct {
	Devices []DeviceView `json:"devices"`
}

// RelayList is the answer of GET /api/p2p/relays.
type RelayList struct {
	Relays []Relay `json:"relays"`
}

// OKResponse is the answer of a route that has nothing else to say.
type OKResponse struct {
	OK bool `json:"ok"`
}

// fail writes err as an error response with the status it maps to.
func fail(c echo.Context, err error) error {
	code, msg, typ := http.StatusInternalServerError, "internal error", "p2p_error"
	var me *ModeError
	switch {
	case errors.As(err, &me):
		code, msg = http.StatusConflict, me.Error()
	case errors.Is(err, ErrInviteInvalid):
		// One answer for a wrong, used, canceled or expired code.
		code, msg = http.StatusGone, ErrInviteInvalid.Error()
	case errors.Is(err, ErrRateLimited):
		code, msg = http.StatusTooManyRequests, ErrRateLimited.Error()
	case errors.Is(err, ErrNotFound):
		code, msg = http.StatusNotFound, ErrNotFound.Error()
	case errors.Is(err, ErrInvalid):
		code, msg = http.StatusBadRequest, err.Error()
	case errors.Is(err, ErrConflict):
		code, msg = http.StatusConflict, err.Error()
	case errors.Is(err, ErrNeedsAccount):
		// The type lets a client tell this refusal from a wrong mode.
		code, msg, typ = http.StatusConflict, ErrNeedsAccount.Error(), "needs_account"
	case errors.Is(err, ErrUnavailable):
		code, msg = http.StatusServiceUnavailable, err.Error()
	default:
		xlog.Error("P2P API", "path", c.Path(), "error", err)
	}
	return c.JSON(code, schema.ErrorResponse{Error: &schema.APIError{Message: msg, Code: code, Type: typ}})
}

// bind reads a JSON body of at most maxBody bytes.
func bind(c echo.Context, v any) error {
	c.Request().Body = http.MaxBytesReader(c.Response(), c.Request().Body, maxBody)
	if err := c.Bind(v); err != nil {
		return fail(c, fmt.Errorf("%w: invalid request body", ErrInvalid))
	}
	return nil
}

func userID(c echo.Context) string {
	if u := auth.GetUser(c); u != nil {
		return u.ID
	}
	return ""
}

// StatusEndpoint reports the state of the P2P layer.
//
//	@Summary	P2P status
//	@Description	The mode in effect (off, connect or share), the peer ID and addresses of this server, its reachability, the relays that hold a reservation for it, whether it relays for its paired devices, and the number of pending invites and paired devices. Admin only.
//	@Tags		p2p
//	@Produce	json
//	@Success	200	{object}	Status
//	@Router		/api/p2p/status [get]
func StatusEndpoint(svc Service) echo.HandlerFunc {
	return func(c echo.Context) error {
		st, err := svc.Status(c.Request().Context())
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, st)
	}
}

// MintInviteEndpoint creates an invite.
//
//	@Summary	Create a P2P invite
//	@Description	Creates a one-time invite link. kind is device (P2P admission only; the person signs in as usual), remote (also issues an API key for the other server: limited to "models" when given, else every model this server lets a client key use, models added later too; "all_models": true asks for every model explicitly and is refused with a non-empty "models"), worker or split. A remote invite needs a signed-in user account: the API key set with LOCALAI_API_KEY gets 409. relays is "auto", "none" or a list of relay IDs. When a gated relay is named, a single-use relay pass is registered on it; "relay.reason" says why there is none. The link is shown once. Needs P2P mode share. Admin only.
//	@Tags		p2p
//	@Accept		json
//	@Produce	json
//	@Param		request	body		MintRequest	true	"Invite to create"
//	@Success	201		{object}	MintResult
//	@Failure	400		{object}	schema.ErrorResponse
//	@Failure	409		{object}	schema.ErrorResponse
//	@Router		/api/p2p/invites [post]
func MintInviteEndpoint(svc Service) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req MintRequest
		if err := bind(c, &req); err != nil {
			return err
		}
		res, err := svc.MintInvite(c.Request().Context(), userID(c), req)
		if err != nil {
			return fail(c, err)
		}
		c.Response().Header().Set("Cache-Control", "no-store")
		return c.JSON(http.StatusCreated, res)
	}
}

// ListInvitesEndpoint lists the invites.
//
//	@Summary	List P2P invites
//	@Description	Lists the invites with their state: pending, used, expired or canceled. The links are not part of the answer. Needs P2P mode share. Admin only.
//	@Tags		p2p
//	@Produce	json
//	@Success	200	{object}	InviteList
//	@Failure	409	{object}	schema.ErrorResponse
//	@Router		/api/p2p/invites [get]
func ListInvitesEndpoint(svc Service) echo.HandlerFunc {
	return func(c echo.Context) error {
		invs, err := svc.ListInvites(c.Request().Context())
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, InviteList{Invites: invs})
	}
}

// CancelInviteEndpoint cancels a pending invite.
//
//	@Summary	Cancel a P2P invite
//	@Description	Cancels a pending invite and drops its relay pass. Needs P2P mode share. Admin only.
//	@Tags		p2p
//	@Produce	json
//	@Param		id	path		string	true	"Invite ID"
//	@Success	200	{object}	OKResponse
//	@Failure	404	{object}	schema.ErrorResponse
//	@Failure	409	{object}	schema.ErrorResponse
//	@Router		/api/p2p/invites/{id} [delete]
func CancelInviteEndpoint(svc Service) echo.HandlerFunc {
	return func(c echo.Context) error {
		if err := svc.CancelInvite(c.Request().Context(), c.Param("id")); err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, OKResponse{OK: true})
	}
}

// ListDevicesEndpoint lists the paired devices.
//
//	@Summary	List P2P devices
//	@Description	Lists the paired devices, revoked ones included, with how each one reaches the server now (path kind direct, hole_punched or relayed, the relay and its limits). Needs P2P mode share. Admin only.
//	@Tags		p2p
//	@Produce	json
//	@Success	200	{object}	DeviceList
//	@Failure	409	{object}	schema.ErrorResponse
//	@Router		/api/p2p/devices [get]
func ListDevicesEndpoint(svc Service) echo.HandlerFunc {
	return func(c echo.Context) error {
		devs, err := svc.ListDevices(c.Request().Context())
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, DeviceList{Devices: devs})
	}
}

// RenameDeviceEndpoint renames a device.
//
//	@Summary	Rename a P2P device
//	@Tags		p2p
//	@Accept		json
//	@Produce	json
//	@Param		id		path		string				true	"Device ID"
//	@Param		request	body		RenameDeviceRequest	true	"New label"
//	@Success	200		{object}	OKResponse
//	@Failure	400		{object}	schema.ErrorResponse
//	@Failure	404		{object}	schema.ErrorResponse
//	@Failure	409		{object}	schema.ErrorResponse
//	@Router		/api/p2p/devices/{id} [patch]
func RenameDeviceEndpoint(svc Service) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req RenameDeviceRequest
		if err := bind(c, &req); err != nil {
			return err
		}
		if err := svc.RenameDevice(c.Request().Context(), c.Param("id"), strings.TrimSpace(req.Label)); err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, OKResponse{OK: true})
	}
}

// RevokeDeviceEndpoint revokes a device.
//
//	@Summary	Revoke a P2P device
//	@Description	Revokes a device: it is removed from the allowlist, its connections are closed, every relay is told to drop it, and the API key of a remote device is revoked. Needs P2P mode share. Admin only.
//	@Tags		p2p
//	@Produce	json
//	@Param		id	path		string	true	"Device ID"
//	@Success	200	{object}	RevokeResult
//	@Failure	404	{object}	schema.ErrorResponse
//	@Failure	409	{object}	schema.ErrorResponse
//	@Router		/api/p2p/devices/{id} [delete]
func RevokeDeviceEndpoint(svc Service) echo.HandlerFunc {
	return func(c echo.Context) error {
		if err := svc.RevokeDevice(c.Request().Context(), c.Param("id"), userID(c)); err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, RevokeResult{Revoked: true})
	}
}

// ListRelaysEndpoint lists the relays.
//
//	@Summary	List P2P relays
//	@Description	Lists the relays this server knows, with their mode (gated, open, unclaimed) and state (in_sync, out_of_sync, open, unclaimed, not_admin, denied, unreachable, checking). Needs P2P mode share. Admin only.
//	@Tags		p2p
//	@Produce	json
//	@Success	200	{object}	RelayList
//	@Failure	409	{object}	schema.ErrorResponse
//	@Router		/api/p2p/relays [get]
func ListRelaysEndpoint(svc Service) echo.HandlerFunc {
	return func(c echo.Context) error {
		rs, err := svc.ListRelays(c.Request().Context())
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, RelayList{Relays: rs})
	}
}

// AddRelayEndpoint adds a relay.
//
//	@Summary	Add a P2P relay
//	@Description	Adds a relay by its address, which must end in /p2p/<relay peer ID>, and checks it. claim_code claims a relay that has no admin yet, with the code it printed at start. Needs P2P mode share. Admin only.
//	@Tags		p2p
//	@Accept		json
//	@Produce	json
//	@Param		request	body		AddRelayRequest	true	"Relay to add"
//	@Success	201		{object}	Relay
//	@Failure	400		{object}	schema.ErrorResponse
//	@Failure	409		{object}	schema.ErrorResponse
//	@Failure	503		{object}	schema.ErrorResponse
//	@Router		/api/p2p/relays [post]
func AddRelayEndpoint(svc Service) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req AddRelayRequest
		if err := bind(c, &req); err != nil {
			return err
		}
		r, err := svc.AddRelay(c.Request().Context(), strings.TrimSpace(req.Addr), strings.TrimSpace(req.Label), strings.TrimSpace(req.ClaimCode))
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusCreated, r)
	}
}

// RemoveRelayEndpoint removes a relay.
//
//	@Summary	Remove a P2P relay
//	@Tags		p2p
//	@Produce	json
//	@Param		id	path		string	true	"Relay ID"
//	@Success	200	{object}	OKResponse
//	@Failure	404	{object}	schema.ErrorResponse
//	@Failure	409	{object}	schema.ErrorResponse
//	@Failure	503	{object}	schema.ErrorResponse
//	@Router		/api/p2p/relays/{id} [delete]
func RemoveRelayEndpoint(svc Service) echo.HandlerFunc {
	return func(c echo.Context) error {
		if err := svc.RemoveRelay(c.Request().Context(), c.Param("id")); err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, OKResponse{OK: true})
	}
}

// CheckRelayEndpoint checks a relay.
//
//	@Summary	Check a P2P relay
//	@Description	Connects to the relay, reserves a slot, reads whether this server is an admin, tests a circuit and compares the paired set. Needs P2P mode share. Admin only.
//	@Tags		p2p
//	@Produce	json
//	@Param		id	path		string	true	"Relay ID"
//	@Success	200	{object}	RelayCheck
//	@Failure	404	{object}	schema.ErrorResponse
//	@Failure	409	{object}	schema.ErrorResponse
//	@Failure	503	{object}	schema.ErrorResponse
//	@Router		/api/p2p/relays/{id}/check [post]
func CheckRelayEndpoint(svc Service) echo.HandlerFunc {
	return func(c echo.Context) error {
		chk, err := svc.CheckRelay(c.Request().Context(), c.Param("id"))
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, chk)
	}
}

// SyncRelayEndpoint pushes the paired set to a relay.
//
//	@Summary	Sync a P2P relay
//	@Description	Pushes the full set of paired devices to the relay now. Needs P2P mode share. Admin only.
//	@Tags		p2p
//	@Produce	json
//	@Param		id	path		string	true	"Relay ID"
//	@Success	200	{object}	Relay
//	@Failure	404	{object}	schema.ErrorResponse
//	@Failure	409	{object}	schema.ErrorResponse
//	@Failure	503	{object}	schema.ErrorResponse
//	@Router		/api/p2p/relays/{id}/sync [post]
func SyncRelayEndpoint(svc Service) echo.HandlerFunc {
	return func(c echo.Context) error {
		r, err := svc.SyncRelay(c.Request().Context(), c.Param("id"))
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, r)
	}
}

// ClaimRelayEndpoint claims a relay.
//
//	@Summary	Claim a P2P relay
//	@Description	Makes this server an admin of a relay that has none, with the one-time code the relay printed at start. Needs P2P mode share. Admin only.
//	@Tags		p2p
//	@Accept		json
//	@Produce	json
//	@Param		id		path		string				true	"Relay ID"
//	@Param		request	body		ClaimRelayRequest	true	"Claim code"
//	@Success	200		{object}	Relay
//	@Failure	400		{object}	schema.ErrorResponse
//	@Failure	404		{object}	schema.ErrorResponse
//	@Failure	409		{object}	schema.ErrorResponse
//	@Failure	503		{object}	schema.ErrorResponse
//	@Router		/api/p2p/relays/{id}/claim [post]
func ClaimRelayEndpoint(svc Service) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req ClaimRelayRequest
		if err := bind(c, &req); err != nil {
			return err
		}
		if strings.TrimSpace(req.Code) == "" {
			return fail(c, fmt.Errorf("%w: code is required", ErrInvalid))
		}
		r, err := svc.ClaimRelay(c.Request().Context(), c.Param("id"), strings.TrimSpace(req.Code))
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, r)
	}
}

// RelayLimitsEndpoint reads the limits of a relay.
//
//	@Summary	Read the limits of a P2P relay
//	@Description	Reads the limits the relay runs with now: the data, the time and the bandwidth of one circuit (zero means no limit). ceiling is the most a change may set for each limit (zero means no ceiling); the operator of the relay sets it with the relay's flags. can_change says whether this server is an admin of the relay and may change them. Needs P2P mode share. Admin only.
//	@Tags		p2p
//	@Produce	json
//	@Param		id	path		string	true	"Relay ID"
//	@Success	200	{object}	RelayLimitsInfo
//	@Failure	404	{object}	schema.ErrorResponse
//	@Failure	409	{object}	schema.ErrorResponse
//	@Failure	503	{object}	schema.ErrorResponse
//	@Router		/api/p2p/relays/{id}/limits [get]
func RelayLimitsEndpoint(svc Service) echo.HandlerFunc {
	return func(c echo.Context) error {
		info, err := svc.RelayLimits(c.Request().Context(), c.Param("id"))
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, info)
	}
}

// SetRelayLimitsEndpoint changes the limits of a relay.
//
//	@Summary	Change the limits of a P2P relay
//	@Description	Changes the data, time and bandwidth limits of one circuit on a relay this server administers; a field left out stays as it is and zero removes that limit. The relay saves the new limits, so they stay over a restart of the relay. A new bandwidth limit applies to the next circuit at once. A new data or time limit makes the relay restart its circuit service: its reservations are dropped, running circuits keep their old limits until they end, and the paired devices reserve again within moments ("restarted" is true). A value above the ceiling of the relay, or zero for a limit that has a ceiling, is refused with 400 and an error that names the ceiling; nothing is clamped. Answers 409 when this server is not an admin of the relay or the relay is open. Needs P2P mode share. Admin only.
//	@Tags		p2p
//	@Accept		json
//	@Produce	json
//	@Param		id		path		string					true	"Relay ID"
//	@Param		request	body		SetRelayLimitsRequest	true	"Limits to change"
//	@Success	200		{object}	RelayLimitsInfo
//	@Failure	400		{object}	schema.ErrorResponse
//	@Failure	404		{object}	schema.ErrorResponse
//	@Failure	409		{object}	schema.ErrorResponse
//	@Failure	503		{object}	schema.ErrorResponse
//	@Router		/api/p2p/relays/{id}/limits [put]
func SetRelayLimitsEndpoint(svc Service) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req SetRelayLimitsRequest
		if err := bind(c, &req); err != nil {
			return err
		}
		if req.CircuitBytes == nil && req.CircuitSeconds == nil && req.RateBPS == nil {
			return fail(c, fmt.Errorf("%w: give circuit_bytes, circuit_seconds or rate_bps", ErrInvalid))
		}
		info, err := svc.SetRelayLimits(c.Request().Context(), c.Param("id"), req)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, info)
	}
}

// RegenerateIdentityEndpoint makes a new peer identity.
//
//	@Summary	Regenerate the P2P identity
//	@Description	Makes a new peer identity for this server and restarts P2P with it. The old key is kept next to the new one as a backup. Every paired device and remote knows the old peer ID, so each must pair again. The body must be {"confirm": true}. Needs P2P mode connect or share. Admin only.
//	@Tags		p2p
//	@Accept		json
//	@Produce	json
//	@Param		request	body		RegenerateIdentityRequest	true	"Confirmation"
//	@Success	200		{object}	Status
//	@Failure	400		{object}	schema.ErrorResponse
//	@Failure	409		{object}	schema.ErrorResponse
//	@Router		/api/p2p/identity/regenerate [post]
func RegenerateIdentityEndpoint(svc Service) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req RegenerateIdentityRequest
		if err := bind(c, &req); err != nil {
			return err
		}
		if !req.Confirm {
			return fail(c, fmt.Errorf("%w: confirm must be true: a new identity unpairs every device and remote", ErrInvalid))
		}
		st, err := svc.RegenerateIdentity(c.Request().Context())
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, st)
	}
}

// RedeemEndpoint redeems an invite. It is served only over P2P: the caller is
// the peer the connection authenticated, and a request that did not arrive
// over P2P gets 404, like the server's normal HTTP listener does for this
// path.
//
//	@Summary	Redeem a P2P invite (over P2P only)
//	@Description	Served only on the P2P protocol /localai/http/1; the normal HTTP listener answers 404. The caller's peer ID comes from the connection. A wrong, used, canceled or expired code all get the same 410 answer. After 5 wrong codes from one peer in an hour, or too many from all peers, the answer is 429. The answer carries the confirmation phrase to compare on both sides, and for a remote invite the API key (shown once).
//	@Tags		p2p
//	@Accept		json
//	@Produce	json
//	@Param		request	body		RedeemRequest	true	"Code and device name"
//	@Success	200		{object}	RedeemResult
//	@Failure	400		{object}	schema.ErrorResponse
//	@Failure	404		{object}	schema.ErrorResponse
//	@Failure	409		{object}	schema.ErrorResponse
//	@Failure	410		{object}	schema.ErrorResponse
//	@Failure	429		{object}	schema.ErrorResponse
//	@Router		/api/p2p/redeem [post]
func RedeemEndpoint(svc Service) echo.HandlerFunc {
	return func(c echo.Context) error {
		from, ok := PeerFrom(c.Request().Context())
		if !ok {
			return fail(c, ErrNotFound)
		}
		var req RedeemRequest
		if err := bind(c, &req); err != nil {
			return err
		}
		res, err := svc.Redeem(c.Request().Context(), from, req)
		if err != nil {
			return fail(c, err)
		}
		c.Response().Header().Set("Cache-Control", "no-store")
		return c.JSON(http.StatusOK, res)
	}
}
