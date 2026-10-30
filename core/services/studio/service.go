// Package studio runs the chats of the new conversation API: one loop for the
// web chat, for saved agents and for connectors. It holds no state of its own
// that another frontend would need; pending messages and the turn claim live in
// the store.
package studio

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/http/auth"
	mcpendpoint "github.com/mudler/LocalAI/core/http/endpoints/mcp"
	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/egress"
	"github.com/mudler/LocalAI/core/services/studio/egress/audit"
	"github.com/mudler/LocalAI/core/services/studio/files"
	"github.com/mudler/LocalAI/core/services/studio/mcpconns"
	"github.com/mudler/LocalAI/core/services/studio/media"
	"github.com/mudler/LocalAI/core/services/studio/nibharness"
	"github.com/mudler/LocalAI/core/services/studio/notify"
	"github.com/mudler/LocalAI/core/services/studio/pages"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/sharedfolders"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/core/services/studio/wsfeed"
)

// AnonymousPrincipal is the owner of conversations when auth is off. The loop
// needs a non-empty owner, so the empty user becomes this one.
const AnonymousPrincipal = "anonymous"

// ErrNoModel means a conversation was created without a model.
var ErrNoModel = errors.New("studio: a conversation needs a model")

// ErrAgentsOff means a chat named a saved agent but the service has no agent store.
var ErrAgentsOff = errors.New("studio: saved agents are not available")

// ErrPushOff means browser push is off: the server keeps nothing across a
// restart, or it has no push service.
var ErrPushOff = errors.New("studio: browser push is not available")

// ErrConnectorsOff means the service has no connector manager.
var ErrConnectorsOff = errors.New("studio: connectors are not available")

// ErrNotificationsOff means the service has no inbox.
var ErrNotificationsOff = errors.New("studio: notifications are not available")

// ErrSubAgentReadOnly means a user tried to send to a sub-agent conversation.
// It runs with the tools its parent chose, so only the parent starts it.
var ErrSubAgentReadOnly = errors.New("studio: a sub-agent conversation takes no messages from the user")

// UserLookup returns the active user with this ID. It is nil when auth is off.
type UserLookup func(ctx context.Context, id string) (*auth.User, error)

// Deps is what the service needs.
type Deps struct {
	Store    store.Store
	Provider *provider.Provider
	Users    UserLookup
	// StateDir holds the harness's private files, one folder per chat.
	StateDir string
	// HostTools supplies the tools a chat gets besides the ones the service adds
	// itself (for example the tools of MCP servers). A sub-agent gets all of
	// them, or the subset its parent names. Nil: none.
	HostTools func(ctx context.Context, a workspace.Actor) ([]session.Tool, error)
	// MCP returns the MCP tool executor for a user's chat with a model, or nil
	// when the model has none. Its tools become the chat's tools. Nil: no MCP.
	// With limited set, only the servers named in only are used, whether or not
	// the model picks them by itself; missing returns the named servers the user
	// cannot use. Without limited, only is nil and missing is empty.
	MCP func(ctx context.Context, user, model string, only []string, limited bool) (exec mcpendpoint.ToolExecutor, missing []string)
	// MCPServers lists the MCP servers the model's own configuration has, for a
	// user: Auto says whether a chat with no agent uses it by itself. Empty when
	// the user may not use MCP. It feeds the per-chat switches; nil: a switch of
	// a model server has no effect on a chat with no agent.
	MCPServers func(ctx context.Context, user, model string) []ModelMCPServer
	// MCPContent reads the prompts and resources of the model's own MCP
	// servers. Nil: they offer none.
	MCPContent MCPContent
	// Allowed says whether a user may use a feature (auth.FeatureTriggers,
	// auth.FeatureConnectors) right now. The unattended parts ask it before each
	// run, so an owner whose permission was taken away stops running, not only
	// stops creating. Nil: everyone may.
	Allowed func(user *auth.User, feature string) bool
	// Connectors holds the MCP servers the users defined. Nil: none.
	Connectors ConnectorTools
	// MCPConnectors is the same service with its management calls, for the
	// connectors API. When set and Connectors is not, it is used for both.
	MCPConnectors *mcpconns.Service
	// Agents holds the saved agents. Nil: a chat cannot name an agent.
	Agents agents.Store
	// Notify stores and delivers notifications. Nil: there is no inbox.
	Notify *notify.Service
	// Push sends notifications to browsers. Nil: browser push is off.
	Push *notify.Pusher
	// AddonTools supplies the tools of the installed add-ons the user may use.
	// Nil: a chat has none.
	AddonTools AddonTools
	// Assistant gives a chat the tools that manage this instance. Nil: no
	// assistant chat can start.
	Assistant Assistant
	// Collections is the knowledge base a chat can search. Nil: no collection
	// tools.
	Collections Collections
	// ContextTokens returns the context window of a model in tokens, or zero
	// when it is not known (the harness then uses its default). Nil: zero.
	ContextTokens func(model string) int
	// AgentOptions returns the tool-loop options of a model (its agent: block).
	// Nil: nib's defaults.
	AgentOptions func(model string) nibharness.AgentOptions
	// Bus carries live events and control messages between the frontends that
	// serve a chat. Nil: a bus for this process only.
	Bus Bus
	// Workers runs the turns of unattended runs (triggers, jobs, connector
	// messages) on chat workers; see SendBackground. Nil: they run here.
	Workers ChatWorkers
	// RunLimit is, on a chat worker, the longest a turn that a frontend sent may
	// run, by the chat's origin (session.OriginJob, ...). Zero or nil: no bound
	// on the worker; the frontend that watches the run bounds it.
	RunLimit func(origin string) time.Duration
	// MaxWorkerTurns is, on a chat worker, the most turns this process runs at
	// once when it takes a start (ServeStart). A start that would begin one
	// more is refused with ErrWorkerFull; a start for a chat whose turn runs
	// is taken, because its message only waits. Zero: no cap.
	MaxWorkerTurns int
	// HeartbeatEvery is how often a running turn tells the store it is alive.
	// Zero means 15 seconds.
	HeartbeatEvery time.Duration
	// Files is the workspace of the chats. Nil or disabled: the chats have no
	// files, and everything else works.
	Files *files.Service
	// Media runs the image and audio helper tools and transcribes voice notes.
	// Nil: neither.
	Media *media.Service
	// Web serves the built-in web_search and web_fetch tools. Nil: neither.
	Web WebTools
	// Modalities says which inputs a model takes natively: with Vision an
	// attached image goes to the chat model as an image part, with Video a
	// video does. A model without them gets the file and the describe_image
	// tool. Nil: no model takes either.
	Modalities func(model string) Modalities
	// PageConsents keeps the consents of the viewers of shared pages. Nil: an
	// in-memory store, for a single process and tests.
	PageConsents pages.ConsentStore
	// SharedFolderAudit keeps the audit of the shared folders. Nil: an
	// in-memory audit, for a single process and tests.
	SharedFolderAudit sharedfolders.AuditStore
	// FindUser returns the user that ref names (an ID, an email or a name),
	// whatever their status, for the members of a shared folder. Nil: Users
	// looks members up by ID, and with no Users any principal is a member.
	FindUser func(ctx context.Context, ref string) (*auth.User, error)
	// Builtins is the registry of the built-in chat tools an add-on may
	// replace. Nil: the service makes one. The service registers bash in it
	// when HostExec is set.
	Builtins *builtins.Registry
	// HostExec runs the built-in bash on this machine. Nil: there is none.
	HostExec HostExec
	// HostExecMode is the setting chats_host_exec as it applies now: off, ask
	// or on. Nil: off.
	HostExecMode func() string
	// SavedAgentsOn is the setting chats_saved_agents: whether a chat that
	// switched it on may call the user's saved agents as sub-agents. Read at
	// each turn. Nil: on.
	SavedAgentsOn func() bool
	// IsAdmin says whether a user is an administrator. Nil: everyone is (a
	// server without accounts).
	IsAdmin func(ctx context.Context, user string) bool
	// ToolPins names, for a built-in tool that several add-ons replace, the
	// add-on an administrator chose to serve it (runtime setting
	// chats_tool_providers). Nil: no pins.
	ToolPins func() map[string]string
	// SharedPagesOn says whether the pages of shared folders are on; an
	// administrator switches them at run time. Nil: on.
	SharedPagesOn func() bool
	// RemoteModel says whether a model runs on a remote service (a cloud
	// proxy), so a cost shown to the person can say that the tokens leave the
	// server. Nil: no model is remote.
	RemoteModel func(model string) bool
	// Persistent is true when the store keeps conversations across a restart.
	Persistent bool
	// SuggestionsDefault says whether the assistant may offer things (to save a
	// name, a shortcut, a schedule) to a user who did not choose. Read at each
	// turn, so a change applies at once. Nil: on.
	SuggestionsDefault func() bool
	// EgressSettings returns the administrator's limits of the Sandbox
	// network (chats_egress_*), read at each check. Nil: the ceiling is off and
	// no command reaches the network.
	EgressSettings func() egress.Settings
	// EgressAudit keeps the audit of the Sandbox network requests. Nil: an
	// in-memory audit, for a single process and tests.
	EgressAudit audit.Store
	// EgressAuditDays is chats_egress_audit_days. Nil or out of range: 30.
	EgressAuditDays func() int
	// Clock is the time the offer limits use. Nil: time.Now. It is for tests.
	Clock func() time.Time
	// Harness replaces the nib harness. It is for tests.
	Harness session.Harness
	Logger  *slog.Logger
}

