// SPDX-License-Identifier: MIT

// Package usersettings keeps the choices that each person makes for an
// add-on, such as how the Short memory add-on saves a note. The host keeps
// the answers, not the add-on: they live in the account database that every
// frontend shares, or in one file when authentication is off (one local
// owner). The add-on declares the keys and the allowed values in its manifest
// (contract.UserSetting); the HTTP layer checks every write against that
// declaration, and the host rules that read a setting treat any missing,
// unknown or unreadable answer as the default of the declaration.
//
// Authority: per person, shared by all frontends (the account database), so a
// save through one frontend is read through another. A write replaces one
// key and is idempotent, so a retry after an uncertain write is safe; the
// last write wins, as in any single choice. The store never falls back to a
// local copy when the database is unavailable.
package usersettings

import (
	"context"
	"errors"
)

// Setting is one stored answer: one person, one add-on, one key.
type Setting struct {
	UserID  string `gorm:"primaryKey;size:64" json:"userId"`
	AddonID string `gorm:"primaryKey;size:64" json:"addonId"`
	Key     string `gorm:"primaryKey;size:40" json:"key"`
	Value   string `gorm:"size:40" json:"value"`
}

func (Setting) TableName() string { return "addon_user_settings" }

// ErrUnavailable means the store could not answer.
var ErrUnavailable = errors.New("user settings are unavailable")

// Store keeps the answers. Every method is safe for concurrent use.
type Store interface {
	// Get returns the stored answers of one person for one add-on.
	Get(ctx context.Context, userID, addonID string) (map[string]string, error)
	// Set stores one answer, replacing the one before.
	Set(ctx context.Context, userID, addonID, key, value string) error
}
