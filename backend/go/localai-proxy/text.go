package main

import (
	"bufio"
	"context"
	"encoding/json"
	"math"
	"strings"

	"github.com/mudler/xlog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// textRequest is the body for /v1/chat/completions (Messages) and
// /v1/completions (Prompt). Zero sampling values are omitted so the upstream
// model's own config defaults apply, as they would for a direct caller.
// Temperature is the exception: 0 is a real choice (greedy decoding), and
// core always fills it from the model config, so it is always sent.
type textRequest struct {
	Model       string          `json:"model"`
	Messages    []chatMessage   `json:"messages,omitempty"`
	Prompt      string          `json:"prompt,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
	MaxTokens   int32           `json:"max_tokens,omitempty"`
	Temperature float32         `json:"temperature"`
	TopP        float32         `json:"top_p,omitempty"`
	TopK        int32           `json:"top_k,omitempty"`
	Seed        int32           `json:"seed,omitempty"`
	Stop        []string        `json:"stop,omitempty"`
	Tools       json.RawMessage `json:"tools,omitempty"`
	ToolChoice  json.RawMessage `json:"tool_choice,omitempty"`
	// StreamOptions is set on streamed requests: the upstream sends the
	// usage trailer only when include_usage asks for it.
	StreamOptions *streamOptions `json:"stream_options,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatMessage struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	Name       string     `json:"name,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
}

type toolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function"`
}

// textChoice covers both endpoints and both shapes: chat replies fill Message
// (or Delta when streaming), completions fill Text.
type textChoice struct {
	Text    string      `json:"text"`
	Message choiceDelta `json:"message"`
	Delta   choiceDelta `json:"delta"`
}

type choiceDelta struct {
	Content string `json:"content"`
	// LocalAI names the field "reasoning"; other OpenAI-compatible servers
	// use "reasoning_content".
	Reasoning        string     `json:"reasoning"`
	ReasoningContent string     `json:"reasoning_content"`
	ToolCalls        []toolCall `json:"tool_calls"`
}

type textResponse struct {
	// Error is set on the frame LocalAI sends when generation fails after
	// the stream has started (followed by [DONE]).
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
	Choices []textChoice `json:"choices"`
	Usage   *struct {
		PromptTokens     int32 `json:"prompt_tokens"`
		CompletionTokens int32 `json:"completion_tokens"`
	} `json:"usage"`
}

// textRequest picks the chat endpoint when core sent structured messages
// (the model uses the tokenizer template, so the upstream must template
// too); otherwise core already rendered the prompt and completions takes it
// verbatim.
func (p *LocalAIProxy) textRequest(opts *pb.PredictOptions, stream bool) (string, textRequest) {
	if dropped := unforwardedFields(opts); len(dropped) > 0 {
		xlog.Warn("localai-proxy: request fields are not forwarded upstream", "fields", dropped)
	}
	req := textRequest{
		Model:       p.model(""),
		Stream:      stream,
		MaxTokens:   opts.GetTokens(),
		Temperature: opts.GetTemperature(),
		TopP:        opts.GetTopP(),
		TopK:        opts.GetTopK(),
		Seed:        opts.GetSeed(),
		Stop:        opts.GetStopPrompts(),
		Tools:       rawJSON(opts.GetTools()),
		ToolChoice:  rawJSON(opts.GetToolChoice()),
	}
	if stream {
		req.StreamOptions = &streamOptions{IncludeUsage: true}
	}
	if len(opts.GetMessages()) == 0 {
		req.Prompt = opts.GetPrompt()
		return "/v1/completions", req
	}
	for _, m := range opts.GetMessages() {
		msg := chatMessage{
			Role:       m.GetRole(),
			Content:    m.GetContent(),
			Name:       m.GetName(),
			ToolCallID: m.GetToolCallId(),
		}
		// A previous assistant turn carries its tool calls as a JSON string.
		if tc := m.GetToolCalls(); tc != "" {
			if err := json.Unmarshal([]byte(tc), &msg.ToolCalls); err != nil {
				xlog.Debug("localai-proxy: drop malformed tool_calls on message", "error", err)
			}
		}
		req.Messages = append(req.Messages, msg)
	}
	return "/v1/chat/completions", req
}