// Modalities are the inputs a chat model takes besides text.
type Modalities struct{ Vision, Video bool }

// CreateInput describes a new conversation.
type CreateInput struct {
	Title string
	Model string
	// AgentID is a saved agent of the user. The agent narrows the chat's tools
	// and adds its instructions; its model, when it has one, replaces Model.
	AgentID string
	// Assistant starts a chat with the LocalAI management tools. Only an
	// administrator may, and only while the assistant is on. It cannot be
	// combined with AgentID.
	Assistant bool
}

// Origin says what started a conversation when a person did not.
type Origin struct{ Kind, ID string }

// SendResult says what Send did with a message.
type SendResult struct {
	// Started is true when the message began a turn. Otherwise a turn was
	// running, and the message is pending under PendingID.
	Started   bool
	PendingID string
}

// Service runs conversations.
type Service struct {
	d   Deps
	mgr *session.Manager
	// builtins holds the built-in chat tools an add-on may replace.
	builtins *builtins.Registry
	bus      Bus
	log      *slog.Logger

	mu    sync.Mutex
	runs  map[string]*run                 // the turn this process runs, by conversation
	pumps map[string]<-chan session.Event // the event channel read for each conversation
	locks map[string]*keyLock

	// admit makes the check of MaxWorkerTurns and the start it admits one step,
	// so two starts cannot both take the last place.
	admit sync.Mutex

	// Sub-agents; see subagent.go.
	triggers   *Triggers        // set once by SetTriggers
	jobs       *Jobs            // set once by SetJobs
	connectors *Connectors      // set once by SetConnectors
	conversion ConversionSource // set once by SetConversion

	videos   map[string][]string                // videos for the next model request of a turn, by conversation
	children map[string]map[string]*child       // running sub-agents, by parent conversation
	allowed  map[string][]session.Tool          // the tools of a sub-agent conversation
	waiters  map[string][]chan store.TurnStatus // callers waiting for a turn to end
	closed   chan struct{}

	feed *wsfeed.Feed // changes of the home folders; nil without files

	shared *sharedfolders.Service // the folders an administrator shares

	runners runners // the add-on tools that run commands; see capabilities.go

	egressAuditStore audit.Store // see egress.go
	egressChats      egressChats // see egresscheck.go
}

