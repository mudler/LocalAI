// Package remotes manages remotes: a saved address and key of
// another LocalAI, an OpenAI-compatible endpoint or Anthropic. Each model that
// an admin picks from a remote becomes an ordinary model config (an
// entry) that carries only the remote id. The key lives in the remote
// store, sealed at rest, and is handed to the backend when the model loads.
package remotes

import (
	"context"
	"errors"
	"time"

	"github.com/mudler/LocalAI/core/config"
)

// Kind is the kind of remote.
type Kind string

const (
	// KindLocalAI is another LocalAI, served by the localai-proxy backend.
	KindLocalAI Kind = "localai"
	// KindOpenAI is an OpenAI-compatible endpoint, served by cloud-proxy.
	KindOpenAI Kind = "openai"
	// KindAnthropic is Anthropic or a gateway for it, served by cloud-proxy.
	KindAnthropic Kind = "anthropic"
)

func (k Kind) valid() bool {
	return k == KindLocalAI || k == KindOpenAI || k == KindAnthropic
}

// Status is the result of the last contact with the remote.
type Status string

const (
	StatusNever       Status = "never"
	StatusOK          Status = "ok"
	StatusAuthFailed  Status = "auth_failed"
	StatusUnreachable Status = "unreachable"
)

// Test steps of a failed TestResult.
const (
	StepInvalidURL  = "invalid_url"
	StepUnreachable = "unreachable"
	StepAuth        = "auth"
	StepNoModels    = "no_models"
)

// Errors a caller can switch on. Use errors.Is.
var (
	ErrNotFound = errors.New("remote not found")
	ErrConflict = errors.New("a remote with that name already exists")
	// ErrInvalid is wrapped by InvalidError, which carries the message.
	ErrInvalid = errors.New("invalid remote")
	// ErrPartial means the change was saved only in part: the remote
	// exists (or still exists) and the error says what is missing. A retry
	// of the same call can finish the rest. Create, Adopt, Update and Delete
	// return it; Create and Adopt also return the partial Result.
	ErrPartial = errors.New("remotes: the change was saved only in part")
)

const (
	// MaxListedModels caps the models one listing keeps. An upstream that
	// lists more is cut to the first MaxListedModels ids in sort order.
	MaxListedModels = 2000
	// MaxAdoptModels caps the models one Create or Adopt call may adopt.
	MaxAdoptModels = 500
)

// InvalidError is an ErrInvalid with a message that a UI can show as is.
type InvalidError struct{ Message string }

func (e *InvalidError) Error() string { return e.Message }

// Is makes errors.Is(err, ErrInvalid) true.
func (e *InvalidError) Is(target error) bool { return target == ErrInvalid }

func invalid(msg string) error { return &InvalidError{Message: msg} }

// UpstreamModel is a model that a remote lists.
type UpstreamModel struct {
	ID string `json:"id"`
	// Usecases are valid known_usecases values. For the cloud kinds it is
	// empty, which means chat.
	Usecases []string `json:"usecases"`
}

// Remote is the public view of a remote. The key is never part of it.
type Remote struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Kind        Kind            `json:"kind"`
	URL         string          `json:"url"` // address as entered; empty for anthropic
	KeySet      bool            `json:"key_set"`
	Status      Status          `json:"status"`
	LastError   string          `json:"last_error,omitempty"`
	LastRefresh *time.Time      `json:"last_refresh,omitempty"`
	Entries     []string        `json:"entries"`     // model config names this remote owns
	Unavailable []string        `json:"unavailable"` // subset of Entries the upstream no longer lists
	New         []UpstreamModel `json:"new"`         // discovered at last refresh, not adopted
}

// TestResult is the outcome of Service.Test. A failed test is a result, not
// an error.
type TestResult struct {
	OK      bool            `json:"ok"`
	Step    string          `json:"step,omitempty"` // invalid_url, unreachable, auth, no_models
	Message string          `json:"message,omitempty"`
	Models  []UpstreamModel `json:"models,omitempty"`
}

// CreateRequest makes a remote. Models are adopted at once. With none, no
// entry is made: the models the remote lists are only recorded in New.
type CreateRequest struct {
	Kind              Kind
	Name, URL, APIKey string
	Models            []UpstreamModel
}

// UpdateRequest changes a remote. A nil field is left as it is. An empty
// APIKey removes the key.
type UpdateRequest struct{ Name, URL, APIKey *string }

// Skip is a model that Create or Adopt did not turn into an entry.
type Skip struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// Result is the outcome of Create or Adopt.
type Result struct {
	Remote  Remote
	Created []string // names of the new entries
	Skipped []Skip
}

// Service manages remotes. Every method reads and writes the shared
// store, so any frontend of a cluster gives the same answer.
type Service interface {
	// Test lists the models of a remote without saving anything.
	Test(ctx context.Context, kind Kind, url, apiKey string) (TestResult, error)
	Create(ctx context.Context, r CreateRequest) (Result, error)
	List(ctx context.Context) ([]Remote, error)
	Get(ctx context.Context, id string) (Remote, error)
	// Update changes the remote. A new key restarts every loaded model
	// that names the remote; a new URL patches proxy.upstream_url on the
	// entries it owns. A new key or URL also refreshes the remote, so the
	// answer holds the current status.
	Update(ctx context.Context, id string, r UpdateRequest) (Remote, error)
	// Delete removes the remote. With removeEntries false the entries
	// stay as plain models: only proxy.remote is removed from them, and
	// the key is not copied into them.
	Delete(ctx context.Context, id string, removeEntries bool) error
	// Refresh asks the remote for its models and records the answer. It sets
	// Status, LastError, LastRefresh, New and Unavailable and never writes an
	// entry file.
	Refresh(ctx context.Context, id string) (Remote, error)
	Adopt(ctx context.Context, id string, models []UpstreamModel) (Result, error)
	// ResolveKey returns the key for a model load and the address the key
	// is bound to. It is never exposed over HTTP.
	ResolveKey(ctx context.Context, id string) (config.ProxyRemoteKey, error)
}