// unforwardedFields names the request inputs the REST text endpoints cannot
// carry from here: a grammar core compiled locally, and media that core hands
// over as local paths or base64 outside the messages. They are dropped, so
// say so instead of letting the answer silently ignore them.
func unforwardedFields(opts *pb.PredictOptions) []string {
	var out []string
	if opts.GetGrammar() != "" {
		out = append(out, "grammar")
	}
	if len(opts.GetImages()) > 0 {
		out = append(out, "images")
	}
	if len(opts.GetAudios()) > 0 {
		out = append(out, "audios")
	}
	if len(opts.GetVideos()) > 0 {
		out = append(out, "videos")
	}
	return out
}

// rawJSON passes a JSON string through untouched, or omits it when it is
// empty or invalid rather than failing the whole request.
func rawJSON(s string) json.RawMessage {
	if s == "" || !json.Valid([]byte(s)) {
		return nil
	}
	return json.RawMessage(s)
}

// replyFromChoice builds the Reply for one choice. Reasoning and tool calls
// travel as ChatDeltas, the same shape the llama.cpp autoparser emits, so
// core handles them without parsing the text again.
func replyFromChoice(c textChoice, streaming bool) *pb.Reply {
	d := c.Message
	if streaming {
		d = c.Delta
	}
	content := d.Content
	if content == "" {
		content = c.Text
	}
	reasoning := d.Reasoning
	if reasoning == "" {
		reasoning = d.ReasoningContent
	}

	reply := &pb.Reply{Message: []byte(content)}
	// Streaming chunks always carry a delta, like the autoparser's. A
	// complete reply only needs one for reasoning or tool calls; its
	// content must then be in the delta too, because core takes the
	// content from the deltas once they carry anything.
	if reasoning == "" && len(d.ToolCalls) == 0 && (!streaming || content == "") {
		return reply
	}
	delta := &pb.ChatDelta{Content: content, ReasoningContent: reasoning}
	for _, tc := range d.ToolCalls {
		delta.ToolCalls = append(delta.ToolCalls, &pb.ToolCallDelta{
			Index:     clampInt32(tc.Index),
			Id:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}
	reply.ChatDeltas = []*pb.ChatDelta{delta}
	return reply
}

func (p *LocalAIProxy) PredictRich(opts *pb.PredictOptions) (*pb.Reply, error) {
	path, body := p.textRequest(opts, false)
	var resp textResponse
	if err := p.postJSON(context.Background(), path, body, &resp); err != nil {
		return nil, err
	}
	if len(resp.Choices) == 0 {
		return nil, status.Errorf(codes.Internal, "localai-proxy: upstream %s returned no choices", path)
	}
	reply := replyFromChoice(resp.Choices[0], false)
	if resp.Usage != nil {
		reply.PromptTokens = resp.Usage.PromptTokens
		reply.Tokens = resp.Usage.CompletionTokens
	}
	return reply, nil
}

// PredictStreamRich sends one Reply per upstream SSE delta. It does not close
// results: the gRPC server does, after this returns.
func (p *LocalAIProxy) PredictStreamRich(opts *pb.PredictOptions, results chan<- *pb.Reply) error {
	path, body := p.textRequest(opts, true)
	resp, err := p.postStream(context.Background(), path, body)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	scanner := bufio.NewScanner(resp.Body)
	// A single frame can carry a long tool-call argument chunk.
	scanner.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for scanner.Scan() {
		payload, ok := strings.CutPrefix(scanner.Text(), "data:")
		if !ok {
			continue
		}
		payload = strings.TrimSpace(payload)
		if payload == "[DONE]" {
			return nil
		}
		if payload == "" {
			continue
		}
		var chunk textResponse
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			xlog.Debug("localai-proxy: skip malformed SSE frame", "path", path, "error", err)
			continue
		}
		if chunk.Error != nil {
			// The upstream failed mid-generation. Returning nil would turn a
			// cut-off answer into a success; Unavailable lets failover and
			// the client see the failure.
			xlog.Warn("localai-proxy: upstream stream error", "path", path, "error", chunk.Error.Message)
			return status.Errorf(codes.Unavailable, "localai-proxy: upstream %s stream failed: %s", path, chunk.Error.Message)
		}
		if chunk.Usage != nil && len(chunk.Choices) == 0 {
			results <- &pb.Reply{PromptTokens: chunk.Usage.PromptTokens, Tokens: chunk.Usage.CompletionTokens}
			continue
		}
		for _, c := range chunk.Choices {
			reply := replyFromChoice(c, true)
			if len(reply.GetMessage()) == 0 && len(reply.GetChatDeltas()) == 0 {
				continue // role-only or finish frames carry nothing to emit
			}
			results <- reply
		}
	}
	if err := scanner.Err(); err != nil {
		return transportError(path, err)
	}
	return nil
}

// Predict is the legacy string path; the gRPC server prefers PredictRich.
func (p *LocalAIProxy) Predict(opts *pb.PredictOptions) (string, error) {
	reply, err := p.PredictRich(opts)
	if err != nil {
		return "", err
	}
	return string(reply.GetMessage()), nil
}

// PredictStream is the legacy string stream. Unlike PredictStreamRich it
// owns and closes results, per the AIModel contract.
func (p *LocalAIProxy) PredictStream(opts *pb.PredictOptions, results chan string) error {
	defer close(results)
	rich := make(chan *pb.Reply)
	errCh := make(chan error, 1)
	go func() {
		errCh <- p.PredictStreamRich(opts, rich)
		close(rich)
	}()
	for reply := range rich {
		if msg := reply.GetMessage(); len(msg) > 0 {
			results <- string(msg)
		}
	}
	return <-errCh
}

func (p *LocalAIProxy) Embeddings(opts *pb.PredictOptions) ([]float32, error) {
	var resp struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	body := map[string]any{"model": p.model(""), "input": opts.GetEmbeddings()}
	// Core sends tokenized input in EmbeddingTokens and leaves Embeddings
	// empty; a list of token lists is how the REST API takes tokens.
	if tokens := opts.GetEmbeddingTokens(); len(tokens) > 0 {
		body["input"] = [][]int32{tokens}
	}
	if err := p.postJSON(context.Background(), "/v1/embeddings", body, &resp); err != nil {
		return nil, err
	}
	if len(resp.Data) == 0 || len(resp.Data[0].Embedding) == 0 {
		return nil, status.Error(codes.Internal, "localai-proxy: upstream /v1/embeddings returned no embedding")
	}
	return resp.Data[0].Embedding, nil
}

func (p *LocalAIProxy) Rerank(ctx context.Context, in *pb.RerankRequest) (*pb.RerankResult, error) {
	body := map[string]any{
		"model":     p.model(""),
		"query":     in.GetQuery(),
		"documents": in.GetDocuments(),
	}
	// TopN 0 means "score every document" (the router's reranker sends it);
	// upstream rejects top_n < 1, and an absent top_n means the same thing.
	if n := in.GetTopN(); n > 0 {
		body["top_n"] = n
	}
	var resp struct {
		Usage struct {
			TotalTokens  int32 `json:"total_tokens"`
			PromptTokens int32 `json:"prompt_tokens"`
		} `json:"usage"`
		Results []struct {
			Index    int32 `json:"index"`
			Document struct {
				Text string `json:"text"`
			} `json:"document"`
			RelevanceScore float32 `json:"relevance_score"`
		} `json:"results"`
	}
	if err := p.postJSON(ctx, "/v1/rerank", body, &resp); err != nil {
		return nil, err
	}
	out := &pb.RerankResult{Usage: &pb.Usage{TotalTokens: resp.Usage.TotalTokens, PromptTokens: resp.Usage.PromptTokens}}
	for _, r := range resp.Results {
		out.Results = append(out.Results, &pb.DocumentResult{Index: r.Index, Text: r.Document.Text, RelevanceScore: r.RelevanceScore})
	}
	return out, nil
}

// TokenizeString uses the upstream model's tokenizer, which is the one that
// matters: the upstream is where the tokens will be spent.
func (p *LocalAIProxy) TokenizeString(opts *pb.PredictOptions) (pb.TokenizationResponse, error) {
	var resp struct {
		Tokens []int32 `json:"tokens"`
	}
	body := map[string]any{"model": p.model(""), "content": opts.GetPrompt()}
	if err := p.postJSON(context.Background(), "/v1/tokenize", body, &resp); err != nil {
		return pb.TokenizationResponse{}, err
	}
	return pb.TokenizationResponse{Length: clampInt32(len(resp.Tokens)), Tokens: resp.Tokens}, nil
}

func (p *LocalAIProxy) Detokenize(in *pb.DetokenizeRequest) (pb.DetokenizeResponse, error) {
	var resp struct {
		Content string `json:"content"`
	}
	body := map[string]any{"model": p.model(""), "tokens": in.GetTokens()}
	if err := p.postJSON(context.Background(), "/v1/detokenize", body, &resp); err != nil {
		return pb.DetokenizeResponse{}, err
	}
	return pb.DetokenizeResponse{Content: resp.Content}, nil
}

// Score forwards plain candidate scoring to /api/score. That endpoint has no
// decision-pipeline fields, so a question_type request would silently become
// plain scoring upstream; refuse it as a capability gap instead.
func (p *LocalAIProxy) Score(ctx context.Context, in *pb.ScoreRequest) (*pb.ScoreResponse, error) {
	if in.GetQuestionType() != "" {
		return nil, unimplemented("Score with question_type")
	}
	body := map[string]any{
		"model":                  p.model(""),
		"prompt":                 in.GetPrompt(),
		"candidates":             in.GetCandidates(),
		"include_token_logprobs": in.GetIncludeTokenLogprobs(),
		"length_normalize":       in.GetLengthNormalize(),
	}
	var resp struct {
		Candidates []struct {
			LogProb                 float64 `json:"log_prob"`
			LengthNormalizedLogProb float64 `json:"length_normalized_log_prob"`
			NumTokens               int32   `json:"num_tokens"`
			Tokens                  []struct {
				Token   string  `json:"token"`
				LogProb float64 `json:"log_prob"`
			} `json:"tokens"`
		} `json:"candidates"`
	}
	if err := p.postJSON(ctx, "/api/score", body, &resp); err != nil {
		return nil, err
	}
	out := &pb.ScoreResponse{}
	for _, c := range resp.Candidates {
		cs := &pb.CandidateScore{LogProb: c.LogProb, LengthNormalizedLogProb: c.LengthNormalizedLogProb, NumTokens: c.NumTokens}
		for _, t := range c.Tokens {
			cs.Tokens = append(cs.Tokens, &pb.TokenLogProb{Token: t.Token, LogProb: t.LogProb})
		}
		out.Candidates = append(out.Candidates, cs)
	}
	return out, nil
}

// clampInt32 narrows an upstream-supplied int (token counts, tool-call
// indexes) to the int32 the gRPC protocol carries. The upstream is another
// server, so an absurd value must saturate rather than wrap to a negative or
// small number that core would take at face value.
func clampInt32(n int) int32 {
	switch {
	case n > math.MaxInt32:
		return math.MaxInt32
	case n < math.MinInt32:
		return math.MinInt32
	}
	return int32(n)
}