type run struct {
	id      string
	started time.Time
	stopped bool
	failed  bool
	// chat is the chat the turn runs on. Stop goes through it and never opens
	// the chat again: a turn that ended has closed it, and a chat left open by
	// a late Stop would hold stale state while another frontend runs the next turn.
	chat *session.Chat
	// end stops what the turn started besides the loop itself: the heartbeat and
	// the listener for control messages.
	end func()
}

const defaultHeartbeat = 15 * time.Second

type keyLock struct {
	mu sync.Mutex
	n  int
}

// New returns a Service.
func New(d Deps) (*Service, error) {
	if d.Store == nil || d.StateDir == "" {
		return nil, errors.New("studio: a store and a state folder are required")
	}
	if d.Connectors == nil && d.MCPConnectors != nil {
		d.Connectors = d.MCPConnectors
	}
	log := d.Logger
	if log == nil {
		log = slog.Default()
	}
	bus := d.Bus
	if bus == nil {
		bus = NewLocalBus()
	}
	s := &Service{d: d, bus: bus, log: log, runs: map[string]*run{}, pumps: map[string]<-chan session.Event{}, locks: map[string]*keyLock{},
		videos: map[string][]string{}, children: map[string]map[string]*child{}, allowed: map[string][]session.Tool{}, waiters: map[string][]chan store.TurnStatus{}, closed: make(chan struct{})}
	s.egressAuditStore = d.EgressAudit
	if s.egressAuditStore == nil {
		s.egressAuditStore, _ = audit.NewJSON("")
	}
	s.builtins = d.Builtins
	if s.builtins == nil {
		s.builtins = builtins.NewRegistry()
	}
	if err := s.registerHostBash(); err != nil {
		return nil, err
	}
	if err := s.registerWebTools(); err != nil {
		return nil, err
	}
	if err := s.registerSavedAgents(); err != nil {
		return nil, err
	}
	h := d.Harness
	if h == nil {
		if d.Provider == nil {
			return nil, errors.New("studio: a provider is required without a test harness")
		}
		h = nibharness.New(nibharness.Config{LLM: s.llmFor})
	}
	if d.Files == nil {
		d.Files = files.Disabled("")
		s.d.Files = d.Files
	}
	s.shared = sharedfolders.New(sharedfolders.Options{Files: d.Files, Consents: d.PageConsents, Audit: d.SharedFolderAudit, Users: s.folderUsers()})
	// The file tools reach the shared folders at the paths the chat's view
	// gives them, /workspace/shared-<name>.
	d.Files.SetSharedRoots(s.shared.Roots)
	// A chat mounts the shared folders of its owner at /workspace/shared-<name>.
	mgr, err := session.New(session.Options{Store: d.Store, Workspace: d.Files.Store(), Harness: h, StateDir: d.StateDir, Inputs: s.inputs, ChatTools: s.chatTools,
		Shared: s.shared.Roots, Logger: log})
	if err != nil {
		return nil, err
	}
	s.mgr = mgr
	if d.Files.Available() {
		s.feed = wsfeed.New(wsfeed.Options{Store: d.Files.Store(), Admin: workspace.Actor{Principal: files.AdminPrincipal}, Logger: log})
	}
	return s, nil
}

// SharedFolders returns the service of the shared folders.
func (s *Service) SharedFolders() *sharedfolders.Service { return s.shared }

// folderUsers finds the members of a shared folder in the user database.
func (s *Service) folderUsers() sharedfolders.Users {
	find := s.d.FindUser
	if find == nil && s.d.Users != nil {
		find = func(ctx context.Context, ref string) (*auth.User, error) { return s.d.Users(ctx, ref) }
	}
	if find == nil {
		return nil
	}
	return folderUsers(find)
}

type folderUsers func(ctx context.Context, ref string) (*auth.User, error)

func (f folderUsers) Find(ctx context.Context, ref string) (sharedfolders.User, error) {
	u, err := f(ctx, ref)
	if errors.Is(err, ErrNoUser) || (err == nil && u == nil) {
		return sharedfolders.User{}, sharedfolders.ErrUnknownUser
	}
	if err != nil {
		return sharedfolders.User{}, err
	}
	name := u.Name
	if name == "" {
		name = u.Email
	}
	return sharedfolders.User{ID: u.ID, Name: name}, nil
}

// isClosed says whether Close has been called.
func (s *Service) isClosed() bool {
	select {
	case <-s.closed:
		return true
	default:
		return false
	}
}

// Close stops every chat. A turn that it cuts short is recorded as interrupted.
func (s *Service) Close(ctx context.Context) error {
	select {
	case <-s.closed:
	default:
		close(s.closed)
	}
	if s.feed != nil {
		s.feed.Close()
	}
	return s.mgr.Close(ctx)
}

// List returns the user's conversations, newest first. Sub-agent runs are
// included; they have Kind session.KindSubAgent and a ParentID.
func (s *Service) List(ctx context.Context, user string, limit int) ([]session.ChatRecord, error) {
	return s.d.Store.ListChats(ctx, principal(user), session.ChatCursor{}, limit)
}

// ListChats returns the user's chats, newest first, without sub-agent runs. The
// limit applies to chats, so a burst of newer runs cannot push older chats out
// of the page: the store is read page by page until enough chats are found.
func (s *Service) ListChats(ctx context.Context, user string, limit int) ([]session.ChatRecord, error) {
	var out []session.ChatRecord
	var cur session.ChatCursor
	for {
		page, err := s.d.Store.ListChats(ctx, principal(user), cur, 200)
		if err != nil {
			return nil, err
		}
		for _, r := range page {
			if r.Kind == session.KindSubAgent {
				continue
			}
			out = append(out, r)
			if limit > 0 && len(out) >= limit {
				return out, nil
			}
		}
		if len(page) < 200 {
			return out, nil
		}
		last := page[len(page)-1]
		cur = session.ChatCursor{Updated: last.Updated, ID: last.ID}
	}
}

func principal(user string) string {
	if user == "" {
		return AnonymousPrincipal
	}
	return user
}

// chatActor is the actor of a chat: its owner, in the group of every user, so
// the chat sees the folders shared with everyone.
func chatActor(p, conv string) workspace.Actor {
	a := files.UserActor(p)
	a.ChatID = conv
	return a
}

