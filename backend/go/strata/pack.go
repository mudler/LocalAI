package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mudler/xlog"
)

// Pack tools. A Strata model is not only its GGUF files: the engine reads a
// pack directory made from them. Strata ships the tools that make it, and the
// backend image carries them, so a model that is only a set of GGUF files can
// be installed by downloading them. On the first load the backend runs the
// tool. A pack that is already complete is never touched.
const (
	// packToolIQ is tools/iq_pack.py: the index, the dense weights and the
	// tokenizer of a Flash-Next style model. The engine reads the experts from
	// the GGUF files themselves.
	packToolIQ = "iq_pack"
	// packToolTokenizer is tools/strata_tokenizer.py: only the tokenizer and
	// chat template, for models whose engine path reads the GGUF as it is
	// (Qwen3.6-35B-A3B).
	packToolTokenizer = "tokenizer"
	// packToolDSV41 is tools/dsv41/pack_gguf.py: the pack of a DeepSeek V4.1
	// GGUF set (tensor map, n-gram table index, tokenizer, chat template).
	// The files it names stay where they are. It takes the model directory,
	// not one shard, and the files to read through pack_args (--shards,
	// --engram, --engram-meta, --dspark-gguf).
	packToolDSV41 = "dsv41"

	defaultPackTimeout = 2 * time.Hour
	packTailLines      = 20
)

// packBuild is a request to make the pack of a model from its GGUF files.
type packBuild struct {
	tool    string
	script  string // absolute path of the tool, set by the caller from strata_root
	python  string
	gguf    string // first shard
	dest    string // the pack directory to create
	args    []string
	timeout time.Duration
}

// packMarkers are the files that say a pack made by the tool is finished. The
// iq tool writes native_experts.txt last; the tokenizer export has no later
// file, and is built in a staging directory that is renamed when it is done.
func packMarkers(tool string) []string {
	switch tool {
	case packToolTokenizer:
		return []string{filepath.Join("tokenizer", "vocab.json"), filepath.Join("tokenizer", "chat_template.jinja")}
	case packToolDSV41:
		// pack_gguf.py writes the tokenizer last.
		return []string{"ds4.txt", "tensors.txt", "engram.bin", filepath.Join("tokenizer", "vocab.json"), filepath.Join("tokenizer", "chat_template.jinja")}
	}
	return []string{"native_experts.txt", "index.txt", filepath.Join("tokenizer", "vocab.json")}
}

// packComplete reports whether dir holds a finished pack of the given kind.
func packComplete(dir, tool string) bool {
	for _, m := range packMarkers(tool) {
		if !fileExists(filepath.Join(dir, m)) {
			return false
		}
	}
	return true
}

// packScript is the tool file under the Strata source tree.
func packScript(tool string) string {
	switch tool {
	case packToolTokenizer:
		return filepath.Join("tools", "strata_tokenizer.py")
	case packToolDSV41:
		return filepath.Join("tools", "dsv41", "pack_gguf.py")
	}
	return filepath.Join("tools", "iq_pack.py")
}

// runPackBuild makes the pack in a staging directory next to dest and renames
// it when the tool succeeds, so a stop part-way leaves no half pack. It does
// not delete anything it did not make.
func runPackBuild(b packBuild) error {
	if st, err := os.Stat(b.dest); err == nil {
		if !st.IsDir() || packComplete(b.dest, b.tool) {
			return nil
		}
		return fmt.Errorf("%s exists but is not a finished pack; remove it so that the backend can build it again", b.dest)
	}
	if !fileExists(b.script) {
		return fmt.Errorf("the pack tool %s is missing: this backend image carries an older Strata source tree, or strata_root points at a tree without it", b.script)
	}
	stage := b.dest + ".partial"
	if err := os.RemoveAll(stage); err != nil { // a stop in an earlier build; only this backend makes this name
		return err
	}
	if err := os.MkdirAll(stage, 0o750); err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(stage)
		}
	}()

	timeout := b.timeout
	if timeout <= 0 {
		timeout = defaultPackTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	args := append([]string{b.script, "--gguf", b.gguf, "--out", stage}, b.args...)
	dir := ""
	if b.tool == packToolDSV41 {
		// The tool reads every GGUF of the model from one directory and writes
		// their paths into the pack relative to it. The files in pack_args are
		// relative to the same directory, so run the tool there.
		abs, err := filepath.Abs(filepath.Dir(b.gguf))
		if err != nil {
			return err
		}
		if stage, err = filepath.Abs(stage); err != nil {
			return err
		}
		dir = abs
		args = append([]string{b.script, "--model", abs, "--out", stage}, b.args...)
	}
	xlog.Info("strata: building the pack from the GGUF files (first load only)", "tool", b.tool, "gguf", filepath.Base(b.gguf))
	start := time.Now()
	cmd := exec.CommandContext(ctx, b.python, args...) // #nosec G204 -- the interpreter and the arguments come from the model config; the script path is under strata_root
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PYTHONUNBUFFERED=1")
	r, w := io.Pipe()
	cmd.Stdout, cmd.Stderr = w, w
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", b.python, err)
	}
	tail := make(chan []string, 1)
	go func() {
		var last []string
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			xlog.Info("strata: pack", "tool", b.tool, "line", line)
			if last = append(last, line); len(last) > packTailLines {
				last = last[1:]
			}
		}
		tail <- last
	}()
	werr := cmd.Wait()
	_ = w.Close()
	last := <-tail
	if werr != nil {
		why := werr.Error()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			why = "timed out after " + timeout.String() + " (raise pack_timeout)"
		}
		return fmt.Errorf("the pack tool failed: %s\n%s", why, strings.Join(last, "\n"))
	}
	if !packComplete(stage, b.tool) {
		return fmt.Errorf("the pack tool ended without a finished pack in %s\n%s", stage, strings.Join(last, "\n"))
	}
	if err := os.Rename(stage, b.dest); err != nil {
		return err
	}
	ok = true
	xlog.Info("strata: pack ready", "dir", b.dest, "seconds", int(time.Since(start).Seconds()))
	return nil
}
