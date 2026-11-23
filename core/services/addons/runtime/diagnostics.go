package runtime

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// LaunchFailure is the reason a launch of an add-on process failed. It is an
// ErrRuntime and its Error text stays that generic text: the answer to a
// client never changes with it. Only the owner log shows the specific cause,
// through Cause. Stage is a short stable token and Reason is one bounded
// line that holds no credential and no host path beyond a base name.
type LaunchFailure struct {
	Stage  string
	Reason string
}

func (f *LaunchFailure) Error() string { return ErrRuntime.Error() }
func (f *LaunchFailure) Unwrap() error { return ErrRuntime }

// Cause is the text for the owner log: "<stage>: <reason>".
func (f *LaunchFailure) Cause() string { return f.Stage + ": " + f.Reason }

// Cause returns the text of an error for the owner log. A launch failure
// adds its stage and reason to the generic text; any other error is its own
// text. The text is for logs only.
func Cause(e error) string {
	var f *LaunchFailure
	if errors.As(e, &f) {
		return ErrRuntime.Error() + ": " + f.Cause()
	}
	return e.Error()
}

// The stages of a failed launch.
const (
	StageStartRefused         = "start_refused"
	StageManifestInvalid      = "manifest_invalid"
	StageReceiptUnreadable    = "receipt_unreadable"
	StageReceiptMismatch      = "receipt_mismatch"
	StageExecutableMissing    = "executable_missing"
	StageHostEndpoint         = "host_endpoint"
	StageChannelOpen          = "channel_open"
	StageRandomSource         = "random_source"
	StageDataDir              = "data_dir"
	StageOutputPipe           = "output_pipe"
	StageLedgerWrite          = "ledger_write"
	StageProcessStart         = "process_start"
	StageProcessExited        = "process_exited_before_handshake"
	StageHandshakeTimeout     = "handshake_timeout"
	StageHandshakeProtocol    = "handshake_protocol"
	StageStartupWrite         = "startup_write"
	StageRegistrationRejected = "registration_rejected"
	StageReadinessTimeout     = "readiness_timeout"
	StageProcessIdentity      = "process_identity"
	StageLaunchCanceled       = "launch_canceled"
)

func failed(stage, reason string) error { return &LaunchFailure{Stage: stage, Reason: reason} }

// maxOutputCause bounds the last output line in a cause, in bytes.
const maxOutputCause = 200

var (
	outputURLUserinfo = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^/\s@]+@`)
	outputSecretPair  = regexp.MustCompile(`(?i)\b([a-z0-9_.-]*(?:token|secret|password|passwd|apikey|api_key|authorization|credential)[a-z0-9_.-]*)\s*[=:]\s*\S+`)
)

// printableLine makes one line of add-on output fit a cause: printable
// characters only, one line, no credential in a URL or a key=value pair, at
// most maxOutputCause bytes.
func printableLine(s string) string {
	s = strings.ToValidUTF8(s, "?")
	s = strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return ' '
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	s = outputURLUserinfo.ReplaceAllString(s, "${1}[redacted]@")
	s = outputSecretPair.ReplaceAllString(s, "${1}=[redacted]")
	if len(s) > maxOutputCause {
		cut := maxOutputCause
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut] + "..."
	}
	return s
}

// lastOutput remembers the last non-empty line that an add-on wrote to its
// standard output or error. It is filled by the readers of the output pipes
// that the launch already runs, so it adds no goroutine.
type lastOutput struct {
	mu   sync.Mutex
	line string
	// out and err close when the readers of the two streams end.
	out, err chan struct{}
}

func newLastOutput() *lastOutput {
	return &lastOutput{out: make(chan struct{}), err: make(chan struct{})}
}

func (o *lastOutput) record(line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	o.mu.Lock()
	o.line = line
	o.mu.Unlock()
}

// text is the end of a cause: the last line, or "no output". The streams of a
// process that ended close at once; a child of the add-on may hold them open,
// so the wait is short and bounded.
func (o *lastOutput) text(ended bool) string {
	if ended {
		grace := time.NewTimer(200 * time.Millisecond)
		defer grace.Stop()
		for _, c := range []chan struct{}{o.out, o.err} {
			select {
			case <-c:
			case <-grace.C:
			}
		}
	}
	o.mu.Lock()
	line := o.line
	o.mu.Unlock()
	if line = printableLine(line); line == "" {
		return "no output"
	}
	return "last output: " + strconv.Quote(line)
}

// exitText says how the process ended.
func exitText(ps *os.ProcessState, after time.Duration) string {
	secs := fmt.Sprintf("%.2fs", after.Seconds())
	if ps == nil {
		return "ended after " + secs
	}
	if code := ps.ExitCode(); code >= 0 {
		return fmt.Sprintf("exited %d after %s", code, secs)
	}
	return "ended (" + printableLine(ps.String()) + ") after " + secs
}

// reasonOf is the short text of an os error without the path in it.
func reasonOf(e error) string {
	var pe *os.PathError
	if errors.As(e, &pe) {
		e = pe.Err
	}
	var le *os.LinkError
	if errors.As(e, &le) {
		e = le.Err
	}
	return printableLine(e.Error())
}