func key(p, conv string) string { return p + "\x00" + conv }

func newID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (s *Service) lock(k string) func() {
	s.mu.Lock()
	l := s.locks[k]
	if l == nil {
		l = &keyLock{}
		s.locks[k] = l
	}
	l.n++
	s.mu.Unlock()
	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		s.mu.Lock()
		if l.n--; l.n == 0 {
			delete(s.locks, k)
		}
		s.mu.Unlock()
	}
}

// Create starts a new conversation for user.
func (s *Service) Create(ctx context.Context, user string, in CreateInput) (session.ChatRecord, error) {
	return s.CreateAs(ctx, user, in, Origin{})
}

// CreateAs creates a conversation of user that was started by origin: a person
// (the zero Origin), a trigger or a connector. The agent, when named, must be
// the user's own.
func (s *Service) CreateAs(ctx context.Context, user string, in CreateInput, origin Origin) (session.ChatRecord, error) {
	model := in.Model
	if in.Assistant {
		if in.AgentID != "" {
			return session.ChatRecord{}, ErrAssistantAgent
		}
		if origin.Kind != "" {
			return session.ChatRecord{}, ErrAssistantForbidden
		}
		if err := s.assistantAllowed(ctx, user); err != nil {
			return session.ChatRecord{}, err
		}
	}
	if in.AgentID != "" {
		if s.d.Agents == nil {
			return session.ChatRecord{}, ErrAgentsOff
		}
		ag, err := s.d.Agents.Get(ctx, principal(user), in.AgentID)
		if err != nil {
			return session.ChatRecord{}, err
		}
		if ag.Model != "" {
			model = ag.Model
		}
	}
	if strings.TrimSpace(model) == "" {
		return session.ChatRecord{}, ErrNoModel
	}
	title := in.Title
	if title == "" {
		title = session.DefaultTitle
	}
	now := time.Now().UTC()
	rec := session.ChatRecord{ID: newID(), Principal: principal(user), Title: title, Created: now, Updated: now, Kind: session.KindChat, Model: model,
		AgentID: in.AgentID, Origin: origin.Kind, OriginID: origin.ID, Assistant: in.Assistant}
	if origin.Kind == "" {
		// A chat a person starts gets the instance default. An unattended
		// run follows its agent's network field at each check instead.
		rec.Egress = s.newChatEgress()
	}
	if err := s.d.Store.CreateChat(ctx, rec); err != nil {
		return session.ChatRecord{}, err
	}
	return rec, nil
}

// Get returns a conversation the user owns.
func (s *Service) Get(ctx context.Context, user, conv string) (session.ChatRecord, error) {
	return s.d.Store.GetChat(ctx, principal(user), conv)
}

// Messages returns transcript entries after seq.
func (s *Service) Messages(ctx context.Context, user, conv string, after int64, limit int) ([]session.Entry, error) {
	if _, err := s.Get(ctx, user, conv); err != nil {
		return nil, err
	}
	return s.d.Store.Entries(ctx, conv, after, limit)
}

// Turn returns the turn state of a conversation.
func (s *Service) Turn(ctx context.Context, user, conv string) (store.TurnInfo, error) {
	return s.d.Store.Turn(ctx, principal(user), conv)
}

// Principal returns the owner ID the service uses for a user: the user's ID,
// or the anonymous owner when authentication is off.
func (s *Service) Principal(user string) string { return principal(user) }

// Files returns the workspace of the chats. It is never nil.
func (s *Service) Files() *files.Service { return s.d.Files }

// PageConsents returns the store of the consents to shared pages, or nil.
func (s *Service) PageConsents() pages.ConsentStore { return s.d.PageConsents }

// SharedPagesOn says whether the pages of shared folders are on now.
func (s *Service) SharedPagesOn() bool { return s.d.SharedPagesOn == nil || s.d.SharedPagesOn() }

// UserName returns the name of an account to show to another user: its
// display name when the server has accounts, the principal otherwise.
func (s *Service) UserName(ctx context.Context, id string) string {
	if s.d.Users == nil || id == "" {
		return id
	}
	if u, err := s.d.Users(ctx, id); err == nil && u != nil && u.Name != "" {
		return u.Name
	}
	return id
}

// RemoteModel says whether model runs on a remote service.
func (s *Service) RemoteModel(model string) bool {
	return s.d.RemoteModel != nil && model != "" && s.d.RemoteModel(model)
}

// Persistent reports whether conversations survive a restart of the server.
func (s *Service) Persistent() bool { return s.d.Persistent }

// ImageModel names the model that describes images for a chat whose own model
// cannot look at them, or "" when there is none.
func (s *Service) ImageModel() string { return s.d.Media.VisionModel("") }

// Pending lists the messages waiting for the model.
func (s *Service) Pending(ctx context.Context, user, conv string) ([]store.Pending, error) {
	return s.d.Store.Pending(ctx, principal(user), conv)
}

// EditPending changes a waiting message.
func (s *Service) EditPending(ctx context.Context, user, conv, id, text string) error {
	p := principal(user)
	// The person edits the words; the files of the message stay with it.
	waiting, err := s.d.Store.Pending(ctx, p, conv)
	if err != nil {
		return err
	}
	for _, w := range waiting {
		if w.ID == id {
			_, note, atts := session.SplitMessage(w.Text)
			text = session.ComposeMessage(text, note, atts)
			break
		}
	}
	return s.d.Store.EditPending(ctx, p, conv, id, text)
}

// CancelPending removes a waiting message.
func (s *Service) CancelPending(ctx context.Context, user, conv, id string) error {
	return s.d.Store.CancelPending(ctx, principal(user), conv, id)
}

// Events subscribes to the live events of a conversation. Call cancel when done.
func (s *Service) Events(ctx context.Context, user, conv string) (<-chan session.Event, func(), error) {
	p := principal(user)
	if _, err := s.d.Store.GetChat(ctx, p, conv); err != nil {
		return nil, nil, err
	}
	ch, cancel := s.bus.Subscribe(key(p, conv))
	return ch, cancel, nil
}

