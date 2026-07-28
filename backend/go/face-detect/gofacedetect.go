package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/xlog"
)

// purego-bound entry points from libfacedetect.so. Names match
// facedetect_capi.h exactly so a `nm libfacedetect.so | grep facedetect_capi`
// is enough to spot drift.
//
// The opaque ctx and the malloc'd char*/float* return values are declared as
// uintptr so we get the raw pointer back and can release it via the matching
// capi free function. purego's native string/[]float32 returns would copy and
// forget the original pointer, leaking the C-owned buffer on every call.
var (
	CppAbiVersion  func() int32
	CppLoad        func(ggufPath string) uintptr
	CppFree        func(ctx uintptr)
	CppLastError   func(ctx uintptr) string
	CppFreeString  func(s uintptr)
	CppFreeVec     func(v uintptr)
	CppEmbedPath   func(ctx uintptr, imagePath string, outVec, outDim unsafe.Pointer) int32
	CppEmbedRGB    func(ctx uintptr, rgb []byte, width, height int32, outVec, outDim unsafe.Pointer) int32
	CppDetectJSON  func(ctx uintptr, imagePath string) uintptr
	CppVerifyPaths func(ctx uintptr, a, b string, threshold float32, antiSpoof int32, outDistance, outVerified unsafe.Pointer) int32
	CppAnalyzeJSON func(ctx uintptr, imagePath string) uintptr
)

// FaceDetect implements the face-recognition (biometric) subset of the Backend
// gRPC service over libfacedetect.so. The C side keeps a single loaded model
// pack plus a per-ctx last-error buffer and is not reentrant, so
// base.SingleThread serializes every call.
type FaceDetect struct {
	base.SingleThread
	opts   loadOptions
	ctxPtr uintptr
}

func (f *FaceDetect) Load(opts *pb.ModelOptions) error {
	model := opts.ModelFile
	if model == "" {
		model = opts.ModelPath
	}
	if !filepath.IsAbs(model) && opts.ModelPath != "" {
		model = filepath.Join(opts.ModelPath, model)
	}
	if model == "" {
		return errors.New("face-detect: ModelFile is required")
	}

	f.opts = parseOptions(opts.Options)
	if f.opts.modelName == "" {
		f.opts.modelName = filepath.Base(model)
	}

	// Propagate LocalAI's per-model thread budget to the engine. LocalAI spawns
	// one backend process per model and serves requests concurrently, so the
	// engine's own min(hardware_concurrency, 8) default can oversubscribe cores.
	// FACEDETECT_THREADS is read by the engine at backend construction, so it
	// must be set before the capi load. A non-positive Threads means "unset":
	// leave the env alone so the engine keeps its sane default.
	threads := opts.Threads
	if threads > 0 {
		if err := os.Setenv("FACEDETECT_THREADS", strconv.Itoa(int(threads))); err != nil {
			return fmt.Errorf("face-detect: set FACEDETECT_THREADS: %w", err)
		}
		xlog.Info("face-detect: applying LocalAI thread budget", "threads", threads)
	}

	xlog.Info("face-detect: loading model", "model", model,
		"verify_threshold", f.opts.verifyThreshold, "abi", CppAbiVersion())

	ctx := CppLoad(model)
	if ctx == 0 {
		// The last-error buffer lives on the ctx that was never returned, so
		// surface the path the operator tried to load instead.
		return fmt.Errorf("face-detect: facedetect_capi_load failed for %q", model)
	}
	f.ctxPtr = ctx
	return nil
}

// Embeddings returns the L2-normalized ArcFace embedding of the primary face in
// the supplied image. Mirroring the Python face backend, the image is read from
// Images[0] as a base64 payload; materializeImage decodes it to a temp file so
// the path-based C-API can run its own decode (cv2.imread parity). The gRPC
// server wraps the returned slice in an EmbeddingResult.
func (f *FaceDetect) Embeddings(req *pb.PredictOptions) ([]float32, error) {
	if f.ctxPtr == 0 {
		return nil, errors.New("face-detect: model not loaded")
	}
	if len(req.Images) == 0 || req.Images[0] == "" {
		return nil, errors.New("face-detect: Embedding requires Images[0] to be a base64 image")
	}

	path, cleanup, err := materializeImage(req.Images[0])
	if err != nil {
		return nil, err
	}
	defer cleanup()

	return f.embedPath(path)
}

