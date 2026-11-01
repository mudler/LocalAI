package session

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
)

// Codes of a failed turn. The chat page picks its plain-words text by code, so
// the codes are part of the event payload: add new ones, do not rename.
const (
	CodeBackendMissing = "model_backend_missing" // the model's backend is not installed
	CodeCooldown       = "model_cooldown"        // a recent load failed and the model rests
	CodeLoadFailed     = "model_load_failed"     // the model's backend did not start
	CodeModelNotFound  = "model_not_found"       // no such model
	CodeUpstream       = "upstream"              // the provider or proxy behind the model failed
	CodeUpstreamKey    = "upstream_key"          // the upstream refused the key
	CodeUpstreamPath   = "upstream_path"         // the upstream has no such endpoint at that address
	CodeUpstreamDown   = "upstream_unreachable"  // the upstream address does not resolve or answer
	CodeTimeout        = "timeout"
	CodeCancelled      = "cancelled"
	CodeNotAllowed     = "not_allowed"
	CodeQuota          = "quota"
	CodeContext        = "context_too_small"
	CodeUnknownTool    = "unknown_tool"
	CodeAgent          = "agent" // anything else
)

// Marks the model client puts in the error of a model that could not be loaded.
const (
	// MarkBackendMissing says that the backend of the model is not installed.
	MarkBackendMissing = "[studio:backend-missing]"
	// MarkLoadFailed says that the model's load failed for another reason.
	MarkLoadFailed = "[studio:load-failed]"
	// MarkUpstreamKey and MarkUpstreamPath say that a cloud-proxy model's
	// upstream answered 401 or 403, or 404 or 405.
	MarkUpstreamKey  = "[studio:upstream-key]"
	MarkUpstreamPath = "[studio:upstream-path]"
)

// Failure is why a turn ended, in a form that is safe to show to the user.
type Failure struct {
	Code string
	// Reason is one plain sentence or two, in English, with a hint. The chat
	// page translates it by Code and falls back to this text.
	Reason string
	// Model names the model the cause is about, when the error says.
	Model string
	// RetryAfter is the seconds until a model in cooldown loads again.
	RetryAfter int
	// Detail is the error text for the technical view: one line, with
	// addresses, paths and secrets removed, and cut to a limit.
	Detail string
}

var (
	quotedModelRe    = regexp.MustCompile(`model "([^"]{1,128})"`)
	retryAfterRe     = regexp.MustCompile(`retry after (?:(\d+)m)?(?:(\d+)s)?`)
	notFoundModelRe  = regexp.MustCompile(`(?i)model (?:"([^"]{1,128})"|'([^']{1,128})'|([A-Za-z0-9._:/@+-]{1,128})) (?:was )?not found`)
	safeModelRe      = regexp.MustCompile(`^[A-Za-z0-9._:/@+ -]{1,128}$`)
	httpStatusFiveRe = regexp.MustCompile(`HTTP (5\d\d)`)
)