// Send gives the conversation a message. With no turn running it starts one.
// With a turn running the message waits and reaches the model, together with
// any others, at the loop's next model request.
func (s *Service) Send(ctx context.Context, user, conv, text string) (SendResult, error) {
	return s.SendWithFiles(ctx, user, conv, text, nil)
}

// SendWithFiles is Send with files attached. The files are saved in the chat's
// folder of the user's workspace and the model gets their paths. With a turn
// running they are saved at once and the message waits like any other, with
// the paths in its text. With files off it is files.ErrUnavailable.
func (s *Service) SendWithFiles(ctx context.Context, user, conv, text string, up []session.Upload) (SendResult, error) {
	rec, err := s.d.Store.GetChat(ctx, principal(user), conv)
	if err != nil {
		return SendResult{}, err
	}
	if rec.Kind == session.KindSubAgent {
		return SendResult{}, ErrSubAgentReadOnly
	}
	if len(up) > 0 && !s.d.Files.Available() {
		return SendResult{}, fmt.Errorf("%w: %s", files.ErrUnavailable, s.d.Files.Unavailable())
	}
	note := ""
	if len(up) > 0 && s.d.Media != nil {
		// Voice notes are transcribed and documents are read before the turn
		// starts, and the text goes to the model with the message; the files
		// are kept as they are. The transcript keeps that text apart from the
		// user's words.
		var perr error
		if note, up, perr = s.d.Media.PrepareUploads(ctx, principal(user), "", up); perr != nil {
			return SendResult{}, perr
		}
		if note, up, perr = s.d.Media.ExtractDocuments(ctx, note, up); perr != nil {
			return SendResult{}, perr
		}
	}
	up, parts, err := s.nativeMedia(rec.Model, up)
	if err != nil {
		return SendResult{}, err
	}
	return s.send(ctx, user, conv, session.Message{Text: text, Note: note, Files: up, Media: parts})
}

// joinPending puts waiting messages and msg, in that order, into one message:
// the words are joined by blank lines, the files and the text read from them
// follow. A waiting message holds the text the model gets (see
// session.ComposeMessage), so its files are split out of it.
func joinPending(taken []store.Pending, msg session.Message) session.Message {
	if len(taken) == 0 {
		return msg
	}
	var (
		texts, notes []string
		atts         []session.Attachment
	)
	for _, t := range taken {
		body, note, a := session.SplitMessage(t.Text)
		if body != "" {
			texts = append(texts, body)
		}
		notes, atts = append(notes, note), append(atts, a...)
	}
	if strings.TrimSpace(msg.Text) != "" {
		texts = append(texts, msg.Text)
	}
	msg.Text = strings.Join(texts, "\n\n")
	msg.Note = strings.Join(notes, "") + msg.Note
	msg.Attachments = append(atts, msg.Attachments...)
	return msg
}

// send is SendWithFiles without the check on the kind: the service itself
// starts sub-agent conversations through it.
func (s *Service) send(ctx context.Context, user, conv string, msg session.Message) (SendResult, error) {
	p := principal(user)
	if _, err := s.d.Store.GetChat(ctx, p, conv); err != nil {
		return SendResult{}, err
	}
	unlock := s.lock(key(p, conv))
	defer unlock()
	runID := newID()
	switch err := s.d.Store.ClaimTurn(ctx, p, conv, runID); {
	case err == nil:
		s.nameAfter(ctx, p, conv, msg.Text)
		// Messages that wait go first: the new one must not overtake them.
		taken, terr := s.d.Store.TakePending(ctx, p, conv)
		if terr != nil {
			_ = s.d.Store.ReleaseTurn(ctx, p, conv, runID, store.TurnFailed)
			return SendResult{}, terr
		}
		ids := make([]string, len(taken))
		for i, t := range taken {
			ids[i] = t.ID
		}
		if err := s.startTurn(ctx, p, conv, runID, joinPending(taken, msg)); err != nil {
			_ = s.d.Store.RestorePending(ctx, p, conv, ids)
			return SendResult{}, err
		}
		if len(ids) > 0 {
			s.confirm(p, conv, ids)
			s.bus.Publish(key(p, conv), session.Event{Kind: session.KindPendingDelivered, IDs: ids})
		}
		return SendResult{Started: true}, nil
	case errors.Is(err, store.ErrTurnRunning):
		atts := msg.Attachments
		if len(msg.Files) > 0 {
			// The files are saved now, so the waiting message can carry their paths.
			saved, aerr := s.attachWhileRunning(ctx, p, conv, msg.Files)
			if aerr != nil {
				return SendResult{}, aerr
			}
			atts = append(slices.Clip(atts), saved...)
		}
		// A waiting message holds the text the model gets; the transcript and
		// the list of waiting messages split the files out of it again.
		pend, err := s.d.Store.AddPending(ctx, p, conv, session.ComposeMessage(msg.Text, msg.Note, atts))
		if err != nil {
			return SendResult{}, err
		}
		// The turn may have ended on another frontend between the claim and the
		// queue. Nothing would start for this message then, so look again; the
		// claim decides who starts it.
		if info, terr := s.d.Store.Turn(ctx, p, conv); terr == nil && !info.Running && info.Last == store.TurnDone {
			if _, err := s.startFromPending(ctx, p, conv); err != nil {
				s.log.Warn("studio: could not start a turn for a message that arrived late", "chat", conv, "error", err)
			}
		}
		return SendResult{PendingID: pend.ID}, nil
	default:
		return SendResult{}, err
	}
}

// attachWhileRunning saves the files of a message that waits for a running
// turn and returns them. The turn may run on another frontend; the files go to
// the shared workspace either way.
func (s *Service) attachWhileRunning(ctx context.Context, p, conv string, up []session.Upload) ([]session.Attachment, error) {
	s.mu.Lock()
	r := s.runs[key(p, conv)]
	s.mu.Unlock()
	if r != nil {
		return r.chat.Attach(ctx, up)
	}
	chat, err := s.chat(ctx, p, conv)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := chat.Close(); cerr != nil {
			s.log.Warn("studio: could not close the chat after saving attachments", "chat", conv, "error", cerr)
		}
	}()
	return chat.Attach(ctx, up)
}

