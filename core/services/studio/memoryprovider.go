package studio

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path"
	"slices"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/builtins"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// What the host does itself with a provider that serves the notes, on top of
// the tools it gives the model (providerslots.go, providercalls.go):
//
//   - at the start of a turn it calls the context role and puts the answer in
//     front of the message as data it does not trust;
//   - a write of a role tool follows the save level of the person, the grant of
//     the saved agent and the messaging rules, exactly as a note of the
//     built-in provider does;
//   - the export and the forget of the settings page go to the provider.
//
// A provider that fails does not fail the chat: the turn runs without notes,
// the model hears one line, and after repeated failures the host leaves the
// provider alone for a while (the circuit breaker).

// The roles of the notes slot the host calls itself.
const (
	roleContext = "context"
	roleExport  = "export"
	roleForget  = "forget"
	roleWrite   = "write"
)

// Errors of the export and the forget of a provider.
var (
	// ErrNoForget means the notes of the built-in provider are files that the
	// person deletes in Files, and the host has no forget for them.
	ErrNoForget = errors.New("studio: there is no forget for the notes of this provider")
)

// opaqueRef is a stable reference that a provider can key on and that says
// nothing about the person: names, ids of the host and chat text never reach a
// provider.
func opaqueRef(kind, id string) string {
	sum := sha256.Sum256([]byte("ref/" + kind + "/" + id))
	return hex.EncodeToString(sum[:12])
}

// providerContextArgs are the facts a context call offers. The host sends the
// ones the tool declares.
func providerContextArgs(rec session.ChatRecord, message string, scopes []string, budget int) map[string]any {
	if len(message) > 1024 {
		message = cutText(message, 1024)
	}
	origin := "studio"
	if rec.Origin == session.OriginConnector {
		origin = "connector"
	}
	args := map[string]any{"query": message, "scopes": scopes, "budgetBytes": budget, "origin": origin,
		"userRef": opaqueRef("user", rec.Principal)}
	if rec.AgentID != "" {
		args["agentRef"] = opaqueRef("agent", rec.Principal+"/"+rec.AgentID)
	}
	if rec.Origin == session.OriginConnector {
		args["conversationRef"] = opaqueRef("conversation", rec.Principal+"/"+rec.OriginID+"/"+rec.ID)
	}
	return args
}

// providerContext asks the provider that serves the notes for the text of this
// turn. block is the wrapped text for the prompt of the turn. note is one line
// for the model when the provider could not answer. Both are empty when the
// built-in provider serves, the provider has no context role, or this chat
// reads no notes.
func (s *Service) providerContext(ctx context.Context, rec session.ChatRecord, sc *scope, legacy legacyMemory) (block, note string) {
	// Only a turn with a message asks. The hook that primes a chat has none,
	// and a provider is not called for nothing.
	if session.TurnMessage(ctx) == "" {
		return "", ""
	}
	res := s.memoryResolution(ctx, rec.Principal, legacy)
	if res.Addon() == "" || !s.memoryReads(ctx, rec, sc, legacy) {
		return "", ""
	}
	_, decl, err := s.declarationOf(ctx, rec.Principal, res.Addon(), MemorySlot)
	if err != nil || len(decl.Roles[roleContext]) == 0 {
		return "", ""
	}
	choice := ""
	if sc != nil {
		choice = sc.memory
	}
	scopes := []string{string(contract.ScopeUser), string(contract.ScopeAgent)}
	if rec.Origin == session.OriginConnector {
		scopes = append(scopes, string(contract.ScopeChannel))
	}
	scopes = scopesOfChoice(scopes, choice, rec.AgentID != "")
	if !s.personalNotesOpen(rec) {
		// Only the notes of this conversation: the provider is not asked for
		// the owner's.
		scopes = slices.DeleteFunc(scopes, func(sc string) bool { return sc != string(contract.ScopeChannel) })
	}
	if len(scopes) == 0 {
		return "", ""
	}
	conv := rec.ID
	ans, err := s.runProviderRole(ctx, res, providerCall{slot: MemorySlot, role: roleContext, user: rec.Principal, conv: conv,
		args:    providerContextArgs(rec, session.TurnMessage(ctx), scopes, s.providerTimings.maxBytes),
		timeout: s.providerTimings.context, maxBytes: s.providerTimings.maxBytes})
	if err != nil {
		if ctx.Err() != nil {
			return "", ""
		}
		return "", "The notes could not be loaded this turn because the add-on that serves them did not answer. Go on without notes, and do not claim to remember anything from earlier chats."
	}
	title := res.AddonTitle
	if title == "" {
		title = res.Addon()
	}
	return wrapProviderText("Notes the user saved", title, ans.Text), ""
}