func (f *FaceDetect) embedPath(path string) ([]float32, error) {
	var vec uintptr
	var dim int32
	rc := CppEmbedPath(f.ctxPtr, path, unsafe.Pointer(&vec), unsafe.Pointer(&dim))
	if rc != 0 || vec == 0 || dim <= 0 {
		return nil, f.lastErr("embed", path)
	}
	defer CppFreeVec(vec)
	// Copy out of the C-owned malloc'd buffer before freeing it. The
	// uintptr->Pointer conversion trips vet's unsafeptr check, which can't tell
	// a C heap pointer from Go-managed memory; safe here, the GC neither tracks
	// nor moves this buffer and we copy immediately.
	src := unsafe.Slice((*float32)(unsafe.Pointer(vec)), int(dim)) //nolint:govet // C-owned malloc'd vector, copied out before free
	out := make([]float32, int(dim))
	copy(out, src)
	return out, nil
}

// Detect runs SCRFD over the image and returns one Detection per face. The
// C-API emits a box as [x1,y1,x2,y2] in pixels; the proto carries x/y plus
// width/height, so the corners are converted. The 5 facial landmarks the engine
// also returns are dropped: the Detection message has no field for them.
func (f *FaceDetect) Detect(req *pb.DetectOptions) (pb.DetectResponse, error) {
	if f.ctxPtr == 0 {
		return pb.DetectResponse{}, errors.New("face-detect: model not loaded")
	}
	if req.Src == "" {
		return pb.DetectResponse{}, errors.New("face-detect: src image is required")
	}

	path, cleanup, err := materializeImage(req.Src)
	if err != nil {
		return pb.DetectResponse{}, err
	}
	defer cleanup()

	faces, err := f.detectFaces(path)
	if err != nil {
		return pb.DetectResponse{}, err
	}

	dets := make([]*pb.Detection, 0, len(faces))
	for _, fc := range faces {
		if req.Threshold > 0 && fc.Score < req.Threshold {
			continue
		}
		x, y, w, h := fc.xywh()
		dets = append(dets, &pb.Detection{
			X:          x,
			Y:          y,
			Width:      w,
			Height:     h,
			Confidence: fc.Score,
			ClassName:  "face",
		})
	}
	return pb.DetectResponse{Detections: dets}, nil
}

// FaceVerify embeds the primary face in each image and reports whether they are
// the same identity by cosine distance against a threshold. A request threshold
// <= 0 falls back to the model-configured default (verify_threshold option,
// 0.35 if unset). When anti_spoofing is set, the C-API applies a MiniFASNet
// veto internally (verified forced false on a spoof); the per-image liveness
// scores are not exposed by the verify entry point, so img*_is_real /
// img*_antispoof_score stay at their zero values.
func (f *FaceDetect) FaceVerify(req *pb.FaceVerifyRequest) (pb.FaceVerifyResponse, error) {
	if f.ctxPtr == 0 {
		return pb.FaceVerifyResponse{}, errors.New("face-detect: model not loaded")
	}
	if req.Img1 == "" || req.Img2 == "" {
		return pb.FaceVerifyResponse{}, errors.New("face-detect: img1 and img2 are required")
	}

	path1, cleanup1, err := materializeImage(req.Img1)
	if err != nil {
		return pb.FaceVerifyResponse{}, err
	}
	defer cleanup1()
	path2, cleanup2, err := materializeImage(req.Img2)
	if err != nil {
		return pb.FaceVerifyResponse{}, err
	}
	defer cleanup2()

	threshold := req.Threshold
	if threshold <= 0 {
		threshold = f.opts.verifyThreshold
	}

	antiSpoof := int32(0)
	if req.AntiSpoofing {
		antiSpoof = 1
	}

	started := time.Now()
	var distance float32
	var verified int32
	rc := CppVerifyPaths(f.ctxPtr, path1, path2, threshold, antiSpoof,
		unsafe.Pointer(&distance), unsafe.Pointer(&verified))
	if rc != 0 {
		return pb.FaceVerifyResponse{}, f.lastErr("verify", req.Img1[:min(8, len(req.Img1))]+"...")
	}
	elapsedMs := float32(time.Since(started).Seconds() * 1000.0)

	// Confidence decays linearly from 100 at distance 0 to 0 at the threshold,
	// matching the Python face backend's reporting.
	confidence := float32(0)
	if threshold > 0 {
		confidence = float32(math.Max(0, math.Min(100, (1.0-float64(distance)/float64(threshold))*100.0)))
	}

	return pb.FaceVerifyResponse{
		Verified:         verified != 0,
		Distance:         distance,
		Threshold:        threshold,
		Confidence:       confidence,
		Model:            f.opts.modelName,
		Img1Area:         f.bestArea(path1),
		Img2Area:         f.bestArea(path2),
		ProcessingTimeMs: elapsedMs,
	}, nil
}

