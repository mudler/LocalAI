package application

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/http/auth"
	p2papi "github.com/mudler/LocalAI/core/http/endpoints/p2p"
	"github.com/mudler/LocalAI/core/services/p2ppair"
)

// userMintMu makes the limit checks and the mint of a user invite one step on
// this frontend. Two frontends can still both pass the checks at the same
// moment; the device limit is checked again in the redeem transaction.
var userMintMu sync.Mutex

// pairingUser returns the active user row of a user who pairs their own
// device. The legacy identity of LOCALAI_API_KEY has no row.
func (a *Application) pairingUser(ctx context.Context, userID string) (*auth.User, error) {
	db := a.AuthDB()
	if db == nil || userID == "" {
		return nil, p2papi.ErrPairNeedsAccount
	}
	var u auth.User
	if err := db.WithContext(ctx).Where("id = ? AND status = ?", userID, auth.StatusActive).Take(&u).Error; err != nil {
		return nil, p2papi.ErrPairNeedsAccount
	}
	return &u, nil
}

// userPairingPolicy reads p2p_user_pairing from the auth database. It is read
// on each use: the database is the authority for every frontend, and users
// mint rarely.
func (a *Application) userPairingPolicy(ctx context.Context) (p2ppair.UserPairingPolicy, error) {
	db := a.AuthDB()
	if db == nil {
		return p2ppair.DefaultUserPairingPolicy(), fmt.Errorf("%w: user pairing needs authentication", p2papi.ErrConflict)
	}
	return p2ppair.LoadUserPairingPolicy(ctx, db)
}

// p2pDeviceLimit is the device limit of the redeem of a sign-in invite. A
// policy that cannot be read gives the default limit, never none.
func (a *Application) p2pDeviceLimit(ctx context.Context) int {
	p, err := a.userPairingPolicy(ctx)
	if err != nil {
		xlog.Warn("P2P: could not read the user pairing setting, using the default device limit", "error", err)
		return p2ppair.DefaultUserPairingPolicy().MaxDevices
	}
	return p.MaxDevices
}

// userNames returns the display names of the owners of devs.
func (a *Application) userNames(ctx context.Context, devs []p2ppair.Device) map[string]string {
	out := map[string]string{}
	db := a.AuthDB()
	if db == nil {
		return out
	}
	seen := map[string]bool{}
	var ids []string
	for _, d := range devs {
		if d.OwnerID != "" && !seen[d.OwnerID] {
			seen[d.OwnerID] = true
			ids = append(ids, d.OwnerID)
		}
	}
	if len(ids) == 0 {
		return out
	}
	var rows []auth.User
	if err := db.WithContext(ctx).Select("id", "name", "email").Where("id IN ?", ids).Find(&rows).Error; err != nil {
		xlog.Warn("P2P: could not read the names of the device owners", "error", err)
		return out
	}
	for _, u := range rows {
		out[u.ID] = u.Name
		if u.Name == "" {
			out[u.ID] = u.Email
		}
	}
	return out
}

func policyView(p p2ppair.UserPairingPolicy) p2papi.UserPairingSettings {
	return p2papi.UserPairingSettings{
		Relays: string(p.Relays), AllowOpenRelay: p.AllowOpenRelay, OmitLANAddrs: p.OmitLANAddrs,
		MaxDevices: p.MaxDevices, MaxPending: p.MaxPending, MaxTTLSeconds: p.MaxTTLSeconds,
	}
}

func (s p2pService) UserPairing(ctx context.Context) (p2papi.UserPairingSettings, error) {
	p, err := s.n.app.userPairingPolicy(ctx)
	if err != nil {
		return p2papi.UserPairingSettings{}, err
	}
	return policyView(p), nil
}

func (s p2pService) SetUserPairing(ctx context.Context, by string, req p2papi.SetUserPairingRequest) (p2papi.UserPairingSettings, error) {
	p, err := s.n.app.userPairingPolicy(ctx)
	if err != nil {
		return p2papi.UserPairingSettings{}, err
	}
	if req.Relays != nil {
		p.Relays = p2ppair.RelayMode(*req.Relays)
	}
	if req.AllowOpenRelay != nil {
		p.AllowOpenRelay = *req.AllowOpenRelay
	}
	if req.OmitLANAddrs != nil {
		p.OmitLANAddrs = *req.OmitLANAddrs
	}
	if req.MaxDevices != nil {
		p.MaxDevices = *req.MaxDevices
	}
	if req.MaxPending != nil {
		p.MaxPending = *req.MaxPending
	}
	if req.MaxTTLSeconds != nil {
		p.MaxTTLSeconds = *req.MaxTTLSeconds
	}
	if err := p2ppair.SaveUserPairingPolicy(ctx, s.n.app.AuthDB(), p, by); err != nil {
		if errors.Is(err, p2ppair.ErrMalformed) {
			return p2papi.UserPairingSettings{}, pairErr(err)
		}
		return p2papi.UserPairingSettings{}, err
	}
	return policyView(p), nil
}

