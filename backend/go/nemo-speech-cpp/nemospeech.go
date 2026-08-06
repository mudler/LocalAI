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

	// engineMu serializes calls into the C runtime for this model.
	engineMu sync.Mutex

	recognizer uintptr
	diarizer   uintptr
	synth      uintptr
	translator uintptr
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
// The caller MUST defer the release function immediately, in the same statement
// that takes the pointer. Dropping it leaks the pin, which the runtime reports
// as a "found leaked Pinner" panic at the next collection. That is loud and
// wrong-looking on purpose: the alternative failure mode is C reading freed
// memory, which shows up as rare corruption with no trace back to here.
func cstr(s string) (uintptr, func()) {
	if s == "" {
		return 0, func() {}
	}
	b := append([]byte(s), 0)
	pin := new(runtime.Pinner)
	pin.Pin(&b[0])
	return uintptr(unsafe.Pointer(&b[0])), func() {
		if pin == nil {
			return
		}
		pin.Unpin()
		pin = nil
	}
}

// goString copies a NUL-terminated C string into Go memory.
func goString(p uintptr) string {
	if p == 0 {
		return ""
	}
	var n int
	for {
		if *(*byte)(unsafe.Pointer(p + uintptr(n))) == 0 { //nolint:govet // #nosec G103 -- NUL-terminated string owned by the C runtime or pinned by cstr, never a bare Go allocation
			break
		}
		n++
	}
	return string(unsafe.Slice((*byte)(unsafe.Pointer(p)), n)) //nolint:govet // #nosec G103 -- same pointer, copied into Go memory by the string conversion
}

// requireFamily gates an RPC on the family selected at load time. Returning
// Unimplemented rather than a nil dereference means a misconfigured model YAML
// produces a message a user can act on.
func (n *NemoSpeech) requireFamily(want family) error {
	if n.fam != want {
		return status.Errorf(codes.Unimplemented,
			"nemo-speech-cpp: this model was loaded as %s, not %s", n.fam, want)
	}
	return nil
}

func (n *NemoSpeech) Load(opts *pb.ModelOptions) error {
	modelFile := opts.GetModelFile()
	if modelFile == "" {
		return errors.New("nemo-speech-cpp: ModelFile is required")
	}
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
// refused by requireFamily rather than handed a dangling handle.
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
	if n.translator != 0 {
		NMTDestroy(n.translator)
		n.translator = 0
	}
	n.fam = familyUnknown
	return nil
}

// The four loaders below create the runtime handle for their family. Task 6
// implements loadASR, Task 7 loadDiarizer, Task 8 loadTTS and Task 9 loadNMT;
// each populates its config structs from n.opts and stores the handle in the
// matching field.
//
// They must not take engineMu: Load runs before the model is serving, and the
// lock is not reentrant.

func (n *NemoSpeech) loadASR(modelFile string) error { return nil }

func (n *NemoSpeech) loadDiarizer(modelFile string) error { return nil }

func (n *NemoSpeech) loadTTS(modelFile string) error { return nil }

func (n *NemoSpeech) loadNMT(modelFile string) error { return nil }
