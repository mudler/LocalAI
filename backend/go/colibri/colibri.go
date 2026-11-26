package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mudler/xlog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	"github.com/mudler/LocalAI/pkg/grpc/grpcerrors"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

const backendName = "colibri"

// importTimeout bounds `coli import`. In link mode (the default) it writes
// a few small files and reads the GGUF header, so minutes are plenty.
const importTimeout = 30 * time.Minute

// Colibri is the LocalAI backend that runs colibri's `coli serve` gateway
// as a child process on a loopback port and talks to its OpenAI-compatible
// API. colibri renders the chat template and parses tool calls itself; the
// backend passes the structured chat in and the parsed result back out as
// ChatDeltas.
//
// It embeds base.Base, not base.SingleThread: coli serve has its own
// admission queue and KV slots, so requests are passed on concurrently.
type Colibri struct {
	base.Base

	// lifeMu serializes Load, Free's process stop and crash restarts. They
	// can take minutes, so they do not hold mu, which the request path and
	// Status take briefly.
	lifeMu sync.Mutex

	mu       sync.RWMutex
	sc       *sidecar
	cfg      loadConfig
	stateDir string
	broken   error // set when a crashed coli serve could not be started again
	gen      uint64

	active atomic.Int32

	// Seams for tests.
	start     func(context.Context, loadConfig, string) (*sidecar, error)
	runImport func(context.Context, loadConfig) error
}

func NewColibri() *Colibri {
	return &Colibri{start: startSidecar, runImport: importGGUF}
}

func (b *Colibri) Load(opts *pb.ModelOptions) error {
	c, err := parseLoad(opts)
	if err != nil {
		return err
	}

	b.lifeMu.Lock()
	defer b.lifeMu.Unlock()

	// The previous model goes first, so its memory is free for the next one.
	b.mu.Lock()
	old, oldDir := b.sc, b.stateDir
	b.sc, b.stateDir, b.broken = nil, "", nil
	b.gen++
	b.mu.Unlock()
	if old != nil {
		old.stop(b.cfg.stopTimeout)
	}
	if oldDir != "" {
		_ = os.RemoveAll(oldDir)
	}

	if c.gguf != "" {
		if err := b.runImport(context.Background(), c); err != nil {
			return err
		}
	}

	stateDir, err := os.MkdirTemp("", "colibri-")
	if err != nil {
		return fmt.Errorf("colibri: state directory: %w", err)
	}
	sc, err := b.start(context.Background(), c, stateDir)
	if err != nil {
		_ = os.RemoveAll(stateDir)
		return err
	}

	b.mu.Lock()
	b.sc, b.cfg, b.stateDir = sc, c, stateDir
	b.mu.Unlock()
	xlog.Info("colibri: model ready", "model", c.modelDir)
	return nil
}

// importGGUF runs `coli import` into a directory next to the final one and
// renames it into place, so an interrupted import is never taken for a
// finished one.
func importGGUF(ctx context.Context, c loadConfig) error {
	ctx, cancel := context.WithTimeout(ctx, importTimeout)
	defer cancel()
	partial := c.modelDir + ".partial"
	_ = os.RemoveAll(partial)
	xlog.Info("colibri: importing GGUF (once)", "gguf", c.gguf, "into", c.modelDir)
	prog, argv := c.command("import", c.gguf, "--out", partial)
	// #nosec G204 -- prog is the packaged coli (or the coli_path/python model options an admin sets); argv is passed without a shell
	cmd := exec.CommandContext(ctx, prog, argv...)
	cmd.Env = append(append(baseEnv(), "PYTHONDONTWRITEBYTECODE=1"), c.env...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		_ = os.RemoveAll(partial)
		return fmt.Errorf("colibri: coli import %s failed: %w\n%s", filepath.Base(c.gguf), err, lastLines(out.String(), tailLines))
	}
	if !fileExists(filepath.Join(partial, "config.json")) {
		_ = os.RemoveAll(partial)
		return fmt.Errorf("colibri: coli import %s wrote no config.json\n%s", filepath.Base(c.gguf), lastLines(out.String(), tailLines))
	}
	_ = os.RemoveAll(c.modelDir)
	if err := os.Rename(partial, c.modelDir); err != nil {
		return fmt.Errorf("colibri: import: %w", err)
	}
	return nil
}

