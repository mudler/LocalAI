package main

import (
	"context"
	"encoding/binary"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	"io"
	"os"
	"sync"
	"time"

	"github.com/ebitengine/purego"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// Opt-in real-model check. The input producer cannot send late text or EOF
// until it observes actual PCM, so a whole-text implementation deadlocks here.
var _ = Describe("TTSLiveNative", func() {
	It("preserves the live synthesis contract", func() {
		if os.Getenv("QWEN3TTS_LIVE_TEST") != "1" {
			Skip(fmt.Sprint("set QWEN3TTS_LIVE_TEST=1 and model/codec/library paths"))
		}
		lib, e := purego.Dlopen(os.Getenv("QWEN3TTS_LIBRARY"), purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if e != nil {
			Fail(fmt.Sprint(e))
		}
		purego.RegisterLibFunc(&CppLoad, lib, "qt3_load")
		purego.RegisterLibFunc(&CppUnload, lib, "qt3_unload")
		purego.RegisterLibFunc(&CppTTSLive, lib, "qt3_tts_live")
		if rc := CppLoad(os.Getenv("QWEN3TTS_MODEL"), os.Getenv("QWEN3TTS_CODEC"), 1, 0); rc != 0 {
			Fail(fmt.Sprint("load", rc))
		}
		defer CppUnload()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		input := make(chan *pb.TTSRequest)
		early := make(chan struct{})
		var once sync.Once
		go func() {
			select {
			case input <- &pb.TTSRequest{Voice: "ryan", Language: func() *string { s := "english"; return &s }(), Text: "Hello there. ", Params: map[string]string{"max_new_tokens": "160", "seed": "42"}}:
			case <-ctx.Done():
				return
			}
			select {
			case <-early:
			case <-ctx.Done():
				return
			}
			select {
			case input <- &pb.TTSRequest{Text: "This is a streaming test. "}:
			case <-ctx.Done():
				return
			}
			close(input)
		}()
		chunks, bytes := 0, 0
		var output []byte
		outputPath := os.Getenv("QWEN3TTS_LIVE_OUTPUT")
		b := &Qwen3TtsCpp{}
		e = b.TTSLive(ctx, func() (*pb.TTSRequest, error) {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case req, ok := <-input:
				if !ok {
					return nil, io.EOF
				}
				return req, nil
			}
		}, func(audio []byte) error {
			if outputPath != "" {
				output = append(output, audio...)
			}
			// The first callback is only a WAV header and cannot open the audio gate.
			if chunks > 0 {
				bytes += len(audio)
				once.Do(func() { close(early) })
			}
			chunks++
			return nil
		})
		if e != nil {
			Fail(fmt.Sprint(e))
		}
		if bytes == 0 {
			Fail(fmt.Sprint("no PCM"))
		}
		if outputPath != "" {
			binary.LittleEndian.PutUint32(output[4:8], uint32(len(output)-8))
			binary.LittleEndian.PutUint32(output[40:44], uint32(len(output)-44))
			if err := os.WriteFile(outputPath, output, 0600); err != nil {
				Fail(err.Error())
			}
		}
		_, _ = fmt.Fprintf(GinkgoWriter, "PCM bytes=%d callbacks=%d; late text and EOS sent after first PCM", bytes, chunks)
	})
})
