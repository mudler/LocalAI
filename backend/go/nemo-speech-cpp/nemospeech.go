package main

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/xlog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// family is the model family selected at load time from the GGUF architecture.
type family int

const (
	familyUnknown family = iota
	familyASR
	familyDiarization
	familyTTS
	familyNMT
)

func (f family) String() string {
	switch f {
	case familyASR:
		return "asr"
	case familyDiarization:
		return "diarization"
	case familyTTS:
		return "tts"
	case familyNMT:
		return "nmt"
	}
	return "unknown"
}

// NemoSpeech is one loaded model. Exactly one of the handles is non-zero,
// matching fam.
type NemoSpeech struct {
	base.SingleThread

	fam  family
	opts loadOptions

	// engineMu guards fam and the handles, and serializes calls into the C
	// runtime for this model. Its participants today are withEngine and Free;
	// the per-family RPCs in Tasks 6 to 9 join it by routing through withEngine.
	engineMu sync.Mutex

	// synth and nmt are shortened rather than spelled out: synthesizer and
	// translator are the names of the two RPC-side interfaces those handles are
	// wrapped in (tts.go, nmt.go), and a field sharing a name with an interface in
	// the same package makes every construction site read as a conversion.
	recognizer uintptr
	diarizer   uintptr
	synth      uintptr
	nmt        uintptr
}

// cstr allocates a NUL-terminated C string and returns its pointer plus a
// release function. The empty string maps to a null pointer because the C API
// treats NULL and "" as equivalent for every optional field.
//
// The address leaves the Go type system as a uintptr, which the collector does
// not trace, so the bytes are pinned for as long as C may read them. Pinning is
// the only mechanism with a documented guarantee here: the config structs hold
// raw addresses, and an unpinned Go allocation is free to be collected (and, in
// principle, moved) the moment its last traced reference dies.
//
// The returned pointer is for C only, and the direction is one-way. Converting
// it back to an unsafe.Pointer to read the bytes from Go is checked by checkptr
// (which -race turns on) and kills the process with
//
//	fatal error: checkptr: pointer arithmetic result points to invalid allocation
//
// as soon as the address lands inside a Go allocation, which is exactly what
// this produces. C reading it is fine because C is not instrumented; Go reading
// it back is not.
//
// The caller MUST defer the release function immediately, in the same statement
// that takes the pointer. Dropping it leaks the pin, which the runtime reports
// at the next collection as:
//
//	runtime.Pinner: found leaking pinned pointer; forgot to call Unpin()?
//
// That is loud and wrong-looking on purpose: the alternative failure mode is C
// reading freed memory, which shows up as rare corruption with no trace back
// to here.
func cstr(s string) (uintptr, func()) {
	if s == "" {
		return 0, func() {}
	}
	b := append([]byte(s), 0)
	pin := new(runtime.Pinner)
	pin.Pin(&b[0])
	// #nosec G103 -- b is non-empty (s != "" above) and &b[0] is pinned on the
	// previous line, so the address C receives cannot be collected or moved
	// until the returned release runs. One-way by construction: the doc comment
	// above forbids converting this uintptr back, which is what keeps checkptr
	// (and therefore -race) out of it.
	return uintptr(unsafe.Pointer(&b[0])), func() {
		if pin == nil {
			return
		}
		pin.Unpin()
		pin = nil
	}
}

// There is deliberately no inverse of cstr in this package. Every C entry point
// that returns a string is bound in abi.go with a Go `string` return, which
// purego converts from the char* itself, so a hand-rolled reader would have no
// production caller and would exist only as an unsafe helper waiting to be
// pointed at the wrong kind of address. Reach for purego's conversion instead;
// if a future symbol genuinely needs the raw char* (to tell NULL from ""), bind
// it as uintptr at that call site, where the ownership can be reasoned about.

// requireFamily gates an RPC on the family selected at load time. Returning
// Unimplemented rather than a nil dereference means a misconfigured model YAML
// produces a message a user can act on.
//
// Callers must already hold engineMu: Free writes n.fam under it, so an
// unlocked read here is a data race. Use withEngine rather than calling this
// directly.
func (n *NemoSpeech) requireFamily(want family) error {
	if n.fam != want {
		return status.Errorf(codes.Unimplemented,
			"nemo-speech-cpp: this model was loaded as %s, not %s", n.fam, want)
	}
	return nil
}

// withEngine runs fn holding engineMu, having first checked the family.
//
// Every RPC must go through this rather than calling requireFamily on its own.
// pkg/grpc/server.go takes the backend lock around each RPC but calls Free
// without it, so a teardown can land mid-request. Checking the family and then
// making the C calls that trust it under two separate acquisitions leaves a
// window in which Free destroys the handle, and the request goes on to use a
// zeroed one.
func (n *NemoSpeech) withEngine(want family, fn func() error) error {
	n.engineMu.Lock()
	defer n.engineMu.Unlock()

	if err := n.requireFamily(want); err != nil {
		return err
	}
	return fn()
}