// nameAfter gives a chat that still has the default title one made from its
// first message. The caller holds the claim of the turn and the chat is not
// open, so nothing writes the old title back. A failure only leaves the default.
func (s *Service) nameAfter(ctx context.Context, p, conv, text string) {
	rec, err := s.d.Store.GetChat(ctx, p, conv)
	if err != nil || rec.Title != session.DefaultTitle || rec.Turns > 0 {
		return
	}
	title := session.TitleFrom(text)
	if title == "" {
		return
	}
	rec.Title = title
	if err := s.d.Store.UpdateChat(ctx, rec); err != nil {
		s.log.Warn("studio: could not name a chat after its first message", "chat", conv, "error", err)
	}
}

// SendNow starts a turn from the waiting messages. It is what the user chooses
// after a Stop or a failure; nothing else starts them then.
func (s *Service) SendNow(ctx context.Context, user, conv string) error {
	p := principal(user)
	if _, err := s.d.Store.GetChat(ctx, p, conv); err != nil {
		return err
	}
	unlock := s.lock(key(p, conv))
	defer unlock()
	_, err := s.startFromPending(ctx, p, conv)
	return err
}

// Stop ends the running turn. Waiting messages stay. The turn may run on another
// frontend, which then hears the request over the bus.
func (s *Service) Stop(ctx context.Context, user, conv string) error {
	p := principal(user)
	if _, err := s.d.Store.GetChat(ctx, p, conv); err != nil {
		return err
	}
	// The sub-agents of the chat stop with it, also when its own turn has
	// ended and they run on in the background.
	s.stopChildren(p, conv)
	k := key(p, conv)
	s.mu.Lock()
	local := s.runs[k] != nil
	s.mu.Unlock()
	// The sub-agents of a conversation stop with it, even when the parent has
	// no turn left to stop: a background sub-agent can outlive the turn.
	s.stopChildren(p, conv)
	if !local {
		s.bus.Control(k, ControlMsg{Stop: true})
		return nil
	}
	return s.stopLocal(ctx, p, conv)
}

// stopLocal stops a turn that runs in this process.
func (s *Service) stopLocal(ctx context.Context, p, conv string) error {
	k := key(p, conv)
	s.mu.Lock()
	r := s.runs[k]
	if r != nil {
		r.stopped = true
	}
	s.mu.Unlock()
	if r == nil {
		return nil
	}
	return r.chat.Stop(ctx)
}

// startFromPending starts a turn whose message is the waiting messages joined
// in order. It reports whether it started one. The caller holds the lock.
func (s *Service) startFromPending(ctx context.Context, p, conv string) (bool, error) {
	runID := newID()
	if err := s.d.Store.ClaimTurn(ctx, p, conv, runID); err != nil {
		if errors.Is(err, store.ErrTurnRunning) {
			return false, nil // a turn is running; it takes them at its next request
		}
		return false, err
	}
	taken, err := s.d.Store.TakePending(ctx, p, conv)
	if err != nil || len(taken) == 0 {
		_ = s.d.Store.ReleaseTurn(ctx, p, conv, runID, store.TurnDone)
		if err == nil {
			// A message may have been queued on another frontend while this claim
			// was held: it saw a running turn and did not start one. Look again
			// now that the claim is free.
			if left, perr := s.d.Store.Pending(ctx, p, conv); perr == nil && len(left) > 0 {
				return s.startFromPending(ctx, p, conv)
			}
		}
		return false, err
	}
	ids := make([]string, len(taken))
	for i, t := range taken {
		ids[i] = t.ID
	}
	if err := s.startTurn(ctx, p, conv, runID, joinPending(taken, session.Message{})); err != nil {
		_ = s.d.Store.RestorePending(ctx, p, conv, ids)
		return false, err
	}
	s.confirm(p, conv, ids)
	s.bus.Publish(key(p, conv), session.Event{Kind: session.KindPendingDelivered, IDs: ids})
	return true, nil
}

// startTurn runs msg as a turn the claim runID already covers.
func (s *Service) startTurn(ctx context.Context, p, conv, runID string, msg session.Message) error {
	chat, err := s.chat(ctx, p, conv)
	if err != nil {
		_ = s.d.Store.ReleaseTurn(ctx, p, conv, runID, store.TurnFailed)
		return err
	}
	k := key(p, conv)
	// While the turn runs, tell the store it is alive and listen for a stop
	// requested on another frontend.
	hbCtx, stopBeat := context.WithCancel(context.Background())
	go s.heartbeat(hbCtx, p, conv, runID) // #nosec G118 -- the heartbeat outlives the request and stops with the turn
	stopListening := s.bus.OnControl(k, func(m ControlMsg) {
		if m.Stop {
			if err := s.stopLocal(context.Background(), p, conv); err != nil {
				s.log.Warn("studio: could not stop a turn on request", "chat", conv, "error", err)
			}
		}
	})
	end := func() { stopBeat(); stopListening() }
	s.mu.Lock()
	s.runs[k] = &run{id: runID, started: time.Now(), end: end, chat: chat}
	s.mu.Unlock()
	msg.Media = s.keepVideos(k, msg.Media)
	if err := chat.Send(context.WithoutCancel(ctx), msg); err != nil {
		s.mu.Lock()
		delete(s.runs, k)
		s.mu.Unlock()
		end()
		// s.chat opened the chat; with no turn running nothing else will close it.
		if cerr := chat.Close(); cerr != nil {
			s.log.Warn("studio: could not close the chat after a failed start", "chat", conv, "error", cerr)
		}
		_ = s.d.Store.ReleaseTurn(ctx, p, conv, runID, store.TurnFailed)
		return err
	}
	return nil
}

