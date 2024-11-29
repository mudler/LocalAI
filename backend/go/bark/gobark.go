package main

// #cgo CXXFLAGS: -I${SRCDIR}/../../../sources/bark.cpp/ -I${SRCDIR}/../../../sources/bark.cpp/encodec.cpp -I${SRCDIR}/../../../sources/bark.cpp/examples -I${SRCDIR}/../../../sources/bark.cpp/spm-headers
// #cgo LDFLAGS: -L${SRCDIR}/ -L${SRCDIR}/../../../sources/bark.cpp/build/examples -L${SRCDIR}/../../../sources/bark.cpp/build/encodec.cpp/ -lbark -lencodec -lcommon
// #include <gobark.h>
// #include <stdlib.h>
import "C"

import (
	"fmt"
	"unsafe"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

type Bark struct {
	base.SingleThread
	threads int
}

func (sd *Bark) Load(opts *pb.ModelOptions) error {

	sd.threads = int(opts.Threads)

	modelFile := C.CString(opts.ModelFile)
	defer C.free(unsafe.Pointer(modelFile))

	ret := C.load_model(modelFile)
	if ret != 0 {
		return fmt.Errorf("inference failed")
	}

	return nil
}

func (sd *Bark) TTS(opts *pb.TTSRequest) error {
	t := C.CString(opts.Text)
	defer C.free(unsafe.Pointer(t))

	dst := C.CString(opts.Dst)
	defer C.free(unsafe.Pointer(dst))

	threads := C.int(sd.threads)

	ret := C.tts(t, threads, dst)
	if ret != 0 {
		return fmt.Errorf("inference failed")
	}

	return nil
}
