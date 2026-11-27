// Package store keeps the records of the plugins that are compatible with the
// Claude Code plugin format: marketplaces, marketplace snapshots, packages,
// installs and the state of each user.
//
// Mode (see .agents/distributed-state.md): stateless per request. Every
// frontend reads the rows of the shared database for each request and each
// turn. GormStore is the store of a server with a database; FileStore keeps
// the same records in one JSON file for a server without one, which has one
// frontend only.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// Kinds of marketplace source.
const (
	KindGitHub = "github"
	KindGit    = "git"
	KindURL    = "url"
)

// Scopes of an install.
const (
	ScopeInstance = "instance"
	ScopeUser     = "user"
)

// States of an install.
const (
	StateInstalled       = "installed"
	StateUpdateAvailable = "update_available"
	StateBlocked         = "blocked"
)

// MaxSnapshotBody is the largest marketplace.json a snapshot keeps.
const MaxSnapshotBody = 1 << 20

var (
	// ErrNotFound means the record does not exist.
	ErrNotFound = errors.New("plugins store: not found")
	// ErrConflict means a unique key is taken.
	ErrConflict = errors.New("plugins store: conflict")
	// ErrInvalid means the record breaks a rule of the store.
	ErrInvalid = errors.New("plugins store: invalid record")
)

// InstanceInstalledError refuses a user install of a plugin ID that has an
// instance install. errors.Is(err, ErrConflict) holds for it.
type InstanceInstalledError struct {
	// InstallID is the ID of the instance install.
	InstallID string
}

func (e *InstanceInstalledError) Error() string {
	return fmt.Sprintf("plugins store: the plugin is installed for everyone (install %s)", e.InstallID)
}

// Is makes the error match ErrConflict.
func (e *InstanceInstalledError) Is(target error) bool { return target == ErrConflict }

var marketplaceIDRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// ValidMarketplaceID reports whether id can name a marketplace.
func ValidMarketplaceID(id string) bool { return marketplaceIDRE.MatchString(id) }

// Marketplace is a source of plugins.
type Marketplace struct {
	ID       string
	Name     string
	Kind     string // KindGitHub, KindGit or KindURL
	Location string // owner/repo, an https git URL or an https JSON URL
	Ref      string // branch or tag to follow; empty is the default branch
	Enabled  bool
	// Default marks the marketplace that LocalAI ships.
	Default bool
	// Removed is the tombstone of a removed default. Marketplaces returns
	// it so that seeding can see it; callers never list or enable it.
	Removed bool
	// AuthReference is reserved: a reference to a credential, never a secret.
	AuthReference string
	HeadSHA       string
	RefreshedAt   time.Time
	// LastError is a short code, never a body.
	LastError string
	CreatedBy string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Snapshot is the last good marketplace.json of one marketplace.
type Snapshot struct {
	MarketplaceID string
	SHA           string // empty for a url marketplace
	Body          []byte // at most MaxSnapshotBody
	BodySHA256    string
	Entries       []Entry
	Skipped       []Skipped
	FetchedAt     time.Time
}

// Entry is one normalized plugin entry of a marketplace.
type Entry struct {
	Name        string          `json:"name"`
	Source      json.RawMessage `json:"source,omitempty" swaggertype:"object"`
	Description string          `json:"description,omitempty"`
	Version     string          `json:"version,omitempty"`
	Category    string          `json:"category,omitempty"`
	Tags        []string        `json:"tags,omitempty"`
	Homepage    string          `json:"homepage,omitempty"`
	Author      string          `json:"author,omitempty"`
	Strict      *bool           `json:"strict,omitempty"`
	Supported   bool            `json:"supported"`
	// Reason says why an entry is not supported.
	Reason string `json:"reason,omitempty"`
}

// Skipped is a part that the parser left out, with its reason code.
type Skipped struct {
	Component string `json:"component,omitempty"`
	Name      string `json:"name,omitempty"`
	Reason    string `json:"reason"`
}

// File is one file of a package.
type File struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Mode   string `json:"mode"` // "0644" or "0755"
	SHA256 string `json:"sha256"`
}

// Package is the stored, immutable copy of one plugin at one content digest.
type Package struct {
	Digest        string // sha256:<hex>
	Name          string
	Version       string
	Description   string
	MarketplaceID string
	Entry         string
	SourceSHA     string
	SizeBytes     int64
	FileCount     int
	Files         []File
	// Model is the parsed plugin (plugins.Model) as JSON. The store does
	// not read it.
	Model       json.RawMessage
	CodeReasons []string
	CodeFiles   []string
	Skipped     []Skipped
	CreatedAt   time.Time
}

// Inspection is the last inspect of one plugin ID: what the version at SHA
// contributes and whether it runs code. Browse shows it before anyone
// installs the plugin. It holds facts about public content, never a user.
type Inspection struct {
	PluginID    string
	SHA         string
	Digest      string
	RunsCode    bool
	CodeReasons []string
	Hosts       []string
	// Model is the parsed plugin (plugins.Model) as JSON. The store does
	// not read it.
	Model     json.RawMessage
	CheckedAt time.Time
}

