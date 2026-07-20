package main

// trellis2.go — purego bindings to libtrellis2's flat C ABI (trellis2_capi.h)
// plus the LocalAI backend implementation. Adapted from the upstream demo
// server's engine.go; the t2_abi_version binding guards against header/library
// drift.

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"unsafe"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/utils"
)

const abiVersion = 11

// Pipeline types (enum t2_pipeline_type) and background modes
// (enum t2_background_mode).
const (
	pipeAuto   = 0
	pipeCoarse = 1
	pipe512    = 2
	pipe1024   = 3

	backgroundAuto  = 0
	backgroundKeep  = 1
	backgroundBlack = 2
	backgroundWhite = 3
)

// The bindings use typed pointers (*float32/*int32/*byte) rather than uintptr
// for C-owned buffers so no uintptr->unsafe.Pointer conversions are needed;
// only the opaque t2_pipeline / t2_mesh_result handles stay uintptr.
var (
	t2AbiVersion   func() int32
	t2PipelineLoad func(dino, ssFlow, ssDec, slatFlow, slatFlowHR, shapeDec,
		shapeEnc, texDec, texFlow, texFlowHR string,
		flags int32, err *byte, errLen int32) uintptr
	t2PipelineFree    func(p uintptr)
	t2PipelineBackend func(p uintptr) string
	t2PipelineCaps    func(p uintptr) int32
	t2Generate        func(p uintptr, img *byte, imgLen int32,
		pipelineType, backgroundMode int32, seed uint64, steps int32,
		guidance float32, textureSteps int32,
		progress, user, preview, previewUser uintptr,
		err *byte, errLen int32) uintptr
	t2MeshNVerts func(r uintptr) int32
	t2MeshNTris  func(r uintptr) int32
	t2MeshVerts  func(r uintptr) *float32
	t2MeshTris   func(r uintptr) *int32
	t2MeshHasPBR func(r uintptr) int32
	t2MeshPBR    func(r uintptr) *float32
	t2MeshFree   func(r uintptr)
	t2BakeGLB    func(verts *float32, nv int32, tris *int32, nt int32,
		pbr *float32, texSize, componentFilter int32,
		outLen *int32, err *byte, errLen int32) *byte
	// CGAL Alpha Wrap print remeshing — availability is fixed at library build
	// time, so gate every use on t2_print_remesh_available.
	t2PrintRemeshAvailable func() int32
	t2PreparePrintMesh     func(verts *float32, nv int32, tris *int32, nt int32,
		pbr *float32, componentFilter int32, alphaRatio, offsetRatio float32,
		err *byte, errLen int32) uintptr
	t2BakeProjectedGLB func(targetVerts *float32, targetNV int32,
		targetTris *int32, targetNT int32,
		sourceVerts *float32, sourceNV int32,
		sourceTris *int32, sourceNT int32,
		sourcePBR *float32, texSize, sourceComponentFilter int32,
		outLen *int32, err *byte, errLen int32) *byte
	t2FreeBuffer func(buf *byte)
)

type libFunc struct {
	funcPtr any
	name    string
}

func registerLibFuncsWith(register func(fptr any, name string)) {
	for _, lf := range []libFunc{
		{&t2AbiVersion, "t2_abi_version"},
		{&t2PipelineLoad, "t2_pipeline_load"},
		{&t2PipelineFree, "t2_pipeline_free"},
		{&t2PipelineBackend, "t2_pipeline_backend"},
		{&t2PipelineCaps, "t2_pipeline_caps"},
		{&t2Generate, "t2_generate"},
		{&t2MeshNVerts, "t2_mesh_n_verts"},
		{&t2MeshNTris, "t2_mesh_n_tris"},
		{&t2MeshVerts, "t2_mesh_verts"},
		{&t2MeshTris, "t2_mesh_tris"},
		{&t2MeshHasPBR, "t2_mesh_has_pbr"},
		{&t2MeshPBR, "t2_mesh_pbr"},
		{&t2MeshFree, "t2_mesh_free"},
		{&t2BakeGLB, "t2_bake_glb"},
		{&t2PrintRemeshAvailable, "t2_print_remesh_available"},
		{&t2PreparePrintMesh, "t2_prepare_print_mesh"},
		{&t2BakeProjectedGLB, "t2_bake_projected_glb"},
		{&t2FreeBuffer, "t2_free_buffer"},
	} {
		register(lf.funcPtr, lf.name)
	}
}

