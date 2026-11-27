package store

import (
	"context"
	"time"
)

// Store keeps the plugin records. GormStore and FileStore implement it and
// pass the same suite (storetest).
type Store interface {
	// Marketplaces lists every marketplace, tombstones included.
	Marketplaces(ctx context.Context) ([]Marketplace, error)
	// SaveMarketplace creates or replaces a marketplace. Saving a default
	// whose stored record is a tombstone keeps the tombstone.
	SaveMarketplace(ctx context.Context, m Marketplace) error
	// DeleteMarketplace removes a marketplace and its snapshot. A default
	// becomes a tombstone instead, so that it stays removed.
	DeleteMarketplace(ctx context.Context, id string) error

	Snapshot(ctx context.Context, marketplaceID string) (Snapshot, error)
	// SaveSnapshot replaces the snapshot of a marketplace.
	SaveSnapshot(ctx context.Context, s Snapshot) error

	Package(ctx context.Context, digest string) (Package, error)
	// PutPackage inserts a package once. A second insert of the same digest
	// is not an error and changes nothing.
	PutPackage(ctx context.Context, p Package) error
	// UnreferencedPackages lists the digests of the packages created before
	// olderThan that no install refers to (current, previous or update).
	UnreferencedPackages(ctx context.Context, olderThan time.Time) ([]string, error)
	// DeletePackage deletes a package row when no install refers to it, and
	// reports whether it did.
	DeletePackage(ctx context.Context, digest string) (bool, error)

	Installs(ctx context.Context, f InstallFilter) ([]Install, error)
	// InstallTx runs fn in one transaction: either all its writes happen
	// or none.
	InstallTx(ctx context.Context, fn func(Tx) error) error

	// UserState returns ErrNotFound when the principal never chose
	// anything for the install.
	UserState(ctx context.Context, principal, installID string) (UserState, error)
	// UserStates lists the state of every install for one principal.
	UserStates(ctx context.Context, principal string) ([]UserState, error)
	// SaveUserState creates or replaces a state. The install must exist.
	SaveUserState(ctx context.Context, s UserState) error
	// MarkNeedsReenable sets NeedsReenable on every user state of the
	// install.
	MarkNeedsReenable(ctx context.Context, installID string) error

	// PutToken stores a preview token. The ID is a hash of the secret that
	// the client holds, never the secret.
	PutToken(ctx context.Context, t Token) error
	// Token returns a token that has not expired at now, or ErrNotFound.
	Token(ctx context.Context, id string, now time.Time) (Token, error)
	// TakeToken returns and deletes a token that has not expired at now, or
	// returns ErrNotFound. Of two concurrent takes, one gets the token.
	TakeToken(ctx context.Context, id string, now time.Time) (Token, error)
	// DeleteExpiredTokens deletes the tokens that expired before now.
	DeleteExpiredTokens(ctx context.Context, now time.Time) (int, error)

	// SaveInspection creates or replaces the last inspection of a plugin ID.
	SaveInspection(ctx context.Context, in Inspection) error
	// Inspections lists the last inspection of every plugin ID.
	Inspections(ctx context.Context) ([]Inspection, error)
}

// Tx is the view of the store inside InstallTx.
type Tx interface {
	Installs(f InstallFilter) ([]Install, error)
	Package(digest string) (Package, error)
	PutPackage(p Package) error
	// CreateInstall inserts an install. A taken (plugin ID, scope, owner)
	// is ErrConflict, and a user install of a plugin ID that has an
	// instance install is an *InstanceInstalledError.
	CreateInstall(in Install) error
	// UpdateInstall replaces an existing install, keeping its creation time.
	UpdateInstall(in Install) error
	// DeleteInstall removes an install and its user states.
	DeleteInstall(id string) error
	MarkNeedsReenable(installID string) error
	// SaveUserState creates or replaces a state. The install must exist.
	SaveUserState(s UserState) error
}