// Install makes a plugin available to everyone (ScopeInstance) or to one
// user (ScopeUser).
type Install struct {
	ID             string
	PluginID       string // <name>@<marketplace>
	Scope          string
	Owner          string // the user of a user install; empty for an instance install
	Digest         string
	SourceSHA      string
	PreviousDigest string
	RunsCode       bool
	ApprovedBy     string
	ApprovedAt     time.Time
	ApprovedDigest string
	UpdateDigest   string
	UpdateSHA      string
	State          string
	InstalledBy    string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// InstallFilter selects installs. Empty fields match everything.
type InstallFilter struct {
	ID       string
	PluginID string
	Scope    string
	Owner    string
	// VisibleTo selects the instance installs and the user installs of this
	// principal.
	VisibleTo string
}

func (f InstallFilter) match(in Install) bool {
	switch {
	case f.ID != "" && in.ID != f.ID,
		f.PluginID != "" && in.PluginID != f.PluginID,
		f.Scope != "" && in.Scope != f.Scope,
		f.Owner != "" && in.Owner != f.Owner,
		f.VisibleTo != "" && in.Scope != ScopeInstance && in.Owner != f.VisibleTo:
		return false
	}
	return true
}

// UserState is what one principal chose for one install.
type UserState struct {
	Principal string
	InstallID string
	Enabled   bool
	// NeedsReenable is set when an update adds code. The install does not
	// contribute for this principal until they turn it on again.
	NeedsReenable bool
	Config        json.RawMessage // non-sensitive userConfig values
	Secrets       []byte          // sealed sensitive values
	UpdatedAt     time.Time
}

// Kinds of preview token.
const (
	TokenInstall = "install"
	TokenUpdate  = "update"
)

// Token is a preview token: the result of an inspect or an update preview
// that one principal may confirm once before it expires.
type Token struct {
	// ID is the SHA-256 (hex) of the secret given to the client.
	ID        string
	Kind      string // TokenInstall or TokenUpdate
	Principal string
	PluginID  string
	// InstallID is the install that an update token changes.
	InstallID string
	SHA       string
	Digest    string
	RunsCode  bool
	// CodeFiles are the code files a confirmation must name: the changed
	// code files of an update that needs a review of code.
	CodeFiles []string
	// Review is set when the confirmation needs the administrator's browser
	// session: an update whose code changed, even with no file to tick.
	Review bool
	// Name is the manifest name of the plugin, the namespace of its skills
	// and commands.
	Name      string
	ExpiresAt time.Time
	CreatedAt time.Time
}

func validToken(t Token) error {
	if t.ID == "" || t.Principal == "" || t.PluginID == "" || t.ExpiresAt.IsZero() {
		return fmt.Errorf("%w: a token needs an ID, a principal, a plugin and an expiry", ErrInvalid)
	}
	switch t.Kind {
	case TokenInstall:
	case TokenUpdate:
		if t.InstallID == "" {
			return fmt.Errorf("%w: an update token needs an install", ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: unknown token kind %q", ErrInvalid, t.Kind)
	}
	return nil
}

// validInstall checks the rules of a new or changed install.
func validInstall(in Install) error {
	if in.ID == "" || in.PluginID == "" {
		return fmt.Errorf("%w: an install needs an ID and a plugin ID", ErrInvalid)
	}
	switch in.Scope {
	case ScopeInstance:
		if in.Owner != "" {
			return fmt.Errorf("%w: an instance install has no owner", ErrInvalid)
		}
	case ScopeUser:
		if in.Owner == "" {
			return fmt.Errorf("%w: a user install needs an owner", ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: unknown scope %q", ErrInvalid, in.Scope)
	}
	return nil
}

func validMarketplace(m Marketplace) error {
	if !ValidMarketplaceID(m.ID) {
		return fmt.Errorf("%w: %q is not a marketplace ID", ErrInvalid, m.ID)
	}
	return nil
}

func validSnapshot(s Snapshot) error {
	if s.MarketplaceID == "" {
		return fmt.Errorf("%w: a snapshot needs a marketplace", ErrInvalid)
	}
	if len(s.Body) > MaxSnapshotBody {
		return fmt.Errorf("%w: marketplace.json is over %d bytes", ErrInvalid, MaxSnapshotBody)
	}
	return nil
}

// keepTombstone reports whether saving m over old must keep the tombstone of
// a removed default: the seeding of the shipped defaults saves them at every
// start, and must not bring back one that an administrator removed.
func keepTombstone(old, m Marketplace) bool {
	return old.Default && old.Removed && m.Default && !m.Removed
}

// referenced reports whether any install refers to digest.
func referenced(installs []Install, digest string) bool {
	for _, in := range installs {
		if in.Digest == digest || in.PreviousDigest == digest || in.UpdateDigest == digest {
			return true
		}
	}
	return false
}