func (s p2pService) MyPairing(ctx context.Context, user string) (p2papi.MyPairing, error) {
	u, err := s.n.app.pairingUser(ctx, user)
	if err != nil {
		return p2papi.MyPairing{}, err
	}
	p, err := s.n.app.userPairingPolicy(ctx)
	if err != nil {
		return p2papi.MyPairing{}, err
	}
	out := p2papi.MyPairing{Limits: p2papi.UserPairingLimits{
		MaxDevices: p.MaxDevices, MaxPending: p.MaxPending, MaxTTLSeconds: p.MaxTTLSeconds, MaxMintsPerHour: p2ppair.MaxUserMintsPerHour,
	}}
	rt, mode := s.n.runtime()
	out.Mode = string(mode)
	if out.Mode == "" {
		out.Mode = string(config.P2PModeOff)
	}
	if rt == nil || mode != config.P2PModeShare {
		out.Reason = "not_share"
		return out, nil
	}
	out.Available = true
	devs, err := rt.pair.ListDevicesByOwner(ctx, u.ID)
	if err != nil {
		return p2papi.MyPairing{}, err
	}
	for _, d := range devs {
		if d.SignIn && d.RevokedAt == nil {
			out.Devices++
		}
	}
	invs, err := rt.pair.ListInvitesByOwner(ctx, u.ID)
	if err != nil {
		return p2papi.MyPairing{}, err
	}
	now := time.Now()
	for _, i := range invs {
		if i.SignIn && i.Status(now) == p2ppair.InvitePending {
			out.PendingInvites++
		}
	}
	return out, nil
}

// userShare returns the host of mode share and the user row of a user route.
func (s p2pService) userShare(ctx context.Context, user string) (*p2pRuntime, *auth.User, error) {
	u, err := s.n.app.pairingUser(ctx, user)
	if err != nil {
		return nil, nil, err
	}
	rt, err := s.share()
	if err != nil {
		return nil, nil, err
	}
	return rt, u, nil
}

func (s p2pService) MintMyInvite(ctx context.Context, user string, req p2papi.MyMintRequest) (p2papi.MintResult, error) {
	rt, u, err := s.userShare(ctx, user)
	if err != nil {
		return p2papi.MintResult{}, err
	}
	p, err := s.n.app.userPairingPolicy(ctx)
	if err != nil {
		return p2papi.MintResult{}, err
	}
	maxTTL := time.Duration(p.MaxTTLSeconds) * time.Second
	ttl := time.Duration(req.TTLSeconds) * time.Second
	switch {
	case req.TTLSeconds < 0 || ttl > maxTTL:
		return p2papi.MintResult{}, fmt.Errorf("%w: ttl_seconds must be between 0 and %d", p2papi.ErrInvalid, p.MaxTTLSeconds)
	case ttl == 0:
		ttl = min(p2ppair.DefaultTTL, maxTTL)
	}

	userMintMu.Lock()
	defer userMintMu.Unlock()
	if err := checkUserMintLimits(ctx, rt.pair, u.ID, p, time.Now()); err != nil {
		return p2papi.MintResult{}, err
	}
	m, err := rt.pair.Mint(ctx, p2ppair.MintRequest{
		OwnerID: u.ID, CreatedBy: u.ID, Kind: p2ppair.KindDevice, SignIn: true, Label: req.Label, TTL: ttl,
		Relays: p.Relays, AllowOpenRelay: p.AllowOpenRelay, OmitLANAddrs: p.OmitLANAddrs,
	})
	if err != nil {
		return p2papi.MintResult{}, pairErr(err)
	}
	rt.reload()
	xlog.Info("P2P: a user minted an invite for their own device", "user", u.ID, "invite", m.Invite.ID)
	return mintResult(m), nil
}

