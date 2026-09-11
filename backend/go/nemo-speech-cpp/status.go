package main

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The status values every C entry point in this backend returns.
//
// There is no single C enum to mirror. asr.h:43-49, tts.h:38-44 and nmt.h:40-45
// each declare their own, and diar.h has none of its own at all: it includes
// asr.h and types every diarization function as nemo_speech_asr_status
// (diar.h:18). The names the three surfaces share carry the same numbers:
//
//	value  asr.h                              tts.h                              nmt.h
//	0      NEMO_SPEECH_ASR_OK                 NEMO_SPEECH_TTS_OK                 NEMO_SPEECH_NMT_OK
//	1      NEMO_SPEECH_ASR_ERROR_INVALID_...  NEMO_SPEECH_TTS_ERROR_INVALID_...  NEMO_SPEECH_NMT_ERROR_INVALID_...
//	2      NEMO_SPEECH_ASR_ERROR_OUT_OF_MEM.  NEMO_SPEECH_TTS_ERROR_OUT_OF_MEM.  NEMO_SPEECH_NMT_ERROR_OUT_OF_MEM.
//	3      NEMO_SPEECH_ASR_ERROR_RUNTIME      NEMO_SPEECH_TTS_ERROR_RUNTIME      NEMO_SPEECH_NMT_ERROR_RUNTIME
//	4      NEMO_SPEECH_ASR_ERROR_CANCELLED    NEMO_SPEECH_TTS_ERROR_CANCELLED    (not declared)
//
// The one divergence is 4, and it is an absence rather than a disagreement. ASR
// and TTS both drive a consumer callback that can ask for the work to stop, and
// cancellation is what they report when it does; nemo_speech_nmt_translate takes
// no callback and returns only when the decode has finished, so the NMT surface
// has no cancellation to name. That is why one table can serve all three: 4 is
// not some other NMT status that would be mislabelled, it is a value the NMT
// surface never produces.
//
// Recheck this table after an upstream pin bump. A status added to one header
// and not the others is exactly the shape of change that would break the single
// mapping, and nothing in the build or the linker can see it: purego binds by
// name, and the return value is a bare int32 on the Go side.
const (
	statusOK              int32 = 0
	statusInvalidArgument int32 = 1
	statusOutOfMemory     int32 = 2
	statusRuntime         int32 = 3
	statusCancelled       int32 = 4
)

// statusCode maps a C status onto the gRPC code the caller should be told.
//
// What the mapping is really carrying is whose mistake the failure was.
// INVALID_ARGUMENT is what every guard in src/{asr,tts,nmt}/c_api.cpp returns
// for a std::invalid_argument from the runtime, and the things that throw it are
// requests: an unknown voice_name (src/tts/synthesizer.cpp), an unsupported
// language pair (src/nmt/translator.cpp), an out-of-range sample rate. Reporting
// those as Internal turns a 400 into a 500 and sends the user hunting for a
// broken model or a broken backend instead of fixing the request.
//
// OUT_OF_MEMORY is a resource limit rather than a defect, which is what
// ResourceExhausted means, and it is the one failure a client can sensibly
// retry later or retry smaller. CANCELLED is the consumer having stopped
// listening, which is not a failure of this backend at all: the streaming sinks
// return false once their client is gone (see ttsDeliverPCM), and the runtime
// turns that into status 4.
//
// RUNTIME, and anything a future pin adds that this table has not been taught,
// stay Internal. An unrecognised status is precisely the case where the backend
// does not know whose fault it was, and Internal is the honest answer.
func statusCode(st int32) codes.Code {
	switch st {
	case statusOK:
		return codes.OK
	case statusInvalidArgument:
		return codes.InvalidArgument
	case statusOutOfMemory:
		return codes.ResourceExhausted
	case statusCancelled:
		return codes.Canceled
	case statusRuntime:
		// Named rather than folded into the default so this switch reads as the
		// whole enum. A status the table has never heard of is a different thing
		// from a runtime error even though both answer Internal, and a reader
		// checking the mapping against the headers should not have to work out
		// which arm RUNTIME lands in.
		return codes.Internal
	default:
		return codes.Internal
	}
}

// statusErrorf builds the gRPC error for a failed C call.
//
// Every C call site in this backend goes through this rather than through
// status.Errorf directly, and that is the whole point of it existing: the
// mapping used to be written out at exactly one of sixteen call sites, so the
// same backend answered an unsupported language pair with InvalidArgument and an
// unknown TTS voice, which is the same class of caller mistake against the same
// process, with Internal.
//
// The OK guard is not defensive noise. status.Errorf(codes.OK, ...) returns a
// nil error, so a call site that built its error without first checking the
// status would report a hard C failure as a successful request with no
// diagnostic anywhere. Returning Internal instead keeps that mistake loud.
func statusErrorf(st int32, format string, args ...any) error {
	if st == statusOK {
		return status.Errorf(codes.Internal, format, args...)
	}
	return status.Errorf(statusCode(st), format, args...)
}
