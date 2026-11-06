package main

import (
	"encoding/json"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// deltaOut is what one batch of parser events adds to the reply.
type deltaOut struct {
	content, reasoning string
	calls              []*pb.ToolCallDelta
}

func (d deltaOut) empty() bool {
	return d.content == "" && d.reasoning == "" && len(d.calls) == 0
}

func (d deltaOut) proto() *pb.ChatDelta {
	return &pb.ChatDelta{Content: d.content, ReasoningContent: d.reasoning, ToolCalls: d.calls}
}

// deltaBuilder turns parser events into chat deltas.
type deltaBuilder struct {
	index   map[string]int32 // tool call id -> index
	started map[string]bool
}

func newDeltaBuilder() *deltaBuilder {
	return &deltaBuilder{index: map[string]int32{}, started: map[string]bool{}}
}

func (b *deltaBuilder) add(evs []parseEvent) deltaOut {
	var d deltaOut
	for _, ev := range evs {
		switch ev.Kind {
		case "content":
			d.content += ev.Text
		case "reasoning":
			d.reasoning += ev.Text
		case "tool_start":
			if ev.Call == nil {
				continue
			}
			i := int32(len(b.index)) // #nosec G115 -- token ids and list indexes are far below the int32 range
			b.index[ev.Call.ID] = i
			b.started[ev.Call.ID] = true
			d.calls = append(d.calls, &pb.ToolCallDelta{Index: i, Id: ev.Call.ID, Name: ev.Call.Name})
		case "tool_args":
			if ev.Call == nil {
				continue
			}
			i, ok := b.index[ev.Call.ID]
			if !ok {
				continue
			}
			if n := len(d.calls); n > 0 && d.calls[n-1].Index == i && d.calls[n-1].Id == "" {
				d.calls[n-1].Arguments += ev.Text
				continue
			}
			d.calls = append(d.calls, &pb.ToolCallDelta{Index: i, Arguments: ev.Text})
		case "tool_call":
			if ev.Call == nil || b.started[ev.Call.ID] {
				continue // its arguments were streamed already
			}
			i := int32(len(b.index)) // #nosec G115 -- token ids and list indexes are far below the int32 range
			b.index[ev.Call.ID] = i
			b.started[ev.Call.ID] = true
			args := string(ev.Call.Arguments)
			if args == "" {
				args = "{}"
			}
			d.calls = append(d.calls, &pb.ToolCallDelta{Index: i, Id: ev.Call.ID, Name: ev.Call.Name, Arguments: args})
		}
	}
	return d
}

// deltaAggregate folds a stream of deltas into one, for non-streaming replies.
type deltaAggregate struct {
	content, reasoning string
	calls              []*pb.ToolCallDelta
}

func (a *deltaAggregate) add(d *pb.ChatDelta) {
	if d == nil {
		return
	}
	a.content += d.Content
	a.reasoning += d.ReasoningContent
	for _, c := range d.ToolCalls {
		for int(c.Index) >= len(a.calls) {
			a.calls = append(a.calls, &pb.ToolCallDelta{Index: int32(len(a.calls))}) // #nosec G115 -- token ids and list indexes are far below the int32 range
		}
		t := a.calls[c.Index]
		if c.Id != "" {
			t.Id = c.Id
		}
		if c.Name != "" {
			t.Name = c.Name
		}
		t.Arguments += c.Arguments
	}
}

func (a *deltaAggregate) delta() *pb.ChatDelta {
	if a.content == "" && a.reasoning == "" && len(a.calls) == 0 {
		return nil
	}
	return &pb.ChatDelta{Content: a.content, ReasoningContent: a.reasoning, ToolCalls: a.calls}
}

// logprobCollector builds the OpenAI-shaped logprobs LocalAI decodes from
// Reply.Logprobs.
type logprobCollector struct {
	content []logprobEntry
}

type logprobEntry struct {
	ID          int32          `json:"id"`
	Token       string         `json:"token"`
	Bytes       []int          `json:"bytes,omitempty"`
	Logprob     float64        `json:"logprob"`
	TopLogprobs []logprobEntry `json:"top_logprobs,omitempty"`
}

func newLogprobEntry(tok Tokenizer, id int, lp float64) logprobEntry {
	e := logprobEntry{ID: int32(id), Logprob: lp} // #nosec G115 -- token ids and list indexes are far below the int32 range
	if b, ok := tok.TokenBytes(id); ok {
		e.Token = string(b)
		e.Bytes = make([]int, len(b))
		for i, c := range b {
			e.Bytes[i] = int(c)
		}
	}
	return e
}

func (c *logprobCollector) add(tok Tokenizer, t genToken, topN int) {
	e := newLogprobEntry(tok, t.ID, t.LP)
	for i, a := range t.Top {
		if i >= topN {
			break
		}
		e.TopLogprobs = append(e.TopLogprobs, newLogprobEntry(tok, a.ID, a.LP))
	}
	c.content = append(c.content, e)
}

func (c *logprobCollector) json() []byte {
	if len(c.content) == 0 {
		return nil
	}
	b, err := json.Marshal(struct {
		Content []logprobEntry `json:"content"`
	}{c.content})
	if err != nil {
		return nil
	}
	return b
}