// FaceAnalyze runs the genderage head on every detected face. The C-API returns
// "M"/"F" gender labels and a rounded age; the labels are normalized to the
// "Man"/"Woman" values the proto documents.
func (f *FaceDetect) FaceAnalyze(req *pb.FaceAnalyzeRequest) (pb.FaceAnalyzeResponse, error) {
	if f.ctxPtr == 0 {
		return pb.FaceAnalyzeResponse{}, errors.New("face-detect: model not loaded")
	}
	if req.Img == "" {
		return pb.FaceAnalyzeResponse{}, errors.New("face-detect: img is required")
	}

	path, cleanup, err := materializeImage(req.Img)
	if err != nil {
		return pb.FaceAnalyzeResponse{}, err
	}
	defer cleanup()

	ptr := CppAnalyzeJSON(f.ctxPtr, path)
	if ptr == 0 {
		return pb.FaceAnalyzeResponse{}, f.lastErr("analyze", path)
	}
	defer CppFreeString(ptr)

	faces, err := parseAnalyzeJSON(goStringFromCPtr(ptr))
	if err != nil {
		return pb.FaceAnalyzeResponse{}, fmt.Errorf("face-detect: analyze JSON: %w", err)
	}
	return pb.FaceAnalyzeResponse{Faces: faces}, nil
}

// faceBox is one entry of the detect/analyze JSON documents the engine emits.
type faceBox struct {
	Score  float32   `json:"score"`
	Box    []float32 `json:"box"`
	Age    float32   `json:"age"`
	Gender string    `json:"gender"`
}

// xywh converts the engine's [x1,y1,x2,y2] box into the x/y/width/height the
// proto carries. A short or missing box yields zeros.
func (b faceBox) xywh() (x, y, w, h float32) {
	if len(b.Box) < 4 {
		return 0, 0, 0, 0
	}
	return b.Box[0], b.Box[1], b.Box[2] - b.Box[0], b.Box[3] - b.Box[1]
}

type facesJSON struct {
	Faces []faceBox `json:"faces"`
}

func (f *FaceDetect) detectFaces(path string) ([]faceBox, error) {
	ptr := CppDetectJSON(f.ctxPtr, path)
	if ptr == 0 {
		return nil, f.lastErr("detect", path)
	}
	defer CppFreeString(ptr)

	var doc facesJSON
	if err := json.Unmarshal([]byte(goStringFromCPtr(ptr)), &doc); err != nil {
		return nil, fmt.Errorf("face-detect: detect JSON: %w", err)
	}
	return doc.Faces, nil
}

// bestArea returns the FacialArea of the highest-scoring face in an image, or an
// empty area when detection fails or finds nothing. Best-effort: verify already
// succeeded, so a missing region must not turn a valid match into an error.
func (f *FaceDetect) bestArea(path string) *pb.FacialArea {
	faces, err := f.detectFaces(path)
	if err != nil || len(faces) == 0 {
		return &pb.FacialArea{}
	}
	best := faces[0]
	for _, fc := range faces[1:] {
		if fc.Score > best.Score {
			best = fc
		}
	}
	x, y, w, h := best.xywh()
	return &pb.FacialArea{X: x, Y: y, W: w, H: h}
}