func lastLines(s string, n int) string {
	lines := bytes.Split(bytes.TrimRight([]byte(s), "\n"), []byte("\n"))
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return string(bytes.Join(lines, []byte("\n")))
}

func (b *Colibri) Free() error {
	b.lifeMu.Lock()
	defer b.lifeMu.Unlock()
	b.mu.Lock()
	sc, dir, timeout := b.sc, b.stateDir, b.cfg.stopTimeout
	b.sc, b.stateDir, b.broken = nil, "", nil
	b.gen++
	b.mu.Unlock()
	if sc != nil {
		sc.stop(timeout)
	}
	if dir != "" {
		_ = os.RemoveAll(dir)
	}
	return nil
}

// ready returns a running sidecar. A coli serve that has died is started
// once more from the saved configuration; when that fails too, the backend
// stays broken (Status reports ERROR) until the next Load.
func (b *Colibri) ready(ctx context.Context) (*sidecar, loadConfig, error) {
	b.mu.RLock()
	sc, cfg, broken := b.sc, b.cfg, b.broken
	b.mu.RUnlock()
	if broken != nil {
		return nil, cfg, broken
	}
	if sc == nil {
		return nil, cfg, grpcerrors.ModelNotLoaded(backendName)
	}
	if sc.alive() {
		return sc, cfg, nil
	}
	sc, err := b.restart(ctx, sc)
	return sc, cfg, err
}

// restart is single-flight: restarts share lifeMu with Load and Free, so a
// second request that finds the same dead process waits for the first and
// then uses its result.
func (b *Colibri) restart(ctx context.Context, dead *sidecar) (*sidecar, error) {
	b.lifeMu.Lock()
	defer b.lifeMu.Unlock()

	b.mu.RLock()
	cur, cfg, dir, broken, gen := b.sc, b.cfg, b.stateDir, b.broken, b.gen
	b.mu.RUnlock()
	switch {
	case broken != nil:
		return nil, broken
	case cur == nil:
		return nil, grpcerrors.ModelNotLoaded(backendName)
	case cur != dead && cur.alive():
		return cur, nil
	}
	xlog.Warn("colibri: coli serve is not running, starting it again", "reason", dead.exitReason())
	sc, err := b.start(ctx, cfg, dir)

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.gen != gen { // Free or Load ran meanwhile
		if sc != nil {
			go sc.stop(cfg.stopTimeout)
		}
		return nil, grpcerrors.ModelNotLoaded(backendName)
	}
	if err != nil {
		b.broken = fmt.Errorf("colibri: coli serve exited and could not be started again: %w\n(first exit: %s)", err, dead.exitReason())
		b.sc = nil
		go dead.stop(cfg.stopTimeout)
		return nil, b.broken
	}
	b.sc = sc
	xlog.Info("colibri: coli serve restarted")
	return sc, nil
}

