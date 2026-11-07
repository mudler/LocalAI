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

// promptRequest is an OpenAI chat request, as the Strata frontend reads it.
// Image parts carry a placeholder URL; the helper returns the placeholders in
// prompt order so the caller can match them to the decoded images.
type promptRequest struct {
	Messages           []map[string]any `json:"messages"`
	Tools              any              `json:"tools,omitempty"`
	ToolChoice         any              `json:"tool_choice,omitempty"`
	ChatTemplateKwargs map[string]any   `json:"chat_template_kwargs,omitempty"`
	ReasoningEffort    string           `json:"reasoning_effort,omitempty"`
}

// promptResult is a rendered and tokenized request.
type promptResult struct {
	IDs      []int    `json:"ids"`
	Images   []string `json:"images"`   // image sources, in prompt order
	Thinking bool     `json:"thinking"` // the reply starts inside the model's reasoning
	Force    string   `json:"force"`    // text already appended to the prompt to force a tool call
	Tools    any      `json:"tools"`    // the tools as the output parser needs them
}

// parseEvent is one piece of classified model output.
type parseEvent struct {
	Kind string     `json:"kind"` // reasoning, content, tool_start, tool_args, tool_call
	Text string     `json:"text"`
	Call *parseCall `json:"call"`
}

type parseCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// outputParser splits the model's text into reasoning, content and tool calls.
type outputParser interface {
	Feed(text string) ([]parseEvent, error)
	// Finish flushes held text. reason is the engine's finish reason.
	Finish(reason string) ([]parseEvent, error)
	Close()
}

// Tokenizer turns requests into the token ids the engine takes, and back. The
// engine itself only reads and writes ids.
//
// Two implementations exist: helperTokenizer (below) drives Strata's own
// Python code; tests use an in-memory one. A native Go implementation could
// replace the helper without touching the rest of the backend; see the README
// for what that needs.
type Tokenizer interface {
	// Encode converts text to ids. parseSpecial makes control tokens such as
	// <|im_start|> match as single tokens, which a rendered chat prompt needs.
	Encode(text string, parseSpecial bool) ([]int, error)
	// TokenBytes returns the raw bytes of one token. A multi-byte character
	// can be split across tokens, so callers join bytes before decoding.
	TokenBytes(id int) ([]byte, bool)
	// Prompt renders a chat request with the model's own template and encodes it.
	Prompt(req promptRequest) (promptResult, error)
	// NewParser starts an output parser for one reply. tools is what Prompt
	// returned. opening is text the prompt already ended with, which the parser
	// reads as if the model had written it.
	NewParser(thinking bool, tools any, opening string) (outputParser, []parseEvent, error)
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
	eos    int
	hasEOS bool

	nextParser int
}

type helperReply struct {
	OK          bool           `json:"ok"`
	Error       string         `json:"error"`
	IDs         []int          `json:"ids"`
	Prompt      string         `json:"prompt"`
	Tokens      []string       `json:"tokens"`
	HasTemplate bool           `json:"has_template"`
	Images      []string       `json:"images"`
	Thinking    bool           `json:"thinking"`
	Force       string         `json:"force"`
	Tools       any            `json:"tools"`
	Events      []parseEvent   `json:"events"`
	SpecialIDs  map[string]int `json:"special_ids"`
}

// newHelperTokenizer starts `python -c <helper> <strataRoot> <pack> [family]`
// and loads the vocabulary table. Python needs the `regex` and `jinja2`
// packages. The family selects the chat template rules and the output parser
// of that model family in Strata's code (see the helper).
func newHelperTokenizer(python, strataRoot, pack, family string, timeout time.Duration) (*helperTokenizer, error) {
	python, err := resolveInterpreter(python)
	if err != nil {
		return nil, fmt.Errorf("tokenizer helper: python: %w", err)
	}
	argv := []string{"-c", helperScript, strataRoot, pack}
	if family == familyDeepSeek41 {
		argv = append(argv, family)
	}
	cmd := exec.Command(python, argv...) // #nosec G204 -- interpreter is a PATH name or an absolute file, no shell, script and paths passed as argv
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
			_ = h.Close()
			return nil, fmt.Errorf("tokenizer helper: %w", r.err)
		}
		if !r.r.OK {
			_ = h.Close()
			return nil, fmt.Errorf("tokenizer helper: %s", r.r.Error)
		}
	case <-time.After(timeout):
		_ = h.Close()
		return nil, fmt.Errorf("tokenizer helper did not start within %s", timeout)
	}

	var vr helperReply
	if err := h.call(map[string]any{"op": "vocab"}, &vr); err != nil {
		_ = h.Close()
		return nil, err
	}
	h.vocab = make([][]byte, len(vr.Tokens))
	for i, s := range vr.Tokens {
		b, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			_ = h.Close()
			return nil, fmt.Errorf("tokenizer helper: bad vocab entry %d: %w", i, err)
		}
		h.vocab[i] = b
	}
	var ir helperReply
	if err := h.call(map[string]any{"op": "info"}, &ir); err != nil {
		_ = h.Close()
		return nil, err
	}
	h.eos, h.hasEOS = ir.SpecialIDs["eos"]
	return h, nil
}

// EOSID is the end-of-sentence id the pack's tokenizer names, if it names one.
func (h *helperTokenizer) EOSID() (int, bool) { return h.eos, h.hasEOS }

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

func (h *helperTokenizer) Prompt(req promptRequest) (promptResult, error) {
	var r helperReply
	if err := h.call(map[string]any{"op": "prompt", "request": req}, &r); err != nil {
		return promptResult{}, err
	}
	return promptResult{IDs: r.IDs, Images: r.Images, Thinking: r.Thinking, Force: r.Force, Tools: r.Tools}, nil
}

type helperParser struct {
	h  *helperTokenizer
	id int
}

func (h *helperTokenizer) NewParser(thinking bool, tools any, opening string) (outputParser, []parseEvent, error) {
	h.mu.Lock()
	h.nextParser++
	id := h.nextParser
	h.mu.Unlock()
	var r helperReply
	err := h.call(map[string]any{"op": "parser_new", "id": id, "thinking": thinking, "tools": tools, "opening": opening}, &r)
	if err != nil {
		return nil, nil, err
	}
	return &helperParser{h: h, id: id}, r.Events, nil
}

func (p *helperParser) Feed(text string) ([]parseEvent, error) {
	var r helperReply
	if err := p.h.call(map[string]any{"op": "parser_feed", "id": p.id, "text": text}, &r); err != nil {
		return nil, err
	}
	return r.Events, nil
}

func (p *helperParser) Finish(reason string) ([]parseEvent, error) {
	var r helperReply
	if err := p.h.call(map[string]any{"op": "parser_finish", "id": p.id, "reason": reason}, &r); err != nil {
		return nil, err
	}
	return r.Events, nil
}

func (p *helperParser) Close() {
	var r helperReply
	_ = p.h.call(map[string]any{"op": "parser_drop", "id": p.id}, &r)
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