// scopesOfChoice narrows the scopes a provider may return to what the choice
// of the saved agent reads, with the rules of memorySourcesFor.
func scopesOfChoice(scopes []string, choice string, hasAgent bool) []string {
	var out []string
	for _, sc := range scopes {
		switch {
		case choice == agents.MemoryNone:
		case choice == agents.MemoryOwn && sc == string(contract.ScopeUser):
		case choice == agents.MemoryMine && sc == string(contract.ScopeAgent):
		case sc == string(contract.ScopeAgent) && !hasAgent:
		default:
			out = append(out, sc)
		}
	}
	return out
}

// providerRoleRef says which role of which slot a tool of a serving add-on
// fills, and the facts the add-on declared for the slot.
type providerRoleRef struct {
	Slot, Role string
	Props      map[string]any
}

// memoryWriteLevel is the level that applies to a write of the chat.
func (s *Service) memoryWriteLevel(ctx context.Context, rec session.ChatRecord) string {
	return s.changes.saveModeOf(ctx, s.chatFor(ctx, rec))
}

// chatFor is the view of a chat that the rules of the built-in tools read.
func (s *Service) chatFor(ctx context.Context, rec session.ChatRecord) builtins.Chat {
	offList, onList := s.switchesOf(ctx, rec)
	allow, audit := s.turnAllow(rec.ID)
	c := builtins.Chat{Actor: s.chatActorFor(ctx, rec), Origin: rec.Origin, OriginID: rec.OriginID, Agent: rec.AgentID != "", AgentID: rec.AgentID,
		Model: rec.Model, SubAgent: rec.Kind == session.KindSubAgent, GroupsOff: offList, GroupsOn: onList, Grants: s.chatGrants(ctx, rec)}
	if rec.Origin == session.OriginConnector {
		c.Allow, c.Audit = allow, audit
	}
	return c
}

// remoteWithoutUndo reports a provider that sends notes away and says nothing
// the host can undo: its writes are never applied unasked.
func remoteWithoutUndo(props map[string]any) bool {
	remote, _ := props["remote"].(bool)
	undo, _ := props["undo"].(string)
	return remote && undo != "host" && undo != "provider"
}

// gateMemoryWrite holds the write tool of the provider that serves the notes
// to the rules of the host. It reports false when the tool must not be offered.
//
//   - Never takes the tool away. A saved agent needs the grant of memory
//     writes, and a sender in a messaging chat needs the capability.
//   - The level decides the rest: only Save it and tell me applies a write at
//     once, and only for a small note without a secret in it, within the
//     limit of a turn, and from a provider that is local or can undo. Every
//     other write is stored as a proposal that the person confirms.
func (s *Service) gateMemoryWrite(rec session.ChatRecord, t session.Tool, ref providerRoleRef, turn *autoTurn) (session.Tool, bool) {
	ctx := context.Background()
	c := s.chatFor(ctx, rec)
	if rec.AgentID != "" && !c.HoldsGrant(contract.CapMemoryWrite) {
		return t, false
	}
	if s.memoryWriteLevel(ctx, rec) == SaveNever {
		return t, false
	}
	if rec.Origin == session.OriginConnector {
		var ok bool
		if t, ok, _ = s.gateAddonTool(t, contract.CapMemoryWrite, rec.ID, false); !ok {
			return t, false
		}
	}
	run := t.Run
	t.Run = func(ctx context.Context, raw json.RawMessage) (string, error) {
		level := s.memoryWriteLevel(ctx, rec)
		if level == SaveNever {
			return "", errors.New("the person turned off saving to their memory, so nothing was sent. Do not try again; say so in plain words if they ask.")
		}
		hold, why := level != SaveAuto, "the save level asks for a confirmation"
		if !hold && remoteWithoutUndo(ref.Props) {
			hold, why = true, "the provider keeps notes elsewhere and cannot undo them"
		}
		if !hold {
			if ok, w := autoFits(string(raw), turn); !ok {
				hold, why = true, w
			}
		}
		s.log.Info("studio: provider write", "slot", ref.Slot, "role", ref.Role, "level", level, "held", hold, "chat", rec.ID, "user", rec.Principal)
		if hold {
			ctx = holdForPerson(ctx)
			out, err := run(ctx, raw)
			if err != nil {
				return out, err
			}
			return out + " It was not saved at once because " + why + ": the person must confirm it.", nil
		}
		return run(ctx, raw)
	}
	return t, true
}

