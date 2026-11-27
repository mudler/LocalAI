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
//	MOCK_BENCH n=<tokens> reasoning=<tokens> tool=<0|1> deltas=<0|1>
//
// n content tokens of fixed text follow reasoning tokens wrapped in
// <think></think>. tool=1 ends the reply with a tool call streamed in small
// pieces, as a model writes it. deltas=1 sends the text as ChatDeltas with an
// empty message, as the llama.cpp autoparser does.
var benchMarker = regexp.MustCompile(`MOCK_BENCH((?:\s+[a-z]+=\d+)*)`)

type benchSpec struct {
	tokens    int
	reasoning int
	tool      bool
	deltas    bool
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
		}
	}
	return spec, true
}

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
	if spec.tool {
		for i := 0; i < len(benchToolCall); i += 4 {
			if err := send(benchToolCall[i:min(i+4, len(benchToolCall))], false); err != nil {
				return err
			}
		}
	}
	return nil
}
