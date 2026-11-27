package auth

import (
	"context"

	"gorm.io/gorm"
)

// DeviceSessionCheck reports whether a request may use a session that the
// paired P2P device deviceID opened with its credential.
type DeviceSessionCheck func(ctx context.Context, deviceID string) bool

type deviceSessionCheckKey struct{}

// WithDeviceSessionCheck marks ctx as the context of a request that arrived
// over P2P, with the check that decides which device sessions it may use.
// Only the P2P HTTP handler sets it, from the peer that libp2p authenticated,
// so a device session presented on the normal HTTP listener finds no check
// and is refused.
func WithDeviceSessionCheck(ctx context.Context, check DeviceSessionCheck) context.Context {
	return context.WithValue(ctx, deviceSessionCheckKey{}, check)
}

// deviceSessionUsable reports whether the request of ctx may use s. A session
// without a device is a normal web session. A device session is bound to its
// device: the check in ctx must accept it.
func deviceSessionUsable(ctx context.Context, s *Session) bool {
	if s.DeviceID == nil {
		return true
	}
	check, _ := ctx.Value(deviceSessionCheckKey{}).(DeviceSessionCheck)
	return check != nil && check(ctx, *s.DeviceID)
}

// ValidateRequestSession is ValidateSession for a request: it also refuses a
// device session that the request of ctx may not use (see
// WithDeviceSessionCheck). Every request path must use it.
func ValidateRequestSession(ctx context.Context, db *gorm.DB, token, hmacSecret string) (*User, *Session) {
	u, s := ValidateSession(db, token, hmacSecret)
	if u == nil || !deviceSessionUsable(ctx, s) {
		return nil, nil
	}
	return u, s
}