func (n *NemoSpeech) Load(opts *pb.ModelOptions) error {
	modelFile := opts.GetModelFile()
	if modelFile == "" {
		return errors.New("nemo-speech-cpp: ModelFile is required")
	}

	// Free writes fam and the handles under engineMu and runs without the
	// backend lock that serialises the RPCs (pkg/grpc/server.go), so the
	// load-side writes to those same fields need the same protection: without
	// it this is the write-side half of the race withEngine closed on the read
	// side. n.opts is in here too, since the loaders read it.
	//
	// The loaders called below must NOT take engineMu themselves; sync.Mutex is
	// not reentrant and this is why.
	n.engineMu.Lock()
	defer n.engineMu.Unlock()

	n.opts = parseOptions(opts.GetOptions(), opts.GetModelPath())

	arch, err := ggufArchitecture(modelFile)
	if err != nil {
		return err
	}
	fam, err := familyFor(arch)
	if err != nil {
		return err
	}
	xlog.Info("nemo-speech-cpp: loading model", "arch", arch, "family", fam.String())

	// fam is committed only once the family-specific loader has succeeded.
	// requireFamily is the gate every RPC goes through, so a half-loaded model
	// that kept its family would route requests at a handle that was never
	// created.
	switch fam {
	case familyASR:
		err = n.loadASR(modelFile)
	case familyDiarization:
		err = n.loadDiarizer(modelFile)
	case familyTTS:
		if err = discoverTTSAssets(modelFile, &n.opts); err == nil {
			err = n.loadTTS(modelFile)
		}
	case familyNMT:
		err = n.loadNMT(modelFile)
	default:
		err = fmt.Errorf("nemo-speech-cpp: unhandled family for architecture %q", arch)
	}
	if err != nil {
		return err
	}
	n.fam = fam
	return nil
}

// Free destroys the runtime handle created at load time.
//
// base.SingleThread.Free is a no-op that derived backends are expected to
// override, and every family here owns C memory that only its own destroy
// entry point can release, so without this an unloaded model leaks a whole
// acoustic model. Clearing fam as well means an RPC that races the unload is
// refused by the gate rather than handed a dangling handle, but that only holds
// for callers that took engineMu, which today means callers that went through
// withEngine.
func (n *NemoSpeech) Free() error {
	n.engineMu.Lock()
	defer n.engineMu.Unlock()

	// Guarded on the handle, not on fam: a load that failed part way through
	// leaves fam unset, and the destroy functions are nil pointers until
	// openLibraries has bound them.
	// Each is tested independently rather than switched on: the one-handle
	// invariant is an invariant, and if it ever broke, a switch would silently
	// leak the others.
	if n.recognizer != 0 {
		ASRDestroy(n.recognizer)
		n.recognizer = 0
	}
	if n.diarizer != 0 {
		DiarDestroy(n.diarizer)
		n.diarizer = 0
	}
	if n.synth != 0 {
		TTSDestroy(n.synth)
		n.synth = 0
	}
	if n.nmt != 0 {
		NMTDestroy(n.nmt)
		n.nmt = 0
	}
	n.fam = familyUnknown
	return nil
}

// The loaders are one per family: loadASR in asr.go, loadDiarizer in diar.go,
// loadTTS in tts.go and loadNMT in nmt.go. Each populates its config structs
// from n.opts and stores the handle in the matching field.
//
// Locking protocol, in both directions:
//
//   - Every RPC must hold engineMu across its family check AND its C calls,
//     which means wrapping its body in withEngine. Free runs without the
//     backend lock (pkg/grpc/server.go:1019), so anything that checks the
//     family and then releases the lock before calling C can have the handle
//     destroyed underneath it. asr.go's AudioTranscription is the worked
//     example: even the audio decode sits inside the closure, because the
//     backend already serialises RPCs through base.SingleThread and so the
//     wider hold costs nothing.
//   - A loader must NOT take engineMu. Load holds it across the whole switch,
//     and sync.Mutex is not reentrant, so locking in a loader deadlocks.
//
// One consequence the streaming RPCs have to plan around: a stream whose body
// is wrapped in withEngine holds engineMu for the WHOLE stream, so Free blocks
// until the stream ends rather than tearing the handle out from under it. That
// is the behaviour we want (a half-closed stream over a destroyed recognizer
// has no good outcome), but it means an unload waits on a client that has
// stopped sending, so a streaming loop must have its own way out: honour the
// request context and stop on it, rather than blocking forever on the next
// chunk.
