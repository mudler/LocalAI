package worker

import (
	"context"
	"encoding/json"

	"github.com/mudler/LocalAI/core/services/workerctl"
)

// controlVerb names one control verb independent of the carrier that delivers
// it. The NATS server maps it onto a per-node subject; the HTTP server maps it
// onto a path. The names are those of package workerctl.
type controlVerb string

const (
	verbBackendInstall controlVerb = workerctl.VerbBackendInstall
	verbBackendUpgrade controlVerb = workerctl.VerbBackendUpgrade
	verbBackendStop    controlVerb = workerctl.VerbBackendStop
	verbBackendDelete  controlVerb = workerctl.VerbBackendDelete
	verbBackendList    controlVerb = workerctl.VerbBackendList
	verbModelsRunning  controlVerb = workerctl.VerbModelsRunning
	verbModelUnload    controlVerb = workerctl.VerbModelUnload
	verbModelStop      controlVerb = workerctl.VerbModelStop
	verbModelDelete    controlVerb = workerctl.VerbModelDelete
	verbModelOp        controlVerb = workerctl.VerbModelOp
	verbNodeStop       controlVerb = workerctl.VerbNodeStop
	verbFilesEnsure    controlVerb = workerctl.VerbFilesEnsure
	verbFilesStage     controlVerb = workerctl.VerbFilesStage
	verbFilesTemp      controlVerb = workerctl.VerbFilesTemp
	verbFilesListDir   controlVerb = workerctl.VerbFilesListDir
	verbFilesRelease   controlVerb = workerctl.VerbFilesRelease
)

// progressSink receives install progress while a long-running verb runs.
type progressSink func(workerctl.BackendInstallProgressEvent)

// controlHandler answers one request. A nil reply means the verb sends no
// answer (node.stop). undecodable is non-nil only when body could not be read
// as the verb's request; reply then holds the verb's typed refusal. The NATS
// server sends reply either way, which is today's behaviour. A carrier that can
// signal a malformed request out of band (HTTP 400) may send that instead.
// Only tests read undecodable today; it is kept as the hook for such a carrier.
type controlHandler func(ctx context.Context, body []byte) (reply any, undecodable error)

// progressControlHandler is controlHandler for a verb that may run for minutes
// and reports progress while it runs. progress is never nil.
type progressControlHandler func(ctx context.Context, body []byte, progress progressSink) (reply any, undecodable error)

// controlServer is the carrier the worker serves its control verbs on. A
// registration returns once the carrier will deliver requests for the verb, or
// an error that names the verb; a carrier-side refusal (a NATS permission
// violation) is an error here, never a silent no-op. handle may deliver
// requests concurrently (the NATS server happens to serialise per verb; the
// HTTP server does not, so a handler must be safe to call from several
// goroutines).
// handleWithProgress delivers each request on its own goroutine, because a verb
// that runs for minutes must not hold up the next request of the same verb.
type controlServer interface {
	handle(verb controlVerb, h controlHandler) error
	handleWithProgress(verb controlVerb, h progressControlHandler) error
}

// unary types a controlHandler. Go interfaces cannot carry generic methods, so
// the typing lives in these adapters and the interface stays byte-level.
func unary[Req, Reply any](decode func([]byte) (Req, error), refuse func(error) Reply, h func(context.Context, Req) Reply) controlHandler {
	return func(ctx context.Context, body []byte) (any, error) {
		req, err := decode(body)
		if err != nil {
			return refuse(err), err
		}
		return h(ctx, req), nil
	}
}

// withProgress is unary for progressControlHandler.
func withProgress[Req, Reply any](decode func([]byte) (Req, error), refuse func(error) Reply, h func(context.Context, Req, progressSink) Reply) progressControlHandler {
	return func(ctx context.Context, body []byte, p progressSink) (any, error) {
		req, err := decode(body)
		if err != nil {
			return refuse(err), err
		}
		return h(ctx, req, p), nil
	}
}

// noReply builds the handler of a verb that has no request and no reply.
func noReply(h func(context.Context)) controlHandler {
	return func(ctx context.Context, _ []byte) (any, error) {
		h(ctx)
		return nil, nil
	}
}

func decodeJSON[Req any](body []byte) (Req, error) {
	var req Req
	err := json.Unmarshal(body, &req)
	return req, err
}

// ignoreBody is the decode of a verb that never read its body (backend.list,
// models.running, files.temp). It never fails, so a malformed body is still
// answered, as today.
func ignoreBody[Req any]([]byte) (Req, error) {
	var req Req
	return req, nil
}

// refuseNever is the refusal of a verb whose decode cannot fail (ignoreBody),
// so it is never called.
func refuseNever[Reply any](error) Reply {
	var reply Reply
	return reply
}
