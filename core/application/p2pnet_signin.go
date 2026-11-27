package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	p2papi "github.com/mudler/LocalAI/core/http/endpoints/p2p"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/LocalAI/core/services/p2pnet"
	"github.com/mudler/LocalAI/core/services/p2ppair"
)

// deviceSessionPath exchanges the credential of a paired sign-in device for a
// web session of its owner. It is served over P2P only, to paired peers.
const deviceSessionPath = "/api/p2p/device/session"

var _ p2papi.UserService = p2pService{}

// P2PUsers returns the P2P layer as the handlers of signed-in users and of
// device sign-in see it.
func (a *Application) P2PUsers() p2papi.UserService { return p2pService{n: a.p2p()} }

// DeviceSession checks, in this order, that the peer is an active sign-in
// device, that the credential is its own, that the owner is active and that
// the owner still has p2p_pair, and then opens a session of the owner that
// carries the device. Every refusal is ErrSignInRefused. A wrong credential
// counts against the sign-in limiter of the peer (the peer alone, not its
// source, and not the redeem limiter); a right one is taken back, so an app
// that signs in again and again is never locked out.
func (s p2pService) DeviceSession(ctx context.Context, from, credential string) (p2papi.DeviceSession, error) {
	rt, err := s.share()
	if err != nil {
		return p2papi.DeviceSession{}, err
	}
	p, err := p2pnet.DecodePeerID(from)
	if err != nil {
		return p2papi.DeviceSession{}, fmt.Errorf("%w: unknown peer", p2papi.ErrInvalid)
	}
	db := s.n.app.AuthDB()
	if db == nil {
		return p2papi.DeviceSession{}, p2papi.ErrSignInRefused
	}
	ok, refund := rt.signInLimit.Attempt(p, "")
	if !ok {
		return p2papi.DeviceSession{}, p2papi.ErrRateLimited
	}
	dev, err := rt.pair.VerifyDeviceCredential(ctx, p, credential)
	if err != nil {
		if errors.Is(err, p2ppair.ErrCredentialInvalid) {
			return p2papi.DeviceSession{}, p2papi.ErrSignInRefused
		}
		refund()
		return p2papi.DeviceSession{}, err
	}
	refund()
	var u auth.User
	if err := db.WithContext(ctx).Where("id = ?", dev.OwnerID).Take(&u).Error; err != nil {
		return p2papi.DeviceSession{}, p2papi.ErrSignInRefused
	}
	if u.Status != auth.StatusActive || !auth.HasFeatureAccess(db, &u, auth.FeatureP2PPair) {
		return p2papi.DeviceSession{}, p2papi.ErrSignInRefused
	}
	beforeDeviceSessionCreate()
	secret := s.n.app.ApplicationConfig().Auth.APIKeyHMACSecret
	token, err := auth.CreateDeviceSession(db.WithContext(ctx), u.ID, dev.ID, secret)
	if err != nil {
		return p2papi.DeviceSession{}, err
	}
	// A revoke can land between the credential check and the insert, and
	// then its delete of the sessions ran before this one existed. Check the
	// device again and take the session back if it is gone. The session
	// would not work anyway (deviceSessionCheck), but the row must not stay.
	if !rt.deviceSessionCheck(p)(ctx, dev.ID) {
		if err := auth.DeleteSession(db.WithContext(ctx), token, secret); err != nil {
			xlog.Warn("P2P: could not drop the session of a device revoked during sign-in", "device", dev.ID, "error", err)
		}
		return p2papi.DeviceSession{}, p2papi.ErrSignInRefused
	}
	xlog.Info("P2P: a paired device signed in", "device", dev.ID, "user", u.ID)
	return p2papi.DeviceSession{Token: token, User: p2papi.SessionUser{ID: u.ID, Name: u.Name, Email: u.Email, Role: u.Role}}, nil
}

// deviceSessionCheck binds the web sessions that paired devices opened to
// the device: a request over P2P from peer p may use such a session only
// while its device is p's, is not revoked and still signs in. The device is
// read from the pairing store by its ID (the primary key, so one indexed
// lookup in the shared auth database), which every frontend sees at once
// after a revoke.
func (rt *p2pRuntime) deviceSessionCheck(p p2pnet.ID) auth.DeviceSessionCheck {
	return func(ctx context.Context, deviceID string) bool {
		d, err := rt.pair.GetDevice(ctx, deviceID)
		return err == nil && d.RevokedAt == nil && d.SignIn && d.PeerID == p.String()
	}
}

// beforeDeviceSessionCreate runs between the credential check and the insert
// of the session; tests use it to land a revoke in that window.
var beforeDeviceSessionCreate = func() {}

// revokeDevice is the one revoke of a device for every caller: the store row
// (and the client key of a remote), the web sessions the device opened, the
// gate and the open connections, and the relays. The caller reloads and tells
// the other frontends with changed.
func (rt *p2pRuntime) revokeDevice(ctx context.Context, dev p2ppair.Device, by string) error {
	err := rt.pair.Revoke(ctx, dev.ID, by)
	if errors.Is(err, p2ppair.ErrNotFound) {
		return err
	}
	// The row is revoked even when the client key could not be, so the
	// sessions and the connections go in every case. A failure to end the
	// sessions is returned: the caller must not report a clean revoke.
	serr := rt.net.app.deleteDeviceSessions(dev.ID)
	if p, perr := p2pnet.DecodePeerID(dev.PeerID); perr == nil {
		rt.host.Revoke(p)
		if rt.relays != nil {
			rt.relays.PeerRemoved(p.String())
		}
	}
	return errors.Join(err, serr)
}

