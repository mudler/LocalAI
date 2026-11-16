// Package projects holds the on-disk model of a Project: a named folder of
// files owned by one user, with defaults that its chats inherit.
package projects

import (
	"encoding/json"
	"errors"
	"time"
)

const (
	// ManifestVersion is the only manifest version this build reads.
	ManifestVersion = 1
	// ManifestFile is the name of the manifest at the root of a project folder.
	ManifestFile = "project.json"
	// RootDir is the folder, under each user's files, that holds projects.
	RootDir = "Projects"
)

// Defaults are the resources a new chat in the project starts with. An empty
// list and an absent list both mean "no override"; no caller gives empty a
// different meaning.
type Defaults struct {
	Skills      []string `json:"skills,omitempty"`
	Addons      []string `json:"addons,omitempty"`
	Connectors  []string `json:"connectors,omitempty"`
	MCPServers  []string `json:"mcpServers,omitempty"`
	Collections []string `json:"collections,omitempty"`
}

// Manifest is the content of ManifestFile.
type Manifest struct {
	Version     int      `json:"version"`
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Defaults    Defaults `json:"defaults"`
	// Attached are the tools the project holds, same shape as Defaults. Every
	// chat and agent of the project gets them on each turn. Defaults remain the
	// limit. A manifest from before this field reads as nothing attached.
	Attached Defaults `json:"attached,omitzero"`
	// SharedWith is reserved. A manifest that has it still parses, but the
	// list is dropped on read and never written: sharing is a workspace grant.
	SharedWith []string `json:"sharedWith,omitempty"`
	Archived   bool     `json:"archived,omitempty"`
	// Extra holds the top-level keys this build does not know, as raw JSON,
	// so a rewrite does not drop what a newer build or a person added.
	Extra map[string]json.RawMessage `json:"-"`
}

// Status is the state of a project as seen by its owner.
type Status string

const (
	StatusActive   Status = "active"
	StatusArchived Status = "archived"
	// StatusBroken marks a project whose folder or manifest could not be read.
	StatusBroken Status = "broken"
)

// Project is one project of one owner, as listed to clients.
type Project struct {
	ID      string
	Owner   string
	Slug    string
	Name    string
	Node    int64
	Status  Status
	Error   string
	Updated time.Time
}

var (
	ErrNotFound  = errors.New("project not found")
	ErrInvalid   = errors.New("invalid project")
	ErrNameTaken = errors.New("project name already taken")
	// ErrConflict means the manifest changed while a rewrite was in flight and
	// the retry met a second change.
	ErrConflict = errors.New("project changed while it was saved")
)
