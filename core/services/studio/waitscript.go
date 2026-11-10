package studio

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/mudler/nib/watch"

	"github.com/mudler/LocalAI/core/services/studio/loops"
	"github.com/mudler/LocalAI/core/services/studio/sandbox/runner"
	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/core/services/studio/workspace"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// A wait on a script runs a shell command at each check, with no model call.
// The command runs where the chat's bash runs: in the Sandbox add-on when one
// serves bash for the chat, else in the built-in bash on this machine. It is
// the same code path, with three differences:
//
//   - A check changes no file. It runs read-only (contract.ExecutionCall
//     ReadOnly, hostexec.Runner.RunReadOnly): what it writes is refused and no
//     change set is made.
//   - A check that exits with a non-zero status made a check. The status and
//     the output feed the condition. Only a command that does not finish in
//     time, or that cannot be run, fails a check.
//   - In the built-in bash with the mode ask, the administrator allows the
//     whole command once, when the wait is armed. The checks then run with no
//     card.

// ReadOnlyHostExec is a HostExec that can also run a command that must not
// change the user's files. *hostexec.Runner implements it. A server whose
// HostExec does not cannot run a wait on a script in the built-in bash.
type ReadOnlyHostExec interface {
	HostExec
	// RunReadOnly runs one call of bash that changes no file of the user.
	RunReadOnly(ctx context.Context, a workspace.Actor, args json.RawMessage) (string, error)
}

// scriptTimeout bounds one check of a script. It is the default timeout of
// bash, longer than the 30 s of a tool or a page, because a command may do
// real work.
const scriptTimeout = runner.DefaultTimeout

// scriptOutputCap is how much of the output of a script a check keeps. It is
// the 64 KiB that bash returns, less room for the status line that closes the
// output, which must never be cut off.
const scriptOutputCap = runner.MaxOutput - 512

// errScriptUnavailable means this node has no way to run the script of a wait
// now: it has no host exec and no sandbox that may serve the chat, or the
// chat lost its bash. It ends the wait: the checks of another node may be able
// to run it, but the owner is told plainly instead of the wait polling in vain.
type errScriptUnavailable struct{ reason string }

func (e *errScriptUnavailable) Error() string { return e.reason }

func unavailablef(format string, a ...any) error {
	return &errScriptUnavailable{reason: fmt.Sprintf(format, a...)}
}

// scriptProvider is what runs the commands of a chat.
type scriptProvider struct {
	// run runs one call of bash that changes no file.
	run func(ctx context.Context, args json.RawMessage) (string, error)
	// host is true for the built-in bash on this machine.
	host bool
}

// scriptProviderOf finds what runs the commands of the chat of rec for a
// wait. It follows the tool list of the chat's turns: the add-on that serves
// bash for the chat (SelectExecutionProvider has chosen it among the
// installed ones, and the chat's switches and the agent's grants apply), else
// the built-in bash, built by buildHostBash with the same refusals (a fenced
// chat, bash off, files off, an unattended run without an agent, the mode
// ask). The caller holds the execution scope.
func (s *Service) scriptProviderOf(ctx context.Context, rec session.ChatRecord) (scriptProvider, error) {
	if rec.Kind == session.KindSubAgent {
		return scriptProvider{}, unavailablef("a sub-agent cannot arm a wait on a script; the chat that started it can")
	}
	a := s.chatActorFor(ctx, rec)
	tools, err := s.hostTools(ctx, a)
	if err != nil {
		return scriptProvider{}, fmt.Errorf("could not list the tools of this chat: %w", err)
	}
	for _, t := range dropHidden(tools, s.hiddenFor(ctx, rec)) {
		if t.Name == commandTool && t.Run != nil && t.Provider != "" && t.Provider != BuiltinProvider && len(t.StandInFor) == 0 {
			run := t.Run
			return scriptProvider{run: func(ctx context.Context, args json.RawMessage) (string, error) {
				return run(contract.WithExecutionCall(ctx, contract.ExecutionCall{ReadOnly: true}), args)
			}}, nil
		}
	}
	for _, b := range s.builtinsFor(ctx, a, rec, false) {
		if b.ID != commandTool {
			continue
		}
		if !b.OK {
			why := b.Reason
			if why == "" {
				why = "it has no tool that runs commands"
			}
			return scriptProvider{}, unavailablef("this chat cannot run commands (%s), so it cannot run a script", why)
		}
		hx, ok := s.d.HostExec.(ReadOnlyHostExec)
		if !ok {
			return scriptProvider{}, unavailablef("the built-in bash of this server cannot run a command that leaves the files alone, and no add-on serves this chat")
		}
		return scriptProvider{host: true, run: func(ctx context.Context, args json.RawMessage) (string, error) {
			return hx.RunReadOnly(ctx, a, args)
		}}, nil
	}
	return scriptProvider{}, unavailablef("this chat has no tool that runs commands, so it cannot run a script")
}