// deleteDeviceSessions ends the web sessions of a device. The sessions are
// useless once the device is revoked (see deviceSessionCheck), but the rows
// must go, and a failure is an error of the revoke.
func (a *Application) deleteDeviceSessions(deviceID string) error {
	db := a.AuthDB()
	if db == nil {
		return nil
	}
	if err := auth.DeleteDeviceSessions(db, deviceID); err != nil {
		xlog.Warn("P2P: could not end the sessions of a revoked device", "device", deviceID, "error", err)
		return fmt.Errorf("p2p: the device is revoked but its sessions could not be ended: %w", err)
	}
	return nil
}

// RevokeUserDevices revokes the active sign-in devices of a user, as the
// administrator disables or deletes the user. Devices without sign-in stay:
// they only admit a peer to the network, and the ones an administrator paired
// for the instance (workers, remotes, laptops) must not go with the account
// that happened to mint them. Without a running host in mode share the store
// rows are revoked and the sessions ended; the gate of every frontend follows
// at its next reload, which also leaves out the sign-in devices of an owner
// who is not active.
func (a *Application) RevokeUserDevices(ctx context.Context, userID, by string) error {
	if userID == "" || a.AuthDB() == nil {
		return nil
	}
	if rt, mode := a.p2p().runtime(); rt != nil && mode == config.P2PModeShare {
		devs, err := rt.pair.ListDevicesByOwner(ctx, userID)
		if err != nil {
			return err
		}
		var errs []error
		n := 0
		for _, d := range devs {
			if d.RevokedAt != nil || !d.SignIn {
				continue
			}
			n++
			if err := rt.revokeDevice(ctx, d, by); err != nil && !errors.Is(err, p2ppair.ErrNotFound) {
				errs = append(errs, err)
			}
		}
		if n > 0 {
			rt.changed("revoke")
		}
		return errors.Join(errs...)
	}
	store, err := a.p2pPairStore()
	if err != nil {
		return err
	}
	devs, err := store.ListDevicesByOwner(ctx, userID)
	if err != nil {
		return err
	}
	var errs []error
	n := 0
	for _, d := range devs {
		if d.RevokedAt != nil || !d.SignIn {
			continue
		}
		n++
		if _, err := store.RevokeDevice(ctx, d.ID, by, time.Now()); err != nil && !errors.Is(err, p2ppair.ErrNotFound) {
			errs = append(errs, err)
			continue
		}
		if err := a.deleteDeviceSessions(d.ID); err != nil {
			errs = append(errs, err)
		}
	}
	if nc := a.p2pBroadcaster(); nc != nil && n > 0 {
		if err := nc.Publish(messaging.SubjectCacheInvalidateP2PPeers, messaging.CacheInvalidateEvent{Op: "revoke"}); err != nil {
			xlog.Warn("P2P: could not broadcast a pairing change", "error", err)
		}
	}
	return errors.Join(errs...)
}

// deviceOwner is what the gate needs to know about the owner of a device.
type deviceOwner struct {
	active bool
	admin  bool
}

// deviceOwners reads the status and role of the owners of devs from the auth
// database. Without one there are no users and the map is empty.
func (a *Application) deviceOwners(ctx context.Context, devs []p2ppair.Device) (map[string]deviceOwner, error) {
	out := map[string]deviceOwner{}
	db := a.AuthDB()
	if db == nil {
		return out, nil
	}
	seen := map[string]bool{}
	var ids []string
	for _, d := range devs {
		if d.OwnerID != "" && d.RevokedAt == nil && !seen[d.OwnerID] {
			seen[d.OwnerID] = true
			ids = append(ids, d.OwnerID)
		}
	}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []auth.User
	if err := db.WithContext(ctx).Select("id", "role", "status").Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, u := range rows {
		out[u.ID] = deviceOwner{active: u.Status == auth.StatusActive, admin: u.Role == auth.RoleAdmin}
	}
	return out, nil
}

// admitDevice says whether an active device belongs in the gate. A sign-in
// device acts as its owner, so it stays out once the owner is disabled or
// deleted, even on a frontend that missed RevokeUserDevices.
func admitDevice(d p2ppair.Device, owners map[string]deviceOwner) bool {
	if !d.SignIn {
		return true
	}
	o, ok := owners[d.OwnerID]
	return ok && o.active
}

// reservesOnRelay says whether an admitted device may hold a reservation on
// the in-process relay. A sign-in device never listens, and a device of a
// user who is not an administrator should not hold a slot of the server.
// The legacy identity of LOCALAI_API_KEY (no owner) is an administrator, and
// a device whose owner row is gone keeps what it had before users could pair.
func reservesOnRelay(d p2ppair.Device, owners map[string]deviceOwner) bool {
	if d.SignIn {
		return false
	}
	o, ok := owners[d.OwnerID]
	return !ok || o.admin
}

// mayReserve reports whether p may reserve on the in-process relay.
func (rt *p2pRuntime) mayReserve(p p2pnet.ID) bool {
	rt.peersMu.Lock()
	defer rt.peersMu.Unlock()
	_, ok := rt.reservers[p]
	return ok
}

// p2pOwnerName names the owner of a sign-in device in the redeem answer, so
// the app can say whose account the device signs in as: the display name, or
// the email when the account has none.
func (a *Application) p2pOwnerName(ctx context.Context, ownerID string) string {
	db := a.AuthDB()
	if db == nil || ownerID == "" {
		return ""
	}
	var u auth.User
	if err := db.WithContext(ctx).Select("id", "name", "email").Where("id = ?", ownerID).Take(&u).Error; err != nil {
		return ""
	}
	if u.Name != "" {
		return u.Name
	}
	return u.Email
}
