package downloader

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/mudler/LocalAI/pkg/llmman"
	"github.com/mudler/xlog"
)

// fetchModelPackViaLlmman acquires a CNCF ModelPack artifact through a running
// `llmman serve` daemon and materialises it at dst.
//
// reference must be digest-pinned to the manifest the caller inspected and
// verified, so llmman cannot re-resolve a tag to different bytes.
//
// The daemon does the pull (POST /api/pull, streamed so progress is not a
// multi-gigabyte silence) but deliberately exposes no local path, so
// `llmman resolve --no-pull` is asked where the bytes landed. Both pieces are
// therefore required, and each missing one has its own error.
func fetchModelPackViaLlmman(ctx context.Context, reference, dst, requestedPath string, downloadStatus func(string, string, string, float64)) error {
	endpoint := llmman.Endpoint()

	if err := llmman.CheckDaemon(ctx, llmman.ProbeClient(), endpoint); err != nil {
		return err
	}

	xlog.Info("Pulling CNCF ModelPack artifact via llmman", "ref", reference, "endpoint", endpoint)

	if err := llmman.Pull(ctx, llmman.DefaultClient(), endpoint, reference, modelPackProgress(reference, downloadStatus)); err != nil {
		return err
	}

	src, err := llmman.Resolve(ctx, reference)
	if err != nil {
		return err
	}

	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	// OCI extraction normalizes filenames to directories, but a GGUF result
	// must retain the filename requested by the caller.
	if !info.IsDir() {
		dst = requestedPath
	}
	return linkOrCopyTree(src, dst)
}

// modelPackProgress adapts llmman pull progress to the downloadStatus contract
// of (fileName, written, total, percentage).
func modelPackProgress(reference string, downloadStatus func(string, string, string, float64)) llmman.ProgressFunc {
	return func(_ string, completed, total int64) {
		if downloadStatus == nil {
			return
		}
		var totalText string
		var pct float64
		if total > 0 {
			totalText = formatBytes(total)
			pct = float64(completed) / float64(total) * 100
		}
		downloadStatus(reference, formatBytes(completed), totalText, pct)
	}
}

// linkOrCopyTree materialises src at dst, hard-linking files where possible so
// a model shared with llmman's store costs its bytes once rather than twice,
// and copying when a link cannot be made (different filesystem).
func linkOrCopyTree(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("resolved path %q: %w", src, err)
	}

	if !info.IsDir() {
		// An existing directory is a container for the resolved file.
		if target, err := os.Stat(dst); err == nil && target.IsDir() {
			dst = filepath.Join(dst, filepath.Base(src))
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return linkOrCopyFile(src, dst)
	}

	return filepath.Walk(src, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if fi.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !fi.Mode().IsRegular() {
			// Symlinks and devices carry no model payload.
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return linkOrCopyFile(path, target)
	})
}

func linkOrCopyFile(src, dst string) error {
	if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Link(src, dst); err == nil {
		return nil
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
