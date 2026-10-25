// SPDX-License-Identifier: MIT

// Package pins stores which add-ons each user pinned to the sidebar, and in
// which order. A pin names an add-on ID, never a generation. The service
// never deletes a pin because an add-on stopped, was updated or lost access;
// only a removed add-on's pin expires, after Retention.
package pins

import (
	"context"
	"time"
)

// HardLimit is the longest list the server accepts. The sidebar caps the list
// lower (eight); this bound only stops a runaway client.
const HardLimit = 32

// Retention is how long the pin of a removed add-on is kept. It must not be
// shorter than the time an administrator needs to reinstall after a mistake.
const Retention = 30 * 24 * time.Hour

// SeedIDs are pinned once, at upgrade, for every account that already exists,
// so the layout does not change under users who had Studio and Talk in the
// sidebar. An account created after the upgrade is never seeded.
var SeedIDs = []string{"studio", "talk"}

// UpgradeSeedName names the one seed run of this release. A later release that
// seeds something else uses another name.
const UpgradeSeedName = "studio-talk-v1"

// Pin is one row: one user, one add-on, one position. Every field is
// serialised: the file store writes whole Pin values, and a pin without its
// owner or its missing mark would restart the retention window at each reopen.
type Pin struct {
	UserID       string     `gorm:"primaryKey;size:64" json:"userId"`
	AddonID      string     `gorm:"primaryKey;size:64" json:"id"`
	Position     int        `gorm:"index" json:"position"`
	MissingSince *time.Time `json:"missingSince,omitempty"`
}

func (Pin) TableName() string { return "addon_pins" }

// SeedRun records that a seed ran for the whole deployment.
type SeedRun struct {
	Name string `gorm:"primaryKey;size:64"`
	At   time.Time
}

func (SeedRun) TableName() string { return "addon_pin_seed_runs" }

// Store keeps the pins. Every method is safe for concurrent use.
type Store interface {
	List(ctx context.Context, userID string) ([]Pin, error)
	Replace(ctx context.Context, userID string, ids []string) error
	SeedExisting(ctx context.Context, userIDs []string, ids []string) (bool, error)
	Distinct(ctx context.Context) ([]string, error)
	MarkMissing(ctx context.Context, ids []string, now time.Time) error
	ClearMissing(ctx context.Context, ids []string) error
	DeleteMissingBefore(ctx context.Context, cutoff time.Time) (int64, error)
}