// armScript refuses what cannot be armed as a script wait, and asks for the
// one approval that the mode ask needs. It runs when wait_for is called, in the
// turn of the chat, so a card is shown to the person.
func (h *Loops) armScript(ctx context.Context, a workspace.Actor, script string) error {
	switch {
	case strings.TrimSpace(script) == "":
		return errors.New("script is empty")
	case strings.ContainsRune(script, 0):
		return errors.New("script contains a NUL byte")
	case len(script) > loops.MaxScriptBytes:
		return fmt.Errorf("script is longer than %d bytes: put it in a file and run that file", loops.MaxScriptBytes)
	}
	s := h.svc
	rec, err := s.d.Store.GetChat(ctx, a.Principal, a.ChatID)
	if err != nil {
		return err
	}
	ctx, closeScope := s.executionScope(ctx, a.Principal, a.ChatID)
	defer closeScope()
	prov, err := s.scriptProviderOf(ctx, rec)
	if err != nil {
		return err
	}
	if prov.host && s.hostExecMode() == "ask" {
		_, refusal, err := s.approveHostExec(ctx, a, script)
		if err != nil {
			return err
		}
		if refusal != "" {
			return errors.New(strings.TrimSpace(refusal))
		}
	}
	return nil
}

// scriptExec returns the function that runs the script of a wait for a check:
// the ExecFunc of watch.Runner. A non-zero status is a value (watch.ExecFunc);
// the error is set only when the script did not run to the end.
func (s *Service) scriptExec(ctx context.Context, rec session.ChatRecord) (watch.ExecFunc, error) {
	prov, err := s.scriptProviderOf(ctx, rec)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, script string) (string, int, error) {
		tag := scriptTag()
		args, err := json.Marshal(map[string]any{"command": wrapScript(script, tag), "timeoutSeconds": int(scriptTimeout.Seconds())})
		if err != nil {
			return "", 0, err
		}
		text, err := prov.run(ctx, args)
		if err != nil {
			// The text of the error is not shown as it is: it may carry what
			// the shell echoed of the command.
			if ctx.Err() != nil {
				return "", 0, ctx.Err()
			}
			return "", 0, errors.New("the command could not be run")
		}
		return parseScriptResult(text, tag)
	}, nil
}

// scriptTag is the line that ends the output of a check, followed by the exit
// status. It is new for each check, so that the output of the script cannot
// make up a status.
func scriptTag() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "LOCALAI_WAIT_STATUS_" + hex.EncodeToString(b) + "="
}

// wrapScript wraps the command so that the exit status comes back as a value:
// bash returns text, and its own exit-code line cannot be told from the output.
// The command runs in a subshell with stderr on stdout, cut to scriptOutputCap
// (the rest is drained, so the command is not stopped by a closed pipe), and
// the status line follows.
func wrapScript(script, tag string) string {
	return "(\n" + script + "\n) 2>&1 | { head -c " + strconv.Itoa(scriptOutputCap) + "; cat >/dev/null; }\n" +
		"__rc=${PIPESTATUS[0]}\nprintf '\\n%s%s\\n' '" + tag + "' \"$__rc\"\n"
}