// heartbeat tells the store that a turn is alive until ctx ends.
func (s *Service) heartbeat(ctx context.Context, p, conv, runID string) {
	every := s.d.HeartbeatEvery
	if every <= 0 {
		every = defaultHeartbeat
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			err := s.d.Store.TouchTurn(ctx, p, conv, runID)
			if err == nil || ctx.Err() != nil {
				continue
			}
			if errors.Is(err, store.ErrNotClaimed) {
				// Another frontend reaped this turn as dead and may have started a
				// new one. Two turns on one chat must not run: stop this one.
				s.log.Warn("studio: a turn lost its claim and is stopping", "chat", conv)
				if serr := s.stopLocal(context.Background(), p, conv); serr != nil {
					s.log.Warn("studio: could not stop a turn that lost its claim", "chat", conv, "error", serr)
				}
				return
			}
			s.log.Warn("studio: could not record a turn's heartbeat", "chat", conv, "error", err)
		}
	}
}

// ReapStale ends the turns that have not beaten for olderThan and tells the
// watchers of each. A frontend that died leaves such turns behind. In a cluster
// one frontend runs this on a timer; a standalone process runs it once at
// startup with a zero duration, because every claim then belongs to a process
// that no longer exists.
func (s *Service) ReapStale(ctx context.Context, olderThan time.Duration) (int, error) {
	reaped, err := s.d.Store.ReapTurns(ctx, time.Now().Add(-olderThan))
	for _, r := range reaped {
		k := key(r.Principal, r.ChatID)
		s.bus.Publish(k, session.Event{Kind: session.KindError, Text: "The turn was interrupted."})
		s.bus.Publish(k, session.Event{Kind: session.KindTurnEnd})
	}
	return len(reaped), err
}

// chat opens the conversation in the loop and makes sure its events are read.
func (s *Service) chat(ctx context.Context, p, conv string) (*session.Chat, error) {
	if s.d.Files.Available() {
		// The loop needs the user's home before it opens a chat.
		if err := s.d.Files.EnsureHome(ctx, p); err != nil {
			return nil, err
		}
	}
	chat, err := s.mgr.Open(ctx, chatActor(p, conv), conv)
	if err != nil {
		return nil, err
	}
	k := key(p, conv)
	// A chat that was closed after its last turn has a new event channel when
	// it reopens, so the pump is told apart by its channel, not by the chat:
	// the pump of the old channel may not have noticed that it ended yet.
	evs := chat.Events()
	s.mu.Lock()
	start := s.pumps[k] != evs
	s.pumps[k] = evs
	s.mu.Unlock()
	if start {
		go s.pump(chat, evs, p, conv)
	}
	return chat, nil
}

// pump reads the events of one chat for its lifetime, publishes them, and
// settles the turn when it ends.
func (s *Service) pump(chat *session.Chat, evs <-chan session.Event, p, conv string) {
	k := key(p, conv)
	defer func() {
		s.mu.Lock()
		if s.pumps[k] == evs { // a newer pump may have taken the place
			delete(s.pumps, k)
		}
		s.mu.Unlock()
	}()
	for ev := range evs {
		if ev.Kind == session.KindTurnEnd {
			// The end is recorded before it is announced: a client that reads
			// the chat when it sees turn_end must not find it still running.
			next := s.finishTurn(chat, p, conv)
			s.bus.Publish(k, ev)
			next()
			continue
		}
		s.bus.Publish(k, ev)
		switch ev.Kind {
		case session.KindError:
			// Only the error that ended the turn fails it. A failed save or a
			// recovered tool panic leaves a turn that finished normally.
			if !ev.Fatal {
				break
			}
			s.mu.Lock()
			if r := s.runs[k]; r != nil {
				r.failed = true
			}
			s.mu.Unlock()
		}
	}
}

// finishTurn records how the turn ended. It returns what to run once the end
// is announced: after a normal end only, it starts the next turn from messages
// that arrived too late for the last request.
func (s *Service) finishTurn(chat *session.Chat, p, conv string) (next func()) {
	next = func() {}
	ctx := context.Background()
	k := key(p, conv)
	unlock := s.lock(k)
	defer unlock()
	s.mu.Lock()
	r := s.runs[k]
	delete(s.runs, k)
	s.mu.Unlock()
	if r == nil {
		return next
	}
	if r.end != nil {
		r.end()
	}
	// Another frontend may run the next turn, so nothing of this chat is kept in
	// memory past its turn, and is saved before the claim is given up: the next turn here loads the record again.
	if err := chat.Close(); err != nil {
		s.log.Warn("studio: could not close the chat after its turn", "chat", conv, "error", err)
	}
	status := store.TurnDone
	switch {
	case r.stopped:
		status = store.TurnStopped
	case r.failed:
		status = store.TurnFailed
	case s.isClosed():
		// The server is going down: the turn was cut short, its text is partial,
		// and nothing starts after it. The owner of a run is told.
		status = store.TurnInterrupted
	}
	if err := s.releaseTurn(ctx, p, conv, r.id, status); err != nil {
		s.log.Warn("studio: could not release the turn", "chat", conv, "error", err)
		// The turn is over all the same, and a parent may wait for it. If the
		// claim was reaped, whoever holds the chat now decides what runs next;
		// if the store is failing, the reaper frees the chat later. Either way
		// this one only cleans up after itself and reports a terminal status.
		status = store.TurnInterrupted
	}
	s.observeTurn(ctx, p, conv, r.started, outcomeOf(r.stopped, r.failed || status == store.TurnInterrupted))
	s.notifyWaiters(k, status)
	if status != store.TurnDone {
		// A stopped or failed turn takes its running sub-agents with it, and the
		// waiting messages stay for the user to decide.
		s.stopChildren(p, conv)
		return next
	}
	// The next turn is started from another goroutine: this one reads the events
	// that the new turn will produce.
	return func() {
		go func() {
			unlock := s.lock(k)
			defer unlock()
			started, err := s.startFromPending(ctx, p, conv)
			if err != nil {
				s.log.Warn("studio: could not start the next turn", "chat", conv, "error", err)
				return
			}
			if started {
				// On a chat worker the next turn gets the same bound as the
				// turn that a frontend started: nothing else watches it when
				// that frontend is gone.
				s.limitWorkerRun(ctx, p, conv)
			}
		}()
	}
}

