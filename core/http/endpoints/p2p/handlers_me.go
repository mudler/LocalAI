package p2p

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
)

// The routes of a signed-in user who pairs their own devices. Each needs the
// feature p2p_pair, which the route feature registry enforces; a client key
// is refused. The handlers pass the ID of the user, and the service answers
// 404 for an invite or a device of another user.

// MyPairingEndpoint says whether the user can pair a device now.
//
//	@Summary	My P2P pairing
//	@Description	Whether a device can pair now (available is true in P2P mode share; reason not_share otherwise), the limits of the user (max_devices, max_pending, max_ttl_seconds, max_mints_per_hour) and how many devices and pending invites the user has. Needs the feature p2p_pair and a user account (the API key of LOCALAI_API_KEY gets 409 with error type needs_account).
//	@Tags		p2p
//	@Produce	json
//	@Success	200	{object}	MyPairing
//	@Failure	403	{object}	schema.ErrorResponse
//	@Failure	409	{object}	schema.ErrorResponse
//	@Router		/api/p2p/me [get]
func MyPairingEndpoint(svc UserService) echo.HandlerFunc {
	return func(c echo.Context) error {
		res, err := svc.MyPairing(c.Request().Context(), userID(c))
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, res)
	}
}

// MintMyInviteEndpoint mints an invite for a device of the user.
//
//	@Summary	Pair one of my devices
//	@Description	Creates a one-time invite link for a device that signs in as the user once it pairs: the redeem gives it a credential for POST /api/p2p/device/session. The body takes label and ttl_seconds only; the kind (device, with sign-in), the relays, open relays and local addresses come from the setting p2p_user_pairing, and any other field is ignored. ttl_seconds above max_ttl_seconds gets 400. 429 with error type limit_reached when the user has max_pending pending invites or max_devices devices, minted 10 invites in the last hour, or the server has 20 pending sign-in invites. The link is shown once. Needs P2P mode share, the feature p2p_pair and a user account.
//	@Tags		p2p
//	@Accept		json
//	@Produce	json
//	@Param		request	body		MyMintRequest	true	"Label and life of the invite"
//	@Success	201		{object}	MintResult
//	@Failure	400		{object}	schema.ErrorResponse
//	@Failure	403		{object}	schema.ErrorResponse
//	@Failure	409		{object}	schema.ErrorResponse
//	@Failure	429		{object}	schema.ErrorResponse
//	@Router		/api/p2p/me/invites [post]
func MintMyInviteEndpoint(svc UserService) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req MyMintRequest
		if err := bind(c, &req); err != nil {
			return err
		}
		req.Label = strings.TrimSpace(req.Label)
		res, err := svc.MintMyInvite(c.Request().Context(), userID(c), req)
		if err != nil {
			return fail(c, err)
		}
		c.Response().Header().Set("Cache-Control", "no-store")
		return c.JSON(http.StatusCreated, res)
	}
}

// ListMyInvitesEndpoint lists the pending invites of the user.
//
//	@Summary	List my pending P2P invites
//	@Description	Lists the pending invites the user minted, without the links. Needs P2P mode share, the feature p2p_pair and a user account.
//	@Tags		p2p
//	@Produce	json
//	@Success	200	{object}	InviteList
//	@Failure	403	{object}	schema.ErrorResponse
//	@Failure	409	{object}	schema.ErrorResponse
//	@Router		/api/p2p/me/invites [get]
func ListMyInvitesEndpoint(svc UserService) echo.HandlerFunc {
	return func(c echo.Context) error {
		invs, err := svc.ListMyInvites(c.Request().Context(), userID(c))
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, InviteList{Invites: invs})
	}
}

// CancelMyInviteEndpoint cancels a pending invite of the user.
//
//	@Summary	Cancel one of my P2P invites
//	@Description	Cancels a pending invite of the user and drops its relay pass. An invite of another user gets 404. Needs P2P mode share, the feature p2p_pair and a user account.
//	@Tags		p2p
//	@Produce	json
//	@Param		id	path		string	true	"Invite ID"
//	@Success	200	{object}	OKResponse
//	@Failure	403	{object}	schema.ErrorResponse
//	@Failure	404	{object}	schema.ErrorResponse
//	@Failure	409	{object}	schema.ErrorResponse
//	@Router		/api/p2p/me/invites/{id} [delete]
func CancelMyInviteEndpoint(svc UserService) echo.HandlerFunc {
	return func(c echo.Context) error {
		if err := svc.CancelMyInvite(c.Request().Context(), userID(c), c.Param("id")); err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, OKResponse{OK: true})
	}
}