// Classify names the cause of an error that ended a turn. It never quotes the
// error in Reason: an internal address or a stack would not belong in a chat.
// Detail carries the error, cleaned.
func Classify(err error) Failure {
	if err == nil {
		return Failure{Code: CodeAgent, Reason: reasonAgent}
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	f := Failure{Code: CodeAgent, Reason: reasonAgent, Detail: Sanitize(msg)}
	switch {
	case strings.Contains(msg, MarkNotAllowed):
		f.Code, f.Reason = CodeNotAllowed, ReasonNotAllowed
	case strings.Contains(msg, MarkQuota):
		f.Code, f.Reason = CodeQuota, ReasonQuota
	case isLoadFailure(lower):
		f = loadFailure(f, msg, lower)
	case strings.Contains(msg, MarkUpstreamKey):
		f.Code = CodeUpstreamKey
		f.Reason = "The upstream refused the key. Check the API key of this model in its settings."
	case strings.Contains(msg, MarkUpstreamPath):
		f.Code = CodeUpstreamPath
		f.Reason = "The upstream does not accept this request at that address. The upstream address must be the full endpoint, for example .../v1/chat/completions."
	case strings.Contains(lower, "no such host") || strings.Contains(lower, "upstream request failed"):
		f.Code = CodeUpstreamDown
		f.Reason = "The upstream address could not be reached. Check the address in the model's settings."
	case notFoundModelRe.MatchString(msg):
		f.Code = CodeModelNotFound
		f.Model = firstModel(notFoundModelRe.FindStringSubmatch(msg))
		f.Reason = namedModel("The model", f.Model, " was not found. Pick another model, or install it from the Models page.")
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(lower, "deadline exceeded") ||
		strings.Contains(lower, "timed out") || strings.Contains(lower, "timeout"):
		f.Code = CodeTimeout
		f.Reason = "The model took too long to answer, so the turn was stopped. Try again, or use a smaller or faster model."
	case errors.Is(err, context.Canceled) || strings.Contains(lower, "context canceled"):
		f.Code = CodeCancelled
		f.Reason = "The turn was cancelled before the model answered."
	case strings.Contains(lower, "context") && (strings.Contains(lower, "exceed") || strings.Contains(lower, "too long") ||
		strings.Contains(lower, "too small") || strings.Contains(lower, "too large") || strings.Contains(lower, "overflow")):
		f.Code, f.Reason = CodeContext, contextReason(msg)
	case strings.Contains(msg, "not found in available tools"):
		f.Code, f.Reason = CodeUnknownTool, unknownToolReason(msg)
	case strings.Contains(msg, "the model failed ") || strings.Contains(lower, "connection refused") ||
		strings.Contains(lower, "bad gateway") || httpStatusFiveRe.MatchString(msg):
		f.Code = CodeUpstream
		f.Reason = reasonModelFailedPrefix + " several times in a row, so the turn was stopped."
		if m := httpStatusRe.FindStringSubmatch(msg); m != nil {
			f.Reason = reasonModelFailedPrefix + " several times in a row (HTTP " + m[1] + "), so the turn was stopped. The provider or proxy behind the model returned an error: check its address and key in the model's settings, or try again."
		}
	}
	return f
}

func isLoadFailure(lower string) bool {
	return strings.Contains(lower, strings.ToLower(MarkBackendMissing)) || strings.Contains(lower, strings.ToLower(MarkLoadFailed)) ||
		strings.Contains(lower, "the model could not be loaded") || strings.Contains(lower, "load is in cooldown") ||
		strings.Contains(lower, "backend not found") || strings.Contains(lower, "failed to load model")
}

func loadFailure(f Failure, msg, lower string) Failure {
	f.Model = firstModel(quotedModelRe.FindStringSubmatch(msg))
	missing := strings.Contains(msg, MarkBackendMissing) || strings.Contains(lower, "backend not found")
	if strings.Contains(lower, "load is in cooldown") {
		f.RetryAfter = retrySeconds(msg)
	}
	switch {
	case missing:
		f.Code = CodeBackendMissing
		f.Reason = namedModel("The model", f.Model, " could not be started: its backend is not installed. Install the backend, or pick another model.")
	case f.RetryAfter > 0:
		f.Code = CodeCooldown
		f.Reason = namedModel("The model", f.Model, " could not be started a moment ago and is resting. Try again in "+strconv.Itoa(f.RetryAfter)+" seconds.")
	default:
		f.Code = CodeLoadFailed
		f.Reason = namedModel("The model", f.Model, " could not be started. Check the model's configuration and its backend.")
	}
	return f
}

// namedModel builds "The model "x" ..." or "The model ...".
func namedModel(prefix, model, rest string) string {
	if model == "" {
		return prefix + rest
	}
	return prefix + " \"" + model + "\"" + rest
}

func firstModel(m []string) string {
	for _, s := range m[min(1, len(m)):] {
		if s != "" && safeModelRe.MatchString(s) {
			return s
		}
	}
	return ""
}

func retrySeconds(msg string) int {
	m := retryAfterRe.FindStringSubmatch(msg)
	if m == nil {
		return 0
	}
	mins, _ := strconv.Atoi(m[1])
	secs, _ := strconv.Atoi(m[2])
	return mins*60 + secs
}

// MaxDetail is the longest Detail, in bytes.
const MaxDetail = 600

var (
	urlRe    = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s"')\]]+`)
	markRe   = regexp.MustCompile(`\s?\[studio:[a-z-]+\]`)
	bearerRe = regexp.MustCompile(`(?i)\b(?:bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}`)
	secretRe = regexp.MustCompile(`(?i)\b(api[_-]?key|token|secret|password|authorization)(["']?\s*[:=]\s*["']?)[^\s"',;]+|\bsk-[A-Za-z0-9_-]{8,}`)
	pathRe   = regexp.MustCompile(`(?:^|[\s"'(=])(/(?:home|root|tmp|var|opt|usr|etc|mnt|srv|run|data|models|backends)/[^\s"')]*)`)
	hostPort = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}(?::\d+)?\b`)
	longTok  = regexp.MustCompile(`\b[A-Za-z0-9_-]{40,}\b`)
)

// Sanitize makes the text of an error fit for the technical view of a chat: the
// first line only (a stack is gone), no URLs, addresses, local paths or
// secrets, and no more than MaxDetail bytes.
func Sanitize(msg string) string {
	msg = strings.TrimSpace(msg)
	if i := strings.IndexAny(msg, "\r\n"); i >= 0 {
		msg = msg[:i]
	}
	msg = markRe.ReplaceAllString(msg, "")
	msg = bearerRe.ReplaceAllString(msg, "[hidden]")
	msg = secretRe.ReplaceAllStringFunc(msg, func(s string) string {
		if m := secretRe.FindStringSubmatch(s); m != nil && m[1] != "" {
			return m[1] + m[2] + "[hidden]"
		}
		return "[hidden]"
	})
	msg = urlRe.ReplaceAllString(msg, "[address]")
	msg = hostPort.ReplaceAllString(msg, "[address]")
	msg = pathRe.ReplaceAllStringFunc(msg, func(s string) string {
		if before, _, ok := strings.Cut(s, "/"); ok {
			return before + "[path]"
		}
		return s
	})
	msg = longTok.ReplaceAllString(msg, "[hidden]")
	return CapText(msg, MaxDetail)
}

// reasonFor is the reason of Classify.
func reasonFor(err error) string { return Classify(err).Reason }
