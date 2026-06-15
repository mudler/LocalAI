package main

// godepthanythingcpp.go - gRPC handlers (Load, Predict, GenerateImage) for the
// depth-anything-cpp backend, wrapping the Depth Anything 3 ggml C-API
// (libdepthanythingcpp-<variant>.so) via purego.
//
// Embeds base.SingleThread to default the unimplemented RPCs to "not supported"
// and to serialize calls — the C side shares a ggml graph allocator and is NOT
// reentrant, so all inference must run one-at-a-time.
//
// Depth has no native OpenAI endpoint, so the model is exposed two ways:
//
//   - GenerateImage(src, dst): run depth on the src image and write a
//     min-max-normalised grayscale depth PNG to dst.
//   - Predict(images[0]): run depth+pose and return a JSON blob with the depth
//     dimensions, depth stats and the camera extrinsics (3x4) / intrinsics (3x3).

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"unsafe"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// C-API function pointers, registered in main.go via purego. The da_capi_*
// symbols live inside libdepthanything (src/da_capi.cpp) and are re-exported by
// the DA_SHARED build.
var (
	// da_capi_load(const char* gguf_path, int n_threads) -> da_ctx* (0 = fail)
	CapiLoad func(gguf string, nThreads int32) uintptr
	// da_capi_free(da_ctx* ctx) — safe on a 0 handle.
	CapiFree func(handle uintptr)
	// da_capi_last_error(da_ctx* ctx) -> const char* (owned by ctx, "" if none).
	// purego marshals the returned C string into a Go string (a copy), so we
	// never free it.
	CapiLastError func(handle uintptr) string
	// da_capi_depth_path(ctx, image_path, out_h*, out_w*) -> float* depth map
	// (row-major H*W); nil on error. Caller frees via da_capi_free_floats.
	CapiDepthPath func(handle uintptr, imagePath string, outH *int32, outW *int32) *float32
	// da_capi_free_floats(float* p)
	CapiFreeFloats func(p *float32)
	// da_capi_pose_path(ctx, image_path, out_ext[12], out_intr[9]) -> 0 ok, -1 err
	CapiPosePath func(handle uintptr, imagePath string, outExt *float32, outIntr *float32) int32
	// da_capi_depth_dense(ctx, image_path, out_h*, out_w*, out_depth**, out_conf**,
	//   out_sky**, out_ext[12], out_intr[9], out_is_metric*) -> 0 ok, -1 err.
	// Each non-NULL out_depth/out_conf/out_sky receives a malloc'd float[H*W] (free
	// via da_capi_free_floats); buffers the model doesn't produce are set NULL.
	CapiDepthDense func(handle uintptr, imagePath string,
		outH, outW *int32,
		outDepth, outConf, outSky **float32,
		outExt, outIntr *float32,
		outIsMetric *int32) int32
	// da_capi_points(ctx, image_path, conf_thresh, out_n*, out_xyz**, out_rgb**) ->
	//   0 ok, -1 err. *out_xyz = malloc'd float[3*N] (free via da_capi_free_floats),
	//   *out_rgb = malloc'd uint8[3*N] (free via da_capi_free_bytes).
	CapiPoints func(handle uintptr, imagePath string, confThresh float32,
		outN *int32, outXyz **float32, outRgb **byte) int32
	// da_capi_free_bytes(unsigned char* p)
	CapiFreeBytes func(p *byte)
	// da_capi_export_glb(ctx, image_path, out_glb) -> 0 ok, -1 err
	CapiExportGlb func(handle uintptr, imagePath string, outGlb string) int32
	// da_capi_export_colmap(ctx, image_path, out_dir, binary) -> 0 ok, -1 err
	CapiExportColmap func(handle uintptr, imagePath string, outDir string, binary int32) int32
)

type DepthAnythingCpp struct {
	base.SingleThread
	handle uintptr
}

// Load loads the GGUF model at opts.ModelFile (joined with opts.ModelPath if
// relative) and stores the da_ctx handle for later inference calls.
func (r *DepthAnythingCpp) Load(opts *pb.ModelOptions) error {
	modelFile := opts.ModelFile
	if modelFile == "" {
		modelFile = opts.Model
	}
	if modelFile == "" {
		return fmt.Errorf("depth-anything-cpp: ModelFile is empty")
	}

	var modelPath string
	if filepath.IsAbs(modelFile) {
		modelPath = modelFile
	} else {
		modelPath = filepath.Join(opts.ModelPath, modelFile)
	}

	if _, err := os.Stat(modelPath); err != nil {
		return fmt.Errorf("depth-anything-cpp: model file not found: %s: %w", modelPath, err)
	}

	threads := opts.Threads
	if threads <= 0 {
		threads = 4
	}

	// Release previous model if any (re-Load).
	if r.handle != 0 {
		CapiFree(r.handle)
		r.handle = 0
	}

	h := CapiLoad(modelPath, threads)
	if h == 0 {
		// da_capi_last_error needs a ctx; on a failed load we have none (it
		// returns "" for a null ctx), so the text is best-effort.
		if msg := CapiLastError(0); msg != "" {
			return fmt.Errorf("depth-anything-cpp: da_capi_load failed for %s: %s", modelPath, msg)
		}
		return fmt.Errorf("depth-anything-cpp: da_capi_load failed for %s", modelPath)
	}
	r.handle = h
	return nil
}