// ErrProviderNoExport means the provider that serves the notes has no export.
var ErrProviderNoExport = errors.New("studio: this provider has no export; use its own site")

// exportFromProvider writes the export of the provider as a zip file. The
// export tool answers files ({"files":[{"path","content"}]}) or any other JSON,
// which is kept as one file. It reports the number of files.
func (s *Service) exportFromProvider(ctx context.Context, user string, res SlotResolution, w io.Writer) (int, error) {
	_, decl, err := s.declarationOf(ctx, user, res.Addon(), MemorySlot)
	if err != nil || len(decl.Roles[roleExport]) == 0 {
		return 0, ErrProviderNoExport
	}
	ans, err := s.runProviderRole(ctx, res, providerCall{slot: MemorySlot, role: roleExport, user: user,
		args: map[string]any{}, timeout: s.providerTimings.tool})
	if err != nil {
		return 0, err
	}
	var doc struct {
		Files []struct{ Path, Content string } `json:"files"`
	}
	zw := zip.NewWriter(w)
	n := 0
	add := func(name, body string) error {
		fw, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = io.WriteString(fw, body)
		n++
		return err
	}
	if json.Unmarshal(ans.Payload, &doc) == nil && len(doc.Files) > 0 {
		for _, f := range doc.Files {
			name := strings.TrimPrefix(path.Clean("/"+f.Path), "/")
			if name == "" || name == "." {
				continue
			}
			if err := add(name, f.Content); err != nil {
				return n, err
			}
		}
	} else if err := add("export.json", string(ans.Payload)); err != nil {
		return n, err
	}
	if n == 0 {
		return 0, ErrNoNotes
	}
	return n, zw.Close()
}

// ForgetResult is what a forget of a provider did.
type ForgetResult struct {
	// Done is true when the provider forgot. Otherwise the change waits for the
	// person to confirm it on the Add-ons page.
	Done bool `json:"done"`
	// Proposal names the change that waits, when Done is false.
	Proposal string `json:"proposal,omitempty"`
}

// ForgetMemory asks the provider that serves the notes to forget the notes of
// a scope (user, agent or channel; empty is everything it keeps) and of a key
// (the agent or the conversation), through its forget tool. The built-in
// provider has none: its notes are files. resource names the resource the tool
// works on, when its schema has one.
func (s *Service) ForgetMemory(ctx context.Context, user, scope, key, resource string) (ForgetResult, error) {
	res := s.memoryResolution(ctx, user, legacyMemoryOf(s.addonContextSets(ctx, user)))
	if res.Addon() == "" {
		return ForgetResult{}, ErrNoForget
	}
	_, decl, err := s.declarationOf(ctx, user, res.Addon(), MemorySlot)
	if err != nil || len(decl.Roles[roleForget]) == 0 {
		return ForgetResult{}, ErrNoForget
	}
	args := map[string]any{"group": scope, "key": key}
	if resource != "" {
		args[contract.ResourceArgument] = resource
	}
	ans, err := s.runProviderRole(ctx, res, providerCall{slot: MemorySlot, role: roleForget, user: user, args: args, timeout: s.providerTimings.tool})
	if err != nil {
		return ForgetResult{}, err
	}
	if ans.Kind == "proposal" {
		return ForgetResult{Proposal: ans.Proposal}, nil
	}
	return ForgetResult{Done: true}, nil
}
