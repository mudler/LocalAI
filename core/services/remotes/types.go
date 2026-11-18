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

// Transport is how the server reaches a remote. The empty value is the
// address of the remote, over HTTP or HTTPS.
type Transport string

// TransportP2P is a LocalAI reached over P2P. Its address is p2p://<peer ID>
// and its traffic goes through a forwarder on a loopback port of this
// server, so the localai-proxy backend and the listings use a plain
// http://127.0.0.1:<port> address.
const TransportP2P Transport = "p2p"

// p2pScheme is the scheme of the address of a remote over P2P.
const p2pScheme = "p2p://"

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
	// ErrInviteGone means the invite link of a remote over P2P cannot be
	// used: it was used, cancelled or expired. It comes wrapped in an
	// InvalidError that carries the message.
	ErrInviteGone = errors.New("remotes: the invite is not valid any more")
	// ErrInviteRateLimited means the other server saw too many wrong codes.
	// It comes wrapped in an InvalidError that carries the message.
	ErrInviteRateLimited = errors.New("remotes: too many wrong invite codes")
	// ErrInviteUnreachable means the server of the invite could not be
	// reached over P2P. It comes wrapped in an InvalidError that carries the
	// message.
	ErrInviteUnreachable = errors.New("remotes: the server of the invite cannot be reached")
)

const (
	// MaxListedModels caps the models one listing keeps. An upstream that
	// lists more is cut to the first MaxListedModels ids in sort order.
	MaxListedModels = 2000
	// MaxAdoptModels caps the models one Create or Adopt call may adopt.
	MaxAdoptModels = 500
)

// InvalidError is an ErrInvalid with a message that a UI can show as is.
// Reason, when set, is a more precise error such as ErrInviteGone.
type InvalidError struct {
	Message string
	Reason  error
}

func (e *InvalidError) Error() string { return e.Message }

// Is makes errors.Is(err, ErrInvalid) true, and errors.Is(err, e.Reason).
func (e *InvalidError) Is(target error) bool {
	return target == ErrInvalid || (e.Reason != nil && target == e.Reason)
}

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
	// Transport is "p2p" for a remote over P2P and absent otherwise.
	Transport Transport `json:"transport,omitempty"`
	// P2P is the tunnel of a remote over P2P. It is the state of this
	// server's forwarder, not of the shared store.
	P2P *P2PInfo `json:"p2p,omitempty"`
}

// P2PInfo describes the tunnel of a remote over P2P.
type P2PInfo struct {
	PeerID string `json:"peer_id"`
	// LocalPort is the loopback port of the forwarder. The entries of the
	// remote use http://127.0.0.1:<LocalPort> as their upstream address.
	LocalPort int `json:"local_port"`
	// State is off (P2P is off on this server), listening (no request went
	// through yet), up, down or error (the port could not be opened).
	State TunnelState `json:"state"`
	// Path is how the last request reached the server: direct,
	// hole_punched or relayed. Empty before the first request.
	Path string `json:"path,omitempty"`
	// Limited is true when the relay of a relayed path limits the data or
	// the time of the connection.
	Limited bool `json:"limited,omitempty"`
	// Error is the last dial or listen error, for a person to read.
	Error string `json:"error,omitempty"`
}

// TunnelState is the state of the forwarder of a remote over P2P.
type TunnelState string

// The states of a forwarder.
const (
	TunnelOff       TunnelState = "off"
	TunnelListening TunnelState = "listening"
	TunnelUp        TunnelState = "up"
	TunnelDown      TunnelState = "down"
	TunnelError     TunnelState = "error"
)

// Tunnel is the forwarder of one remote over P2P: a loopback port of this
// server whose connections go to the remote's peer.
type Tunnel struct {
	// ID is the id of the remote.
	ID   string
	Peer string
	// Addrs are direct multiaddrs of the peer and Relays relay multiaddrs
	// ending in /p2p/<relay ID>, both from the invite link.
	Addrs  []string
	Relays []string
	// Port is the loopback port. Zero picks a free port.
	Port int
}

// Pairing is the result of a redeemed invite of kind remote.
type Pairing struct {
	Peer       string
	ServerName string
	// Phrase is the confirmation phrase; the server shows the same words.
	Phrase string
	// Key is the API key the server issued for this pairing.
	Key    string
	Addrs  []string
	Relays []string
}

// Tunnels pairs with remotes over P2P and runs their forwarders. The
// application implements it on its P2P host. Errors that a person can act
// on are *InvalidError values.
type Tunnels interface {
	// Pair redeems an invite link of kind remote. Without allowOpenRelay a
	// link that names an open relay is refused.
	Pair(ctx context.Context, link string, allowOpenRelay bool) (Pairing, error)
	// Open keeps a forwarder for t: it runs while P2P is on, and starts
	// again whenever P2P starts. It returns the port in use. Port zero needs
	// P2P to be on now, to pick a free port.
	Open(t Tunnel) (int, error)
	// Close stops the forwarder of a remote and forgets it.
	Close(id string)
	// Status returns the state of the forwarder of a remote.
	Status(id string) P2PInfo
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
	// Invite is an invite link of kind remote. With it the remote is a
	// LocalAI over P2P: URL and APIKey must be empty, the key is the one the
	// server issues when the invite is redeemed, and Name defaults to the
	// name of the server.
	Invite string
	// AllowOpenRelay accepts an invite that names an open relay.
	AllowOpenRelay bool
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
	// Phrase is the confirmation phrase of a remote added by invite.
	Phrase string
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
