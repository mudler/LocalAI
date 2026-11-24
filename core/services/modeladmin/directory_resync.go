package modeladmin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/messaging"
	"github.com/mudler/xlog"
)

// DirectoryResync brings one frontend's in-memory model configs back in line
// with the shared models directory when no invalidation event told it to.
//
// The models invalidation on the message bus is the fast path, but it is fire
// and forget: NATS keeps no history, so a frontend that is disconnected, or
// whose handler failed, never hears about the change again. DirectoryResync is
// the catch-up path. It reruns the same reconcile as ApplyRemoteChange, with no
// named element, so only models whose file actually changed get a revision
// transition, and a pass over an unchanged directory changes nothing.
//
// It keeps no state that other frontends need: every frontend reads the same
// shared directory, and the only local memory is a fingerprint of the config
// files it last reconciled, used to skip a pass when nothing changed.
type DirectoryResync struct {
	loader     *config.ModelConfigLoader
	modelsPath string
	lifecycle  ModelRevisionLifecycle
	opts       []config.ConfigLoaderOption

	mu          sync.Mutex
	reconciled  string // fingerprint of the last successful pass
	lastFailure string // fingerprint of the last failed pass, to log it once
}

// NewDirectoryResync returns a resync for loader against modelsPath. lifecycle
// may be nil; opts are the loader options used everywhere else.
func NewDirectoryResync(loader *config.ModelConfigLoader, modelsPath string, lifecycle ModelRevisionLifecycle, opts ...config.ConfigLoaderOption) *DirectoryResync {
	return &DirectoryResync{loader: loader, modelsPath: modelsPath, lifecycle: lifecycle, opts: opts}
}

// Resync reconciles the loader with the models directory. Unless force is set,
// it does nothing when the config files are byte-for-byte the ones the last
// successful pass reconciled.
func (r *DirectoryResync) Resync(ctx context.Context, force bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	fingerprint, err := configFilesFingerprint(r.modelsPath)
	if err != nil {
		return err
	}
	if !force && fingerprint == r.reconciled {
		return nil
	}
	if err := ApplyRemoteChange(ctx, r.loader, r.modelsPath, messaging.CacheInvalidateEvent{}, r.lifecycle, r.opts...); err != nil {
		r.reconciled = ""
		if fingerprint != r.lastFailure {
			r.lastFailure = fingerprint
			return err
		}
		// Same directory content, same failure: already reported.
		xlog.Debug("Model config resync still failing", "error", err)
		return nil
	}
	r.reconciled = fingerprint
	r.lastFailure = ""
	return nil
}

// Start runs a pass every interval until ctx is done, and a forced pass after
// each reconnect of bus when bus reports reconnects (the NATS client does;
// a nil bus or one without OnReconnect gets only the periodic pass). The first
// periodic pass runs after one interval, because startup has just loaded the
// directory.
func (r *DirectoryResync) Start(ctx context.Context, interval time.Duration, bus any) {
	if reconnecting, ok := bus.(interface{ OnReconnect(func()) }); ok {
		reconnecting.OnReconnect(func() {
			// Off the NATS callback goroutine: a pass reads the disk and the
			// database.
			go func() {
				if err := r.Resync(ctx, true); err != nil {
					xlog.Warn("Failed to resync model configs after a NATS reconnect", "error", err)
				}
			}()
		})
	}
	go r.run(ctx, interval)
}

func (r *DirectoryResync) run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.Resync(ctx, false); err != nil {
				xlog.Warn("Failed to resync model configs from the models directory", "error", err)
			}
		}
	}
}

// configFilesFingerprint hashes the names and contents of the config files the
// loader reads from dir: top-level .yaml and .yml files that are not dotfiles.
// Reading a few small text files is cheap; parsing them is not, because a parse
// also inspects the model files they point at.
func configFilesFingerprint(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("read models directory: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if entry.IsDir() || strings.HasPrefix(name, ".") || (ext != ".yaml" && ext != ".yml") {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			if os.IsNotExist(err) {
				continue // removed between the listing and the read
			}
			return "", fmt.Errorf("read model config %q: %w", name, err)
		}
		sum := sha256.Sum256(data)
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write(sum[:])
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
