package e2ebackends_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// Helpers for the paged serving specs (capabilities paged_overcommit,
// paged_parity and paged_budget). They drive several greedy streams at once
// against one loaded model, which is what the llama.cpp paged_kv option
// schedules.

// pagedStream is the outcome of one streamed request.
type pagedStream struct {
	text  string
	chunk []time.Time // arrival time of every non-empty chunk
	sent  time.Time
	err   error
}

// pagedPredict streams one greedy request that ignores EOS, so every run of
// the same prompt asks for the same number of tokens.
func pagedPredict(client pb.BackendClient, prompt string, tokens int32, timeout time.Duration) pagedStream {
	return pagedPredictOpts(client, &pb.PredictOptions{
		Prompt:      prompt,
		Tokens:      tokens,
		Temperature: 0,
		TopK:        1,
		Seed:        1,
		IgnoreEOS:   true,
	}, timeout)
}

func pagedPredictOpts(client pb.BackendClient, opts *pb.PredictOptions, timeout time.Duration) pagedStream {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out := pagedStream{sent: time.Now()}
	stream, err := client.PredictStream(ctx, opts)
	if err != nil {
		out.err = err
		return out
	}
	var b strings.Builder
	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			out.err = err
			break
		}
		if len(msg.GetMessage()) > 0 {
			b.Write(msg.GetMessage())
			out.chunk = append(out.chunk, time.Now())
		}
	}
	out.text = b.String()
	return out
}

// pagedPredictCached is pagedPredict with the prompt cache on.
func pagedPredictCached(client pb.BackendClient, prompt string, tokens int32, timeout time.Duration) pagedStream {
	return pagedPredictOpts(client, &pb.PredictOptions{
		Prompt: prompt, Tokens: tokens, Temperature: 0, TopK: 1, Seed: 1, IgnoreEOS: true, PromptCacheAll: true,
	}, timeout)
}

// pagedConcurrent runs all prompts at once and returns the results in prompt order.
func pagedConcurrent(client pb.BackendClient, prompts []string, tokens int32, timeout time.Duration) []pagedStream {
	res := make([]pagedStream, len(prompts))
	var wg sync.WaitGroup
	for i, p := range prompts {
		wg.Add(1)
		go func(i int, p string) {
			defer wg.Done()
			res[i] = pagedPredict(client, p, tokens, timeout)
		}(i, p)
	}
	wg.Wait()
	return res
}

// pagedPrompt builds a distinct prompt of about nWords words.
func pagedPrompt(i, nWords int) string {
	words := strings.Fields("river stone cloud engine paper garden signal winter bridge planet memory kernel vector light window market")
	var b strings.Builder
	fmt.Fprintf(&b, "Request %d. Continue the list:", i)
	for k := 0; k < nWords; k++ {
		b.WriteByte(' ')
		b.WriteString(words[(i*7+k*3)%len(words)])
	}
	return b.String()
}

// pagedOption returns the integer value of name:value in the LoadModel options, or def.
func pagedOption(options []string, name string, def int) int {
	for _, o := range options {
		k, v, ok := strings.Cut(o, ":")
		if ok && k == name {
			if n, err := strconv.Atoi(v); err == nil {
				return n
			}
		}
	}
	return def
}

// chunksBetween counts the chunks of s that arrived in [from, to].
func chunksBetween(s pagedStream, from, to time.Time) int {
	n := 0
	for _, t := range s.chunk {
		if !t.Before(from) && !t.After(to) {
			n++
		}
	}
	return n
}

// pagedImage is a small base64 PNG (a colour gradient) for the image specs.
func pagedImage() string {
	img := image.NewRGBA(image.Rect(0, 0, 128, 128))
	for y := 0; y < 128; y++ {
		for x := 0; x < 128; x++ {
			img.Set(x, y, color.RGBA{R: uint8(2 * x), G: uint8(2 * y), B: 128, A: 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		panic(err)
	}
	return base64.StdEncoding.EncodeToString(b.Bytes())
}