// modelSet holds the resolved path for every pipeline role; optional roles are
// "" when disabled (the C side treats NULL/"" as "omit").
type modelSet struct {
	dino, ssFlow, ssDec             string
	slatFlow, slatFlow1024          string
	shapeDec                        string
	shapeEnc, texDec                string
	texSlatFlow512, texSlatFlow1024 string
}

// role → (option key, default filename) in t2_pipeline_load argument order.
// The option keys follow the sd-ggml `*_path` convention; the default
// filenames are the ones the upstream converters emit and the demo server
// looks up, so a gallery install needs no options at all.
type modelRole struct {
	key      string
	filename string
	required bool
	assign   func(*modelSet, string)
}

var modelRoles = []modelRole{
	{"dino_path", "dino_f16.gguf", true, func(s *modelSet, p string) { s.dino = p }},
	{"ss_flow_path", "ss_flow_f16.gguf", true, func(s *modelSet, p string) { s.ssFlow = p }},
	{"ss_dec_path", "ss_dec_f16.gguf", true, func(s *modelSet, p string) { s.ssDec = p }},
	{"slat_flow_path", "slat_flow_f16.gguf", false, func(s *modelSet, p string) { s.slatFlow = p }},
	{"slat_flow_1024_path", "slat_flow_1024_f16.gguf", false, func(s *modelSet, p string) { s.slatFlow1024 = p }},
	{"shape_dec_path", "shape_dec_f16.gguf", false, func(s *modelSet, p string) { s.shapeDec = p }},
	{"shape_enc_path", "shape_enc_f16.gguf", false, func(s *modelSet, p string) { s.shapeEnc = p }},
	{"tex_dec_path", "tex_dec_f16.gguf", false, func(s *modelSet, p string) { s.texDec = p }},
	{"tex_slat_flow_512_path", "tex_slat_flow_512_f16.gguf", false, func(s *modelSet, p string) { s.texSlatFlow512 = p }},
	{"tex_slat_flow_1024_path", "tex_slat_flow_1024_f16.gguf", false, func(s *modelSet, p string) { s.texSlatFlow1024 = p }},
}

// resolveModels maps LocalAI's model file + options onto the ten pipeline
// roles. The model file only anchors the GGUF directory; each role resolves
// to an explicit `<role>_path` option when given, else to its default
// filename in that directory. Missing required files refuse the load (a
// backend must not capture arbitrary GGUFs — see issue #9287); missing
// optional files degrade capabilities the same way the upstream demo does.
func resolveModels(modelFile, modelPath string, options []string) (modelSet, error) {
	base := modelFile
	if !filepath.IsAbs(base) {
		base = filepath.Join(modelPath, base)
	}
	ggufDir := filepath.Dir(base)

	overrides := map[string]string{}
	for _, op := range options {
		key, value, found := strings.Cut(op, ":")
		if !found || !strings.HasSuffix(key, "_path") {
			continue
		}
		if !filepath.IsAbs(value) {
			value = filepath.Join(modelPath, value)
			if err := utils.VerifyPath(value, modelPath); err != nil {
				return modelSet{}, fmt.Errorf("option %s: %w", key, err)
			}
		}
		overrides[key] = value
	}

	var set modelSet
	var missingRequired []string
	for _, role := range modelRoles {
		path, explicit := overrides[role.key]
		if !explicit {
			path = filepath.Join(ggufDir, role.filename)
		}
		if _, err := os.Stat(path); err != nil {
			if explicit {
				return modelSet{}, fmt.Errorf("option %s points at a missing file: %s", role.key, path)
			}
			if role.required {
				missingRequired = append(missingRequired, role.filename)
			}
			path = ""
		}
		role.assign(&set, path)
	}
	if len(missingRequired) > 0 {
		return modelSet{}, fmt.Errorf("not a trellis2 model set: missing required %s in %s", strings.Join(missingRequired, ", "), ggufDir)
	}

	// Degradation mirrors the upstream demo: the 512 pair enables everything
	// finer than coarse; texturing needs its three-model set; a textured 1024
	// cascade additionally needs the HR texture flow.
	if set.slatFlow == "" || set.shapeDec == "" {
		set.slatFlow, set.shapeDec = "", ""
		set.slatFlow1024 = ""
		set.shapeEnc, set.texDec, set.texSlatFlow512, set.texSlatFlow1024 = "", "", "", ""
		return set, nil
	}
	if set.shapeEnc == "" || set.texDec == "" || set.texSlatFlow512 == "" {
		set.shapeEnc, set.texDec, set.texSlatFlow512, set.texSlatFlow1024 = "", "", "", ""
	} else if set.texSlatFlow1024 == "" {
		set.slatFlow1024 = ""
	}
	return set, nil
}