// releaseTurn ends the claim. A store can fail for a moment (a locked SQLite
// database), and a chat left claimed stays "running" until the reaper finds
// it, so the release is tried a few times first.
func (s *Service) releaseTurn(ctx context.Context, p, conv, runID string, status store.TurnStatus) error {
	const attempts = 5
	wait := 25 * time.Millisecond
	var err error
	for i := 0; i < attempts; i++ {
		if err = s.d.Store.ReleaseTurn(ctx, p, conv, runID, status); err == nil || errors.Is(err, store.ErrNotClaimed) {
			return err
		}
		if i == attempts-1 {
			break
		}
		select {
		case <-time.After(wait):
		case <-s.closed:
			return err
		}
		wait *= 2
	}
	return err
}

// inputs is the source of pending messages for one chat's harness. It runs
// before each model request: it takes what is waiting, and if the request
// fails, puts it back.
func (s *Service) inputs(a workspace.Actor) func() ([]string, func(error)) {
	return func() ([]string, func(error)) {
		ctx := context.Background()
		taken, err := s.d.Store.TakePending(ctx, a.Principal, a.ChatID)
		if err != nil || len(taken) == 0 {
			return nil, nil
		}
		texts := make([]string, len(taken))
		ids := make([]string, len(taken))
		for i, t := range taken {
			texts[i], ids[i] = t.Text, t.ID
		}
		// The loop counts a turn in the record only when it ends, so the turn
		// that runs now is the next one.
		turn := 0
		if rec, gerr := s.d.Store.GetChat(ctx, a.Principal, a.ChatID); gerr == nil {
			turn = rec.Turns + 1
		}
		return texts, func(err error) {
			if err != nil {
				if rerr := s.d.Store.RestorePending(ctx, a.Principal, a.ChatID, ids); rerr != nil {
					s.log.Warn("studio: could not put pending messages back", "chat", a.ChatID, "error", rerr)
				}
				return
			}
			for _, t := range texts {
				// The transcript keeps the words apart from the files.
				body, note, atts := session.SplitMessage(t)
				e := session.Entry{Turn: turn, Role: session.RoleUser, Text: body, Note: note, Attachments: atts}
				if _, aerr := s.d.Store.AppendEntry(ctx, a.ChatID, e); aerr != nil {
					s.log.Warn("studio: could not record a delivered message", "chat", a.ChatID, "error", aerr)
				}
			}
			s.confirm(a.Principal, a.ChatID, ids)
			s.bus.Publish(key(a.Principal, a.ChatID), session.Event{Kind: session.KindPendingDelivered, IDs: ids})
		}
	}
}

// confirm tells the store that the messages are in the transcript, so a turn
// that is reaped later does not give them back.
func (s *Service) confirm(p, conv string, ids []string) {
	if err := s.d.Store.ConfirmPending(context.Background(), p, conv, ids); err != nil {
		s.log.Warn("studio: could not confirm delivered messages", "chat", conv, "error", err)
	}
}

// llmFor gives the harness of a chat its model client, acting as the chat's owner.
func (s *Service) llmFor(a workspace.Actor) (nibharness.Model, error) {
	ctx := context.Background()
	rec, err := s.d.Store.GetChat(ctx, a.Principal, a.ChatID)
	if err != nil {
		return nibharness.Model{}, err
	}
	var user *auth.User
	if s.d.Users != nil && a.Principal != AnonymousPrincipal {
		if user, err = s.d.Users(ctx, a.Principal); err != nil {
			return nibharness.Model{}, fmt.Errorf("studio: load the chat's user: %w", err)
		}
	}
	set := rec.Settings
	limit := set.ContextSize
	if limit == 0 && s.d.ContextTokens != nil {
		limit = s.d.ContextTokens(rec.Model)
	}
	params := provider.Params{
		Temperature: set.Temperature, TopP: set.TopP, TopK: set.TopK, ReasoningEffort: set.ReasoningEffort,
		OnUsage:    func(u provider.Usage) { s.reportUsage(a, u, rec.Model, limit) },
		TakeVideos: func() []string { return s.takeVideos(a.Principal, a.ChatID) },
		// An image goes to the model with its message, not with every later one.
		OnlyLastImages: true,
	}
	m := nibharness.Model{
		LLM: s.d.Provider.LLMWith(user, rec.Model, params), Name: rec.Model,
		ContextTokens: limit, ReasoningEffort: set.ReasoningEffort,
	}
	if s.d.AgentOptions != nil {
		m.Agent = s.d.AgentOptions(rec.Model)
	}
	return m, nil
}

// reportUsage tells the watchers of the chat what one model request used.
func (s *Service) reportUsage(a workspace.Actor, u provider.Usage, model string, limit int) {
	s.bus.Publish(key(a.Principal, a.ChatID), session.Event{Kind: session.KindUsage, Usage: &session.UsageEvent{
		Used: u.PromptTokens, Limit: limit, Completion: u.CompletionTokens, TokensPerSecond: u.TokensPerSecond,
		Model: model, Node: u.Node, PromptMillis: u.PromptMillis,
	}})
}

// Delete removes a conversation and its transcript. A running turn is stopped
// first, and so are its running sub-agents. Another user's conversation is
// session.ErrNotFound.
func (s *Service) Delete(ctx context.Context, user, conv string) error {
	p := principal(user)
	if _, err := s.d.Store.GetChat(ctx, p, conv); err != nil {
		return err
	}
	if err := s.Stop(ctx, p, conv); err != nil {
		return err
	}
	s.stopChildren(p, conv)
	return s.mgr.Delete(ctx, chatActor(p, conv), conv)
}

// MCPConnectors returns the service that keeps the users' MCP connectors, or
// nil when the chats have none.
func (s *Service) MCPConnectors() *mcpconns.Service { return s.d.MCPConnectors }