// parseAnalyzeJSON maps the engine's analyze document onto FaceAnalysis entries.
// The engine reports gender as "M"/"F"; both the dominant label and the score
// map are filled with the "Man"/"Woman" form the proto documents.
func parseAnalyzeJSON(doc string) ([]*pb.FaceAnalysis, error) {
	var parsed facesJSON
	if err := json.Unmarshal([]byte(doc), &parsed); err != nil {
		return nil, err
	}

	out := make([]*pb.FaceAnalysis, 0, len(parsed.Faces))
	for _, fc := range parsed.Faces {
		x, y, w, h := fc.xywh()
		fa := &pb.FaceAnalysis{
			Region:         &pb.FacialArea{X: x, Y: y, W: w, H: h},
			FaceConfidence: fc.Score,
			Age:            fc.Age,
		}
		if label := normalizeGender(fc.Gender); label != "" {
			fa.DominantGender = label
			fa.Gender = map[string]float32{label: 1.0}
		}
		out = append(out, fa)
	}
	return out, nil
}

// normalizeGender maps the engine's "M"/"F" code to the "Man"/"Woman" labels the
// proto documents. Unknown codes pass through unchanged.
func normalizeGender(g string) string {
	switch strings.ToUpper(strings.TrimSpace(g)) {
	case "M":
		return "Man"
	case "F":
		return "Woman"
	case "":
		return ""
	default:
		return g
	}
}

// materializeImage decodes a base64 image payload into a temp file and returns
// its path plus a cleanup func. As a convenience for callers that already pass a
// filesystem path (e.g. a test fixture), an existing path is used as-is with a
// no-op cleanup. data: URI prefixes are stripped before decoding.
func materializeImage(src string) (path string, cleanup func(), err error) {
	noop := func() {}
	if src == "" {
		return "", noop, errors.New("face-detect: empty image input")
	}
	if _, statErr := os.Stat(src); statErr == nil {
		return src, noop, nil
	}

	payload := src
	if i := strings.Index(payload, ","); strings.HasPrefix(payload, "data:") && i >= 0 {
		payload = payload[i+1:]
	}
	data, decErr := base64.StdEncoding.DecodeString(strings.TrimSpace(payload))
	if decErr != nil || len(data) == 0 {
		return "", noop, errors.New("face-detect: image is neither an existing path nor valid base64")
	}

	tmp, createErr := os.CreateTemp("", "face-detect-*.img")
	if createErr != nil {
		return "", noop, fmt.Errorf("face-detect: create temp image: %w", createErr)
	}
	cleanup = func() { _ = os.Remove(tmp.Name()) }
	if _, wErr := tmp.Write(data); wErr != nil {
		_ = tmp.Close()
		cleanup()
		return "", noop, fmt.Errorf("face-detect: write temp image: %w", wErr)
	}
	if cErr := tmp.Close(); cErr != nil {
		cleanup()
		return "", noop, fmt.Errorf("face-detect: close temp image: %w", cErr)
	}
	return tmp.Name(), cleanup, nil
}

// lastErr wraps the C-API's per-ctx last-error buffer into a Go error.
func (f *FaceDetect) lastErr(op, subject string) error {
	msg := strings.TrimSpace(CppLastError(f.ctxPtr))
	if msg == "" {
		msg = "no error detail"
	}
	return fmt.Errorf("face-detect: %s failed for %q: %s", op, subject, msg)
}

// goStringFromCPtr copies a NUL-terminated C string into Go memory. cptr is a
// malloc'd buffer the caller owns; release it via CppFreeString after the copy.
//
// The uintptr->Pointer conversion trips vet's unsafeptr check, which can't tell
// a C heap pointer from Go-managed memory. Safe here: the GC neither tracks nor
// moves the buffer and we dereference it immediately to copy the bytes out.
func goStringFromCPtr(cptr uintptr) string {
	if cptr == 0 {
		return ""
	}
	p := unsafe.Pointer(cptr) //nolint:govet // C-owned malloc'd buffer, not Go-GC memory (see doc above)
	n := 0
	for *(*byte)(unsafe.Add(p, n)) != 0 {
		n++
	}
	return string(unsafe.Slice((*byte)(p), n))
}
