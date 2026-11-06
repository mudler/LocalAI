package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	_ "embed"
)

// ChatMessage is one turn handed to the chat template. The fields follow the
// OpenAI message shape that Strata's template expects.
type ChatMessage struct {
	Role             string `json:"role"`
	Content          string `json:"content"`
	Name             string `json:"name,omitempty"`
	ToolCallID       string `json:"tool_call_id,omitempty"`
	ReasoningContent string `json:"reasoning_content,omitempty"`
	ToolCalls        any    `json:"tool_calls,omitempty"`
}

// Tokenizer turns text into the token ids the engine takes, and back. The
// engine itself only reads and writes ids.
//
// Two implementations exist: helperTokenizer (below) drives Strata's own
// Python tokenizer and template; tests use an in-memory one. A native Go
// implementation could replace the helper without touching the rest of the
// backend; see the README for what that needs.
type Tokenizer interface {
	// Encode converts text to ids. parseSpecial makes control tokens such as
	// <|im_start|> match as single tokens, which a rendered chat prompt needs.
	Encode(text string, parseSpecial bool) ([]int, error)
	// TokenBytes returns the raw bytes of one token. A multi-byte character
	// can be split across tokens, so callers join bytes before decoding.
	TokenBytes(id int) ([]byte, bool)
	// ApplyChatTemplate renders messages with the model's own template and
	// appends the assistant generation prompt. tools is a JSON array or "".
	ApplyChatTemplate(msgs []ChatMessage, tools string, kwargs map[string]any) (string, error)
	Close() error
}

//go:embed tokenizer_helper.py
var helperScript string

type helperTokenizer struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	mu     sync.Mutex
	vocab  [][]byte
}

type helperReply struct {
	OK          bool     `json:"ok"`
	Error       string   `json:"error"`
	IDs         []int    `json:"ids"`
	Prompt      string   `json:"prompt"`
	Tokens      []string `json:"tokens"`
	HasTemplate bool     `json:"has_template"`
}

// newHelperTokenizer starts `python -c <helper> <strataRoot> <pack>` and
// loads the vocabulary table. Python needs the `regex` and `jinja2` packages.
func newHelperTokenizer(python, strataRoot, pack string, timeout time.Duration) (*helperTokenizer, error) {
	if python == "" {
		python = "python3"
	}
	cmd := exec.Command(python, "-c", helperScript, strataRoot, pack)
	cmd.Stderr = os.Stderr
	setChildAttrs(cmd)
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start tokenizer helper: %w", err)
	}
	h := &helperTokenizer{cmd: cmd, stdin: in, stdout: bufio.NewReaderSize(out, 1<<20)}

	type res struct {
		r   helperReply
		err error
	}
	ch := make(chan res, 1)
	go func() {
		var r helperReply
		err := h.readJSON(&r)
		ch <- res{r, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			h.Close()
			return nil, fmt.Errorf("tokenizer helper: %w", r.err)
		}
		if !r.r.OK {
			h.Close()
			return nil, fmt.Errorf("tokenizer helper: %s", r.r.Error)
		}
	case <-time.After(timeout):
		h.Close()
		return nil, fmt.Errorf("tokenizer helper did not start within %s", timeout)
	}

	var vr helperReply
	if err := h.call(map[string]any{"op": "vocab"}, &vr); err != nil {
		h.Close()
		return nil, err
	}
	h.vocab = make([][]byte, len(vr.Tokens))
	for i, s := range vr.Tokens {
		b, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			h.Close()
			return nil, fmt.Errorf("tokenizer helper: bad vocab entry %d: %w", i, err)
		}
		h.vocab[i] = b
	}
	return h, nil
}

func (h *helperTokenizer) readJSON(v any) error {
	line, err := h.stdout.ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	return json.Unmarshal(line, v)
}

func (h *helperTokenizer) call(req any, reply *helperReply) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	b, err := json.Marshal(req)
	if err != nil {
		return err
	}
	if _, err := h.stdin.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("tokenizer helper write: %w", err)
	}
	if err := h.readJSON(reply); err != nil {
		return fmt.Errorf("tokenizer helper: %w", err)
	}
	if !reply.OK {
		return errors.New("tokenizer helper: " + reply.Error)
	}
	return nil
}

func (h *helperTokenizer) Encode(text string, parseSpecial bool) ([]int, error) {
	var r helperReply
	if err := h.call(map[string]any{"op": "encode", "text": text, "parse_special": parseSpecial}, &r); err != nil {
		return nil, err
	}
	return r.IDs, nil
}

func (h *helperTokenizer) TokenBytes(id int) ([]byte, bool) {
	if id < 0 || id >= len(h.vocab) {
		return nil, false
	}
	return h.vocab[id], true
}

func (h *helperTokenizer) ApplyChatTemplate(msgs []ChatMessage, tools string, kwargs map[string]any) (string, error) {
	req := map[string]any{"op": "template", "messages": msgs, "kwargs": kwargs}
	if tools != "" {
		var t any
		if err := json.Unmarshal([]byte(tools), &t); err != nil {
			return "", fmt.Errorf("tools is not valid JSON: %w", err)
		}
		req["tools"] = t
	}
	var r helperReply
	if err := h.call(req, &r); err != nil {
		return "", err
	}
	return r.Prompt, nil
}

func (h *helperTokenizer) Close() error {
	if h.cmd.Process != nil {
		_ = h.stdin.Close()
		done := make(chan struct{})
		go func() { _ = h.cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = h.cmd.Process.Kill()
			<-done
		}
	}
	return nil
}