// depthResult is the JSON payload returned by Predict.
type depthResult struct {
	DepthW     int         `json:"depth_w"`
	DepthH     int         `json:"depth_h"`
	DepthMin   float32     `json:"depth_min"`
	DepthMax   float32     `json:"depth_max"`
	Extrinsics [12]float32 `json:"extrinsics"` // 3x4 row-major
	Intrinsics [9]float32  `json:"intrinsics"` // 3x3 row-major
}

// Predict runs depth+pose on the first supplied image and returns depth
// statistics + camera pose as a JSON string. LocalAI wraps the string into the
// Reply.Message of the gRPC response. The image in Images[0] may be a
// filesystem path or a base64-encoded payload.
func (r *DepthAnythingCpp) Predict(opts *pb.PredictOptions) (string, error) {
	imgs := opts.GetImages()
	if len(imgs) == 0 {
		return "", fmt.Errorf("depth-anything-cpp: Predict requires an image in Images[]")
	}

	imgPath, cleanup, err := materializeImage(imgs[0])
	if err != nil {
		return "", fmt.Errorf("depth-anything-cpp: %w", err)
	}
	defer cleanup()

	depth, h, w, ext, intr, err := r.runDepthPose(imgPath)
	if err != nil {
		return "", err
	}

	dmin, dmax := minMax(depth)
	payload, err := json.Marshal(depthResult{
		DepthW: w, DepthH: h,
		DepthMin: dmin, DepthMax: dmax,
		Extrinsics: ext, Intrinsics: intr,
	})
	if err != nil {
		return "", fmt.Errorf("depth-anything-cpp: marshal: %w", err)
	}
	return string(payload), nil
}

// GenerateImage runs depth on req.Src and writes a normalised grayscale depth
// PNG to req.Dst.
func (r *DepthAnythingCpp) GenerateImage(req *pb.GenerateImageRequest) error {
	if req.GetSrc() == "" {
		return fmt.Errorf("depth-anything-cpp: GenerateImage requires src")
	}
	if req.GetDst() == "" {
		return fmt.Errorf("depth-anything-cpp: GenerateImage requires dst")
	}

	imgPath, cleanup, err := materializeImage(req.GetSrc())
	if err != nil {
		return fmt.Errorf("depth-anything-cpp: %w", err)
	}
	defer cleanup()

	depth, h, w, _, _, err := r.runDepthPose(imgPath)
	if err != nil {
		return err
	}
	return writeDepthPNG(req.GetDst(), depth, h, w)
}

