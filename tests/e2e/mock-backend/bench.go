package main

import (
	"regexp"
	"strconv"
	"strings"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// benchMarker selects the load-test stream: the backend streams as fast as the
// client reads, so the time and CPU a request costs are those of LocalAI.
//
//	MOCK_BENCH n=<tokens> reasoning=<tokens> tool=<0|1> deltas=<0|1> calls=<0|1> msg=<0|1>
//
// n content tokens of fixed text follow reasoning tokens wrapped in
// <think></think>. tool=1 ends the reply with a tool call streamed in small
// pieces, as a model writes it. deltas=1 sends the text as ChatDeltas with an
// empty message, as the llama.cpp autoparser does. calls=1 with deltas=1 sends
// the tool call as ToolCallDeltas, name first and then the arguments in
// pieces, as the autoparser does for a call it recognised. With a tokenizer
// msg=1 with deltas=1 also puts the content text in the message, as the
// llama.cpp gRPC server does (it sends the content delta there and the
// reasoning only in ChatDeltas). With a tokenizer
// template the prompt is empty, and the marker is read from the last message.
var benchMarker = regexp.MustCompile(`MOCK_BENCH((?:\s+[a-z]+=\d+)*)`)

type benchSpec struct {
	tokens    int
	reasoning int
	tool      bool
	deltas    bool
	calls     bool
	msg       bool
}

func benchPrompt(in *pb.PredictOptions) string {
	if in.Prompt != "" || len(in.Messages) == 0 {
		return in.Prompt
	}
	return in.Messages[len(in.Messages)-1].Content
}

func parseBenchSpec(prompt string) (benchSpec, bool) {
	all := benchMarker.FindAllStringSubmatch(prompt, -1)
	if len(all) == 0 {
		return benchSpec{}, false
	}
	spec := benchSpec{tokens: 1}
	for _, field := range strings.Fields(all[len(all)-1][1]) {
		k, v, _ := strings.Cut(field, "=")
		n, _ := strconv.Atoi(v)
		switch k {
		case "n":
			spec.tokens = n
		case "reasoning":
			spec.reasoning = n
		case "tool":
			spec.tool = n != 0
		case "deltas":
			spec.deltas = n != 0
		case "calls":
			spec.calls = n != 0
		case "msg":
			spec.msg = n != 0
		}
	}
	return spec, true
}

const benchToolArgs = `{"location": "San Francisco", "unit": "celsius", "days": 3}`

const benchToolCall = `{"name": "get_weather", "arguments": {"location": "San Francisco", "unit": "celsius", "days": 3}}`

func streamBench(spec benchSpec, stream pb.Backend_PredictStreamServer) error {
	count := int32(0)
	send := func(text string, reasoning bool) error {
		count++
		reply := &pb.Reply{Tokens: count, PromptTokens: 12}
		if spec.deltas {
			d := &pb.ChatDelta{}
			if reasoning {
				d.ReasoningContent = text
			} else {
				d.Content = text
				if spec.msg {
					reply.Message = []byte(text)
				}
			}
			reply.ChatDeltas = []*pb.ChatDelta{d}
		} else {
			reply.Message = []byte(text)
		}
		return stream.Send(reply)
	}

	if spec.reasoning > 0 {
		if !spec.deltas {
			if err := send("<think>", true); err != nil {
				return err
			}
		}
		for i := 0; i < spec.reasoning; i++ {
			if err := send(" mull", true); err != nil {
				return err
			}
		}
		if !spec.deltas {
			if err := send("</think>", true); err != nil {
				return err
			}
		}
	}
	for i := 0; i < spec.tokens; i++ {
		if err := send(" word", false); err != nil {
			return err
		}
	}
	if spec.tool && spec.deltas && spec.calls {
		count++
		if err := stream.Send(&pb.Reply{Tokens: count, PromptTokens: 12, ChatDeltas: []*pb.ChatDelta{{
			ToolCalls: []*pb.ToolCallDelta{{Index: 0, Id: "call_bench", Name: "get_weather"}},
		}}}); err != nil {
			return err
		}
		for i := 0; i < len(benchToolArgs); i += 4 {
			count++
			if err := stream.Send(&pb.Reply{Tokens: count, PromptTokens: 12, ChatDeltas: []*pb.ChatDelta{{
				ToolCalls: []*pb.ToolCallDelta{{Index: 0, Arguments: benchToolArgs[i:min(i+4, len(benchToolArgs))]}},
			}}}); err != nil {
				return err
			}
		}
		return nil
	}
	if spec.tool {
		for i := 0; i < len(benchToolCall); i += 4 {
			if err := send(benchToolCall[i:min(i+4, len(benchToolCall))], false); err != nil {
				return err
			}
		}
	}
	return nil
}