// Texts of bash that say that the command did not run at all, not that it ran
// and failed.
var scriptNotRun = []string{"This host cannot run commands", "This server cannot run commands", "Not run:"}

// parseScriptResult reads the text that bash returned for a wrapped script.
func parseScriptResult(text, tag string) (string, int, error) {
	if i := strings.LastIndex(text, tag); i >= 0 {
		out := strings.TrimSuffix(text[:i], "\n")
		rest := text[i+len(tag):]
		code, _, _ := strings.Cut(rest, "\n")
		n, err := strconv.Atoi(strings.TrimSpace(code))
		if err != nil {
			return "", 0, errors.New("the command gave no exit status")
		}
		return out, n, nil
	}
	trimmed := strings.TrimSpace(text)
	switch {
	case strings.HasSuffix(trimmed, "seconds]") && strings.Contains(trimmed, "[stopped after "):
		return "", 0, errors.New("the command did not finish in time")
	}
	for _, p := range scriptNotRun {
		if strings.HasPrefix(trimmed, p) {
			return "", 0, unavailablef("the command could not be started: %s", firstLine(trimmed))
		}
	}
	// The shell stopped before the wrapper ended, which is a syntax error of
	// the command: bash says its status after the output.
	if i := strings.LastIndex(trimmed, "[exit code "); i >= 0 && strings.HasSuffix(trimmed, "]") {
		if n, err := strconv.Atoi(strings.TrimSuffix(trimmed[i+len("[exit code "):], "]")); err == nil {
			return strings.TrimSpace(trimmed[:i]), n, nil
		}
	}
	return "", 0, errors.New("the command gave no exit status")
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return watch.Cap(line, 200)
}

// checkScript makes one check of a wait on a script. The charge on the owner's
// budget comes before the run. It returns the result of the check; the second
// value is set when the wait must end at once and says why (the node cannot
// run the script, or the budget is used up).
func (h *Loops) checkScript(ctx context.Context, l loops.Loop, lim LoopLimits) (watch.Result, *scriptEnd) {
	rec, err := h.svc.d.Store.GetChat(ctx, l.Owner, l.ChatID)
	if err != nil {
		return watch.Result{Err: err}, nil
	}
	ctx, closeScope := h.svc.executionScope(ctx, l.Owner, l.ChatID)
	defer closeScope()
	exec, err := h.svc.scriptExec(ctx, rec)
	var na *errScriptUnavailable
	if errors.As(err, &na) {
		return watch.Result{}, &scriptEnd{why: "the script cannot run on this node", detail: "the script cannot run on this node: " + na.reason + ". It was not run, so the condition is not known."}
	}
	if err != nil {
		return watch.Result{Err: err}, nil
	}
	took, err := h.o.Store.TakeCheck(ctx, l.Owner, loops.Day(h.o.Clock()), lim.DailyScriptChecks)
	switch {
	case err != nil:
		return watch.Result{Err: fmt.Errorf("could not count the check: %w", err)}, nil
	case !took:
		return watch.Result{}, &scriptEnd{why: "the daily budget of script checks is used up", detail: fmt.Sprintf("the server stopped checking: today's limit of %d script checks (UTC) is used up, so the condition is not known.", lim.DailyScriptChecks)}
	}
	res := watch.Runner{Timeout: scriptTimeout, MaxOutput: scriptOutputCap, Exec: func(ctx context.Context, script string) (string, int, error) {
		out, code, err := exec(ctx, script)
		var na *errScriptUnavailable
		if errors.As(err, &na) {
			// The shell told that it did not run the command: the same end.
			return "", 0, na
		}
		return out, code, err
	}}.Run(ctx, watch.Source{Script: l.Watch.Script})
	if errors.As(res.Err, &na) {
		return watch.Result{}, &scriptEnd{why: "the script cannot run on this node", detail: "the script cannot run on this node: " + na.reason + ". It was not run, so the condition is not known."}
	}
	return res, nil
}

// scriptEnd is the end of a wait that is not a verdict of the condition.
type scriptEnd struct{ detail, why string }