func pipelineForQuality(quality string) int32 {
	switch quality {
	case "coarse":
		return pipeCoarse
	case "512":
		return pipe512
	case "1024":
		return pipe1024
	default:
		return pipeAuto
	}
}

func backgroundForMode(background string) int32 {
	switch background {
	case "keep":
		return backgroundKeep
	case "black":
		return backgroundBlack
	case "white":
		return backgroundWhite
	default:
		return backgroundAuto
	}
}

func componentFilterFor(components string) int32 {
	switch components {
	case "tiny":
		return 0 // remove only tiny islands
	case "largest":
		return 1 // keep the largest connected component
	default:
		return 2 // preserve every connected component (demo default)
	}
}

func atoiOr(s string, fallback int32) int32 {
	if s == "" {
		return fallback
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return int32(n)
}

func boolParam(s string) bool {
	return s == "1" || strings.EqualFold(s, "true")
}

// ratioOr parses a fraction-of-bounding-box-diagonal parameter. Out-of-range
// or unparseable values fall back rather than error, mirroring atoiOr; the
// accepted range matches what the upstream demo clamps to.
func ratioOr(s string, fallback float32) float32 {
	if s == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(s, 32)
	if err != nil || f < 0.00001 || f > 0.5 {
		return fallback
	}
	return float32(f)
}

type Trellis2 struct {
	base.SingleThread
	// t2_generate is not thread-safe per pipeline. The gRPC server already
	// serializes calls via Locking(), but keep a local mutex too so the
	// invariant doesn't depend on the transport.
	mu       sync.Mutex
	pipeline uintptr
}

func (t *Trellis2) Load(opts *pb.ModelOptions) error {
	set, err := resolveModels(opts.ModelFile, opts.ModelPath, opts.Options)
	if err != nil {
		return err
	}

	errBuf := make([]byte, 512)
	p := t2PipelineLoad(set.dino, set.ssFlow, set.ssDec,
		set.slatFlow, set.slatFlow1024, set.shapeDec,
		set.shapeEnc, set.texDec, set.texSlatFlow512, set.texSlatFlow1024,
		0 /*flags*/, &errBuf[0], int32(len(errBuf)))
	if p == 0 {
		return fmt.Errorf("trellis2 pipeline load: %s", cstr(errBuf))
	}

	t.mu.Lock()
	if t.pipeline != 0 {
		t2PipelineFree(t.pipeline)
	}
	t.pipeline = p
	t.mu.Unlock()

	fmt.Fprintf(os.Stderr, "trellis2 pipeline loaded: backend=%s caps=%#x\n",
		t2PipelineBackend(p), t2PipelineCaps(p))
	return nil
}

func (t *Trellis2) Free() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.pipeline != 0 {
		t2PipelineFree(t.pipeline)
		t.pipeline = 0
	}
	return nil
}