// checkUserMintLimits refuses a mint that would pass a limit: the pending
// invites and the devices of the user, the mints of the user in the last
// hour, and the pending sign-in invites of the whole instance. Each count is
// read from the store, which every frontend shares.
func checkUserMintLimits(ctx context.Context, pair *p2ppair.Service, user string, p p2ppair.UserPairingPolicy, now time.Time) error {
	invs, err := pair.ListInvitesByOwner(ctx, user)
	if err != nil {
		return err
	}
	pending, recent := 0, 0
	for _, i := range invs {
		if !i.SignIn {
			continue
		}
		if i.Status(now) == p2ppair.InvitePending {
			pending++
		}
		if now.Sub(i.CreatedAt) < time.Hour {
			recent++
		}
	}
	if pending >= p.MaxPending {
		return fmt.Errorf("%w: you have %d pending invites, the most allowed; cancel one or wait until it expires", p2papi.ErrLimitReached, pending)
	}
	if recent >= p2ppair.MaxUserMintsPerHour {
		return fmt.Errorf("%w: you made %d invites in the last hour, the most allowed", p2papi.ErrLimitReached, recent)
	}
	devs, err := pair.ListDevicesByOwner(ctx, user)
	if err != nil {
		return err
	}
	active := 0
	for _, d := range devs {
		if d.SignIn && d.RevokedAt == nil {
			active++
		}
	}
	if active >= p.MaxDevices {
		return fmt.Errorf("%w: you have %d devices, the most allowed; revoke one first", p2papi.ErrLimitReached, active)
	}
	all, err := pair.ListInvites(ctx)
	if err != nil {
		return err
	}
	instance := 0
	for _, i := range all {
		if i.SignIn && i.Status(now) == p2ppair.InvitePending {
			instance++
		}
	}
	if instance >= p2ppair.MaxUserPendingInstance {
		return fmt.Errorf("%w: this server has too many pending invites, try again later", p2papi.ErrLimitReached)
	}
	return nil
}

func (s p2pService) ListMyInvites(ctx context.Context, user string) ([]p2papi.InviteView, error) {
	rt, u, err := s.userShare(ctx, user)
	if err != nil {
		return nil, err
	}
	invs, err := rt.pair.ListInvitesByOwner(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	return rt.inviteViews(invs, func(i p2ppair.Invite) bool { return i.SignIn && i.Status(now) == p2ppair.InvitePending }), nil
}

func (s p2pService) CancelMyInvite(ctx context.Context, user, id string) error {
	rt, u, err := s.userShare(ctx, user)
	if err != nil {
		return err
	}
	inv, err := rt.pair.GetInvite(ctx, id)
	if err != nil || inv.OwnerID != u.ID || !inv.SignIn {
		// The same answer as an unknown ID: a user learns nothing about the
		// invites of others, nor about the instance invites they minted as
		// an administrator.
		return p2papi.ErrNotFound
	}
	if _, err := rt.pair.Cancel(ctx, id, u.ID); err != nil {
		return pairErr(err)
	}
	rt.reload()
	return nil
}

// ownDevice returns a sign-in device of the user, or ErrNotFound for any other
// ID. A device without sign-in (a worker, a remote, a laptop) belongs to the
// instance even when the user minted it as an administrator, so a user route
// never sees it: a demoted administrator must not keep control of it.
func ownDevice(ctx context.Context, rt *p2pRuntime, user, id string) (p2ppair.Device, error) {
	d, err := rt.pair.GetDevice(ctx, id)
	if err != nil || d.OwnerID != user || !d.SignIn {
		return p2ppair.Device{}, p2papi.ErrNotFound
	}
	return d, nil
}

func (s p2pService) ListMyDevices(ctx context.Context, user string) ([]p2papi.DeviceView, error) {
	rt, u, err := s.userShare(ctx, user)
	if err != nil {
		return nil, err
	}
	devs, err := rt.pair.ListDevicesByOwner(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	devs = slices.DeleteFunc(devs, func(d p2ppair.Device) bool { return !d.SignIn })
	return rt.deviceViews(ctx, devs), nil
}

func (s p2pService) RenameMyDevice(ctx context.Context, user, id, label string) error {
	rt, u, err := s.userShare(ctx, user)
	if err != nil {
		return err
	}
	if _, err := ownDevice(ctx, rt, u.ID, id); err != nil {
		return err
	}
	return pairErr(rt.pair.RenameDevice(ctx, id, label))
}

func (s p2pService) RevokeMyDevice(ctx context.Context, user, id string) error {
	rt, u, err := s.userShare(ctx, user)
	if err != nil {
		return err
	}
	d, err := ownDevice(ctx, rt, u.ID, id)
	if err != nil {
		return err
	}
	revokeErr := rt.revokeDevice(ctx, d, u.ID)
	rt.changed("revoke")
	return pairErr(revokeErr)
}