// Depth is the typed Depth RPC. It runs the Depth Anything 3 pipeline on the
// request's src image and fills a DepthResponse honoring the include_* flags and
// exports: per-pixel metric depth + confidence (DualDPT) or depth + sky (mono),
// camera extrinsics/intrinsics, an optional back-projected 3D point cloud and
// glb/COLMAP exports. The src may be a filesystem path or a base64 payload.
func (r *DepthAnythingCpp) Depth(in *pb.DepthRequest) (pb.DepthResponse, error) {
	// Accumulate into locals and return a single composite literal at the end:
	// returning a named pb.DepthResponse value would copy its embedded mutex
	// (go vet copylocks).
	if r.handle == 0 {
		return pb.DepthResponse{}, fmt.Errorf("depth-anything-cpp: model not loaded")
	}
	if in.GetSrc() == "" {
		return pb.DepthResponse{}, fmt.Errorf("depth-anything-cpp: Depth requires src")
	}

	imgPath, cleanup, err := materializeImage(in.GetSrc())
	if err != nil {
		return pb.DepthResponse{}, fmt.Errorf("depth-anything-cpp: %w", err)
	}
	defer cleanup()

	// Dense per-pixel output + pose. Pass buffer pointers only for the
	// requested maps so the native side can skip unrequested work; ext/intr
	// must always point at 12/9 floats per the C ABI.
	var (
		h, w, isMetric      int32
		depthPtr, confPtr   *float32
		skyPtr              *float32
		ext                 [12]float32
		intr                [9]float32
		pDepth, pConf, pSky **float32
	)
	if in.GetIncludeDepth() {
		pDepth = &depthPtr
	}
	if in.GetIncludeConfidence() {
		pConf = &confPtr
	}
	if in.GetIncludeSky() {
		pSky = &skyPtr
	}

	rc := CapiDepthDense(r.handle, imgPath, &h, &w, pDepth, pConf, pSky, &ext[0], &intr[0], &isMetric)
	if rc != 0 {
		return pb.DepthResponse{}, fmt.Errorf("depth-anything-cpp: da_capi_depth_dense failed (rc=%d): %s", rc, r.lastError())
	}

	n := int(h) * int(w)
	var (
		depth, conf, sky      []float32
		extrinsics, intrinsic []float32
		numPoints             int32
		points                []float32
		pointColors           []byte
		exportPaths           []string
	)

	if depthPtr != nil {
		depth = copyFloats(depthPtr, n)
		CapiFreeFloats(depthPtr)
	}
	if confPtr != nil {
		conf = copyFloats(confPtr, n)
		CapiFreeFloats(confPtr)
	}
	if skyPtr != nil {
		sky = copyFloats(skyPtr, n)
		CapiFreeFloats(skyPtr)
	}
	if in.GetIncludePose() {
		extrinsics = append([]float32(nil), ext[:]...)
		intrinsic = append([]float32(nil), intr[:]...)
	}

	// 3D point cloud (DualDPT / pose-capable models only).
	if in.GetIncludePoints() {
		var (
			np     int32
			xyzPtr *float32
			rgbPtr *byte
		)
		if rc := CapiPoints(r.handle, imgPath, in.GetPointsConfThresh(), &np, &xyzPtr, &rgbPtr); rc != 0 {
			return pb.DepthResponse{}, fmt.Errorf("depth-anything-cpp: da_capi_points failed (rc=%d): %s", rc, r.lastError())
		}
		numPoints = np
		if xyzPtr != nil {
			points = copyFloats(xyzPtr, int(np)*3)
			CapiFreeFloats(xyzPtr)
		}
		if rgbPtr != nil {
			pointColors = copyBytes(rgbPtr, int(np)*3)
			CapiFreeBytes(rgbPtr)
		}
	}

	// Exports (glb / colmap). They are written under in.Dst (a directory); a
	// temp dir is used when Dst is empty.
	if len(in.GetExports()) > 0 {
		exportPaths, err = r.runExports(imgPath, in.GetDst(), in.GetExports())
		if err != nil {
			return pb.DepthResponse{}, err
		}
	}

	return pb.DepthResponse{
		Width:       w,
		Height:      h,
		Depth:       depth,
		Confidence:  conf,
		Sky:         sky,
		Extrinsics:  extrinsics,
		Intrinsics:  intrinsic,
		NumPoints:   numPoints,
		Points:      points,
		PointColors: pointColors,
		ExportPaths: exportPaths,
		IsMetric:    isMetric != 0,
	}, nil
}

// runExports writes the requested exports for imgPath into dstDir and returns
// the written paths. Supported exports: "glb", "colmap".
func (r *DepthAnythingCpp) runExports(imgPath, dstDir string, exports []string) ([]string, error) {
	if dstDir == "" {
		tmp, err := os.MkdirTemp("", "depth-anything-export-*")
		if err != nil {
			return nil, fmt.Errorf("depth-anything-cpp: mkdir export dir: %w", err)
		}
		dstDir = tmp
	} else if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return nil, fmt.Errorf("depth-anything-cpp: mkdir %s: %w", dstDir, err)
	}

	var paths []string
	for _, exp := range exports {
		switch exp {
		case "glb":
			out := filepath.Join(dstDir, "pointcloud.glb")
			if rc := CapiExportGlb(r.handle, imgPath, out); rc != 0 {
				return nil, fmt.Errorf("depth-anything-cpp: da_capi_export_glb failed (rc=%d): %s", rc, r.lastError())
			}
			paths = append(paths, out)
		case "colmap":
			out := filepath.Join(dstDir, "colmap")
			if err := os.MkdirAll(out, 0o755); err != nil {
				return nil, fmt.Errorf("depth-anything-cpp: mkdir %s: %w", out, err)
			}
			if rc := CapiExportColmap(r.handle, imgPath, out, 1); rc != 0 {
				return nil, fmt.Errorf("depth-anything-cpp: da_capi_export_colmap failed (rc=%d): %s", rc, r.lastError())
			}
			paths = append(paths, out)
		default:
			return nil, fmt.Errorf("depth-anything-cpp: unknown export %q (want glb|colmap)", exp)
		}
	}
	return paths, nil
}