func (t *Trellis2) Generate3D(opts *pb.Generate3DRequest) error {
	if opts.Dst == "" {
		return fmt.Errorf("dst is empty")
	}
	if opts.GetParams()["operation"] == "print_remesh" {
		t.mu.Lock()
		defer t.mu.Unlock()
		return remeshGLB(opts)
	}
	img, err := os.ReadFile(opts.Src)
	if err != nil {
		return fmt.Errorf("reading conditioning image: %w", err)
	}
	if len(img) == 0 {
		return fmt.Errorf("conditioning image is empty")
	}

	seed := uint64(opts.Seed)
	if opts.Seed <= 0 {
		seed = rand.Uint64()
	}
	guidance := opts.CfgScale
	if guidance <= 0 {
		guidance = -1 // <0 selects the pipeline default (7.5)
	}
	texSize := atoiOr(opts.GetParams()["texture_size"], 0) // <=0 selects the bake default
	componentFilter := componentFilterFor(opts.GetParams()["components"])

	// Optional CGAL Alpha Wrap: wrap the generated mesh into a watertight,
	// intersection-free 2-manifold for 3D printing. Ratios are fractions of
	// the bounding-box diagonal; offset defaults to alpha/30 per the CGAL
	// guideline the upstream demo uses. Offset is deliberately not an
	// independent parameter: looser values produce puffy or degenerate wraps.
	printRemesh := boolParam(opts.GetParams()["print_remesh"])
	alphaRatio := ratioOr(opts.GetParams()["alpha_ratio"], 0.005)
	offsetRatio := alphaRatio / 30
	if printRemesh && t2PrintRemeshAvailable() == 0 {
		return fmt.Errorf("print_remesh requested but libtrellis2 was built without CGAL Alpha Wrap")
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.pipeline == 0 {
		return fmt.Errorf("model not loaded")
	}

	errBuf := make([]byte, 512)
	r := t2Generate(t.pipeline, &img[0], int32(len(img)),
		pipelineForQuality(opts.Quality), backgroundForMode(opts.Background),
		seed, opts.Step, guidance, opts.TextureSteps,
		0, 0, 0, 0, // no progress/preview callbacks
		&errBuf[0], int32(len(errBuf)))
	if r == 0 {
		return fmt.Errorf("trellis2 generate: %s", cstr(errBuf))
	}
	defer t2MeshFree(r)

	nv := t2MeshNVerts(r)
	nt := t2MeshNTris(r)
	if nv == 0 || nt == 0 {
		return fmt.Errorf("empty mesh")
	}
	var pbr *float32
	if t2MeshHasPBR(r) != 0 {
		pbr = t2MeshPBR(r)
	}

	// Bake straight from the mesh accessor buffers — they stay valid until
	// t2_mesh_free, so no copies are needed.
	var outLen int32
	var glb *byte
	if printRemesh {
		wrap := t2PreparePrintMesh(t2MeshVerts(r), nv, t2MeshTris(r), nt, pbr,
			componentFilter, alphaRatio, offsetRatio,
			&errBuf[0], int32(len(errBuf)))
		if wrap == 0 {
			return fmt.Errorf("trellis2 print remesh: %s", cstr(errBuf))
		}
		defer t2MeshFree(wrap)
		wnv, wnt := t2MeshNVerts(wrap), t2MeshNTris(wrap)
		if wnv == 0 || wnt == 0 {
			return fmt.Errorf("empty print mesh")
		}
		if pbr != nil {
			// Wrapping creates new vertices, so the source material is
			// reprojected per texel onto the wrap's UV atlas (demo handleGLB).
			glb = t2BakeProjectedGLB(t2MeshVerts(wrap), wnv, t2MeshTris(wrap), wnt,
				t2MeshVerts(r), nv, t2MeshTris(r), nt, pbr,
				texSize, componentFilter,
				&outLen, &errBuf[0], int32(len(errBuf)))
		} else {
			glb = t2BakeGLB(t2MeshVerts(wrap), wnv, t2MeshTris(wrap), wnt, nil,
				texSize, 2, // the wrap output is already component-filtered
				&outLen, &errBuf[0], int32(len(errBuf)))
		}
	} else {
		glb = t2BakeGLB(t2MeshVerts(r), nv, t2MeshTris(r), nt, pbr,
			texSize, componentFilter,
			&outLen, &errBuf[0], int32(len(errBuf)))
	}
	if glb == nil {
		return fmt.Errorf("trellis2 GLB bake: %s", cstr(errBuf))
	}
	defer t2FreeBuffer(glb)

	out := make([]byte, int(outLen))
	copy(out, unsafe.Slice(glb, int(outLen)))
	return os.WriteFile(opts.Dst, out, 0600)
}

// remeshGLB applies the demo's post-generation print workflow to an existing
// dense vertex-PBR GLB. It does not touch the inference pipeline: CGAL wrapping,
// UV unwrapping, and PBR projection are CPU-only post-processing operations.
func remeshGLB(opts *pb.Generate3DRequest) error {
	if opts.Src == "" {
		return fmt.Errorf("src is empty")
	}
	if t2PrintRemeshAvailable() == 0 {
		return fmt.Errorf("print remeshing is unavailable (libtrellis2 was built without CGAL Alpha Wrap)")
	}
	data, err := os.ReadFile(opts.Src)
	if err != nil {
		return fmt.Errorf("reading source GLB: %w", err)
	}
	mesh, err := parseVertexGLB(data)
	if err != nil {
		return fmt.Errorf("reading source GLB: %w", err)
	}

	params := opts.GetParams()
	alphaRatio := ratioOr(params["alpha_ratio"], 0.005)
	offsetRatio := alphaRatio / 30
	componentFilter := componentFilterFor(params["components"])
	textureSize := atoiOr(params["texture_size"], 2048)
	var sourcePBR *float32
	if len(mesh.pbr) != 0 {
		sourcePBR = &mesh.pbr[0]
	}
	errBuf := make([]byte, 512)
	wrap := t2PreparePrintMesh(
		&mesh.verts[0], int32(len(mesh.verts)/3),
		&mesh.tris[0], int32(len(mesh.tris)/3),
		sourcePBR, componentFilter, alphaRatio, offsetRatio,
		&errBuf[0], int32(len(errBuf)),
	)
	if wrap == 0 {
		return fmt.Errorf("trellis2 print remesh: %s", cstr(errBuf))
	}
	defer t2MeshFree(wrap)

	wrappedVerts, wrappedTris := t2MeshNVerts(wrap), t2MeshNTris(wrap)
	if wrappedVerts == 0 || wrappedTris == 0 {
		return fmt.Errorf("empty print mesh")
	}
	var outLen int32
	var glb *byte
	if sourcePBR != nil {
		glb = t2BakeProjectedGLB(
			t2MeshVerts(wrap), wrappedVerts, t2MeshTris(wrap), wrappedTris,
			&mesh.verts[0], int32(len(mesh.verts)/3),
			&mesh.tris[0], int32(len(mesh.tris)/3), sourcePBR,
			int32(textureSize), componentFilter,
			&outLen, &errBuf[0], int32(len(errBuf)),
		)
	} else {
		glb = t2BakeGLB(
			t2MeshVerts(wrap), wrappedVerts, t2MeshTris(wrap), wrappedTris,
			nil, int32(textureSize), 2,
			&outLen, &errBuf[0], int32(len(errBuf)),
		)
	}
	if glb == nil || outLen <= 0 {
		return fmt.Errorf("trellis2 GLB bake: %s", cstr(errBuf))
	}
	defer t2FreeBuffer(glb)

	out := make([]byte, int(outLen))
	copy(out, unsafe.Slice(glb, int(outLen)))
	return os.WriteFile(opts.Dst, out, 0o600)
}

func cstr(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}
