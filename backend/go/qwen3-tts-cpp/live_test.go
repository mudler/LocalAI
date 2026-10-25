package main

import (
	"context"
	"errors"
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	"io"
	"reflect"
	"unsafe"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

var _ = Describe("TTSLiveAdapter", func() {
	It("preserves the live synthesis contract", func() {
		old := CppTTSLive
		defer func() { CppTTSLive = old }()
		var got []string
		CppTTSLive = func(_, _, _ string, _ unsafe.Pointer, _ int, _ string, _ int64, _ float32, _ int, _, _ float32, _ int, _, _, _ uintptr) int {
			for {
				var text unsafe.Pointer
				var size uintptr
				result := nextLiveText(0, unsafe.Pointer(&text), unsafe.Pointer(&size))
				if result == 0 {
					return 0
				}
				if result != 1 {
					return 1
				}
				got = append(got, string(unsafe.Slice((*byte)(text), int(size))))
				samples := []float32{0.1, 0.2}
				if sendLiveAudio(&samples[0], 2, 0) == 0 {
					return 2
				}
			}
		}
		input := []*pb.TTSRequest{{Text: "early "}, {Text: "late"}}
		chunks := 0
		err := (&Qwen3TtsCpp{}).TTSLive(context.Background(), func() (*pb.TTSRequest, error) {
			if len(input) == 0 {
				return nil, io.EOF
			}
			r := input[0]
			input = input[1:]
			return r, nil
		}, func([]byte) error { chunks++; return nil })
		if err != nil {
			Fail(fmt.Sprint(err))
		}
		if !reflect.DeepEqual(got, []string{"early ", "late"}) || chunks != 3 {
			Fail(fmt.Sprintf("input=%v chunks=%d", got, chunks))
		}
		// A downstream failure must stop the native callback and preserve the exact
		// error, rather than reporting successful synthesis or consuming more input.
		boom := errors.New("audio consumer failed")
		chunks = 0
		err = (&Qwen3TtsCpp{}).TTSLive(context.Background(), func() (*pb.TTSRequest, error) { return &pb.TTSRequest{Text: "text"}, nil }, func([]byte) error {
			chunks++
			if chunks > 1 {
				return boom
			}
			return nil
		})
		if !errors.Is(err, boom) {
			Fail(fmt.Sprint(err))
		}
	})
})