// copyFloats copies n float32 values from a C heap pointer into a fresh Go
// slice so the C buffer can be freed afterwards.
func copyFloats(p *float32, n int) []float32 {
	if p == nil || n <= 0 {
		return nil
	}
	src := unsafe.Slice(p, n)
	out := make([]float32, n)
	copy(out, src)
	return out
}

// copyBytes copies n bytes from a C heap pointer into a fresh Go slice.
func copyBytes(p *byte, n int) []byte {
	if p == nil || n <= 0 {
		return nil
	}
	src := unsafe.Slice(p, n)
	out := make([]byte, n)
	copy(out, src)
	return out
}

// runDepthPose runs depth estimation then pose recovery on an image file. It
// returns the row-major depth map (length h*w), its dimensions, the 3x4
// extrinsics (12 floats) and 3x3 intrinsics (9 floats).
func (r *DepthAnythingCpp) runDepthPose(imagePath string) (depth []float32, h, w int, ext [12]float32, intr [9]float32, err error) {
	if r.handle == 0 {
		err = fmt.Errorf("depth-anything-cpp: model not loaded")
		return
	}

	var ch, cw int32
	ptr := CapiDepthPath(r.handle, imagePath, &ch, &cw)
	if ptr == nil {
		err = fmt.Errorf("depth-anything-cpp: da_capi_depth_path failed: %s", r.lastError())
		return
	}
	h, w = int(ch), int(cw)
	n := h * w
	if n > 0 {
		src := unsafe.Slice(ptr, n)
		depth = make([]float32, n)
		copy(depth, src)
	}
	CapiFreeFloats(ptr)

	if rc := CapiPosePath(r.handle, imagePath, &ext[0], &intr[0]); rc != 0 {
		err = fmt.Errorf("depth-anything-cpp: da_capi_pose_path failed (rc=%d): %s", rc, r.lastError())
		return
	}
	return
}

// lastError returns the context's last error string, or "" if none.
func (r *DepthAnythingCpp) lastError() string {
	if CapiLastError == nil || r.handle == 0 {
		return ""
	}
	return CapiLastError(r.handle)
}

// materializeImage returns a filesystem path for an image argument that may be
// either an existing path or a base64-encoded payload. When the input is
// base64 it is decoded into a temp file; cleanup removes it (no-op for a path).
func materializeImage(arg string) (path string, cleanup func(), err error) {
	cleanup = func() {}
	if _, statErr := os.Stat(arg); statErr == nil {
		return arg, cleanup, nil
	}
	// Strip an optional data URL prefix (data:image/...;base64,<payload>).
	b64 := arg
	if i := indexComma(b64); i >= 0 && hasDataPrefix(b64) {
		b64 = b64[i+1:]
	}
	data, decErr := base64.StdEncoding.DecodeString(b64)
	if decErr != nil {
		return "", cleanup, fmt.Errorf("image is neither an existing path nor valid base64: %v", decErr)
	}
	f, tErr := os.CreateTemp("", "depth-anything-*.img")
	if tErr != nil {
		return "", cleanup, tErr
	}
	if _, wErr := f.Write(data); wErr != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", cleanup, wErr
	}
	_ = f.Close()
	name := f.Name()
	return name, func() { _ = os.Remove(name) }, nil
}

func hasDataPrefix(s string) bool {
	return len(s) >= 5 && s[:5] == "data:"
}

func indexComma(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == ',' {
			return i
		}
	}
	return -1
}

// writeDepthPNG min-max normalises a depth map and writes it as an 8-bit
// grayscale PNG. Near = bright (255), far = dark (0), matching the usual
// depth-map convention for inverse-depth-like outputs.
func writeDepthPNG(dst string, depth []float32, h, w int) error {
	if h <= 0 || w <= 0 || len(depth) < h*w {
		return fmt.Errorf("depth-anything-cpp: writeDepthPNG: bad dims h=%d w=%d len=%d", h, w, len(depth))
	}
	dmin, dmax := minMax(depth)
	span := dmax - dmin
	if span <= 0 || math.IsNaN(float64(span)) {
		span = 1
	}
	img := image.NewGray(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := depth[y*w+x]
			n := (v - dmin) / span // 0..1
			if math.IsNaN(float64(n)) {
				n = 0
			}
			if n < 0 {
				n = 0
			} else if n > 1 {
				n = 1
			}
			img.Pix[y*img.Stride+x] = uint8(n * 255)
		}
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return png.Encode(f, img)
}

func minMax(v []float32) (mn, mx float32) {
	if len(v) == 0 {
		return 0, 0
	}
	mn, mx = v[0], v[0]
	for _, x := range v {
		if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
			continue
		}
		if x < mn {
			mn = x
		}
		if x > mx {
			mx = x
		}
	}
	return mn, mx
}