func (b *Colibri) Status() (pb.StatusResponse, error) {
	b.mu.RLock()
	sc, broken := b.sc, b.broken
	b.mu.RUnlock()
	switch {
	case broken != nil:
		return pb.StatusResponse{State: pb.StatusResponse_ERROR}, nil
	case sc == nil:
		return pb.StatusResponse{State: pb.StatusResponse_UNINITIALIZED}, nil
	case !sc.alive():
		return pb.StatusResponse{State: pb.StatusResponse_ERROR}, nil
	}
	mem := &pb.MemoryUsageData{Breakdown: map[string]uint64{}}
	if r := groupRSS(sc.pid); r > 0 {
		mem.Breakdown["coli-serve"] = r
		mem.Total = r
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	st, err := sc.probe(ctx)
	state := pb.StatusResponse_READY
	switch {
	case err != nil, st != "ok", b.active.Load() > 0:
		// Alive but restarting its engine, too busy to answer, or serving.
		state = pb.StatusResponse_BUSY
	}
	return pb.StatusResponse{State: state, Memory: mem}, nil
}

func (b *Colibri) Busy() bool { return b.active.Load() > 0 }

func (b *Colibri) Embeddings(*pb.PredictOptions) ([]float32, error) {
	return nil, status.Error(codes.Unimplemented, "colibri: embeddings are not supported")
}

func (b *Colibri) TokenizeString(*pb.PredictOptions) (pb.TokenizationResponse, error) {
	return pb.TokenizationResponse{}, status.Error(codes.Unimplemented, "colibri: tokenization is not supported (coli serve has no tokenize endpoint)")
}

// run sends one request and hands each reply to emit (once for a
// non-streamed request).
func (b *Colibri) run(ctx context.Context, opts *pb.PredictOptions, stream bool, emit func(*pb.Reply) error) error {
	if err := checkRequest(opts); err != nil {
		return err
	}
	sc, cfg, err := b.ready(ctx)
	if err != nil {
		return err
	}
	chat := isChat(opts)
	body, err := buildRequest(opts, cfg.modelID, stream)
	if err != nil {
		return err
	}
	path := "/v1/completions"
	if chat {
		path = "/v1/chat/completions"
	}

	b.active.Add(1)
	defer b.active.Add(-1)

	// Cancelling ctx closes the connection; coli serve notices the
	// disconnect and stops generating.
	resp, err := sc.post(ctx, path, body)
	if err != nil {
		return b.requestError(ctx, sc, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return upstreamError(resp.StatusCode, resp.Body)
	}
	if stream {
		if err := readStream(resp.Body, chat, emit); err != nil {
			return b.requestError(ctx, sc, err)
		}
		return nil
	}
	var r response
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(&r); err != nil {
		return b.requestError(ctx, sc, err)
	}
	reply, err := replyFromResponse(&r, chat)
	if err != nil {
		return err
	}
	return emit(reply)
}

// requestError says why a request failed: the caller went away, or coli
// serve died under it (with its last output), or a transport error.
func (b *Colibri) requestError(ctx context.Context, sc *sidecar, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	select {
	case <-sc.done:
		return errors.New("colibri: the request failed because " + sc.exitReason())
	case <-time.After(time.Second):
	}
	return fmt.Errorf("colibri: request to coli serve failed: %w", err)
}

func (b *Colibri) PredictRichContext(ctx context.Context, opts *pb.PredictOptions) (*pb.Reply, error) {
	var out *pb.Reply
	err := b.run(ctx, opts, false, func(r *pb.Reply) error {
		out = r
		return nil
	})
	return out, err
}

// PredictStreamRichContext sends replies into the channel and does not
// close it (the gRPC server does).
func (b *Colibri) PredictStreamRichContext(ctx context.Context, opts *pb.PredictOptions, out chan<- *pb.Reply) error {
	return b.run(ctx, opts, true, func(r *pb.Reply) error {
		select {
		case out <- r:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
}

// PredictRich and PredictStreamRich make the backend an AIModelRich too;
// the gRPC server prefers the context variants above.
func (b *Colibri) PredictRich(opts *pb.PredictOptions) (*pb.Reply, error) {
	return b.PredictRichContext(context.Background(), opts)
}

func (b *Colibri) PredictStreamRich(opts *pb.PredictOptions, out chan<- *pb.Reply) error {
	return b.PredictStreamRichContext(context.Background(), opts, out)
}

func (b *Colibri) Predict(opts *pb.PredictOptions) (string, error) {
	r, err := b.PredictRichContext(context.Background(), opts)
	if err != nil {
		return "", err
	}
	return string(r.GetMessage()), nil
}

func (b *Colibri) PredictStream(opts *pb.PredictOptions, results chan string) error {
	defer close(results)
	return b.run(context.Background(), opts, true, func(r *pb.Reply) error {
		if len(r.GetMessage()) > 0 {
			results <- string(r.GetMessage())
		}
		return nil
	})
}