// ListMyDevicesEndpoint lists the devices of the user.
//
//	@Summary	List my P2P devices
//	@Description	Lists the devices the user paired, revoked ones included, with the path each one uses now and when it was last seen. Needs P2P mode share, the feature p2p_pair and a user account.
//	@Tags		p2p
//	@Produce	json
//	@Success	200	{object}	DeviceList
//	@Failure	403	{object}	schema.ErrorResponse
//	@Failure	409	{object}	schema.ErrorResponse
//	@Router		/api/p2p/me/devices [get]
func ListMyDevicesEndpoint(svc UserService) echo.HandlerFunc {
	return func(c echo.Context) error {
		devs, err := svc.ListMyDevices(c.Request().Context(), userID(c))
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, DeviceList{Devices: devs})
	}
}

// RenameMyDeviceEndpoint renames a device of the user.
//
//	@Summary	Rename one of my P2P devices
//	@Description	A device of another user gets 404. Needs P2P mode share, the feature p2p_pair and a user account.
//	@Tags		p2p
//	@Accept		json
//	@Produce	json
//	@Param		id		path		string				true	"Device ID"
//	@Param		request	body		RenameDeviceRequest	true	"New label"
//	@Success	200		{object}	OKResponse
//	@Failure	400		{object}	schema.ErrorResponse
//	@Failure	403		{object}	schema.ErrorResponse
//	@Failure	404		{object}	schema.ErrorResponse
//	@Failure	409		{object}	schema.ErrorResponse
//	@Router		/api/p2p/me/devices/{id} [patch]
func RenameMyDeviceEndpoint(svc UserService) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req RenameDeviceRequest
		if err := bind(c, &req); err != nil {
			return err
		}
		if err := svc.RenameMyDevice(c.Request().Context(), userID(c), c.Param("id"), strings.TrimSpace(req.Label)); err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, OKResponse{OK: true})
	}
}

// RevokeMyDeviceEndpoint revokes a device of the user.
//
//	@Summary	Revoke one of my P2P devices
//	@Description	Revokes a device of the user: it leaves the allowlist, its connections close, the relays drop it and the web sessions it opened end. A device of another user gets 404. Needs P2P mode share, the feature p2p_pair and a user account.
//	@Tags		p2p
//	@Produce	json
//	@Param		id	path		string	true	"Device ID"
//	@Success	200	{object}	RevokeResult
//	@Failure	403	{object}	schema.ErrorResponse
//	@Failure	404	{object}	schema.ErrorResponse
//	@Failure	409	{object}	schema.ErrorResponse
//	@Router		/api/p2p/me/devices/{id} [delete]
func RevokeMyDeviceEndpoint(svc UserService) echo.HandlerFunc {
	return func(c echo.Context) error {
		if err := svc.RevokeMyDevice(c.Request().Context(), userID(c), c.Param("id")); err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, RevokeResult{Revoked: true})
	}
}

// UserPairingEndpoint reads the setting p2p_user_pairing.
//
//	@Summary	Read the P2P user pairing setting
//	@Description	What an invite that a user mints for their own device carries (relays auto or none, allow_open_relay, omit_lan_addrs) and the limits of each user (max_devices, max_pending, max_ttl_seconds). It is kept in the auth database, so every frontend uses the same setting. 409 without authentication. Admin only.
//	@Tags		p2p
//	@Produce	json
//	@Success	200	{object}	UserPairingSettings
//	@Failure	409	{object}	schema.ErrorResponse
//	@Router		/api/p2p/user-pairing [get]
func UserPairingEndpoint(svc Service) echo.HandlerFunc {
	return func(c echo.Context) error {
		res, err := svc.UserPairing(c.Request().Context())
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, res)
	}
}

// SetUserPairingEndpoint changes the setting p2p_user_pairing.
//
//	@Summary	Change the P2P user pairing setting
//	@Description	A field left out stays as it is. relays is auto or none; max_devices is 1 to 100, max_pending 1 to 20 and max_ttl_seconds 60 to 86400; any other value gets 400. A change applies to the next invite and the next redeem; devices and invites that exist stay. 409 without authentication. Admin only.
//	@Tags		p2p
//	@Accept		json
//	@Produce	json
//	@Param		request	body		SetUserPairingRequest	true	"Fields to change"
//	@Success	200		{object}	UserPairingSettings
//	@Failure	400		{object}	schema.ErrorResponse
//	@Failure	409		{object}	schema.ErrorResponse
//	@Router		/api/p2p/user-pairing [put]
func SetUserPairingEndpoint(svc Service) echo.HandlerFunc {
	return func(c echo.Context) error {
		var req SetUserPairingRequest
		if err := bind(c, &req); err != nil {
			return err
		}
		res, err := svc.SetUserPairing(c.Request().Context(), userID(c), req)
		if err != nil {
			return fail(c, err)
		}
		return c.JSON(http.StatusOK, res)
	}
}
