// SPDX-License-Identifier: MIT
package main

import (
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/xlog"
)

type MeshRemesh struct {
	base.Base
	mu     sync.Mutex
	loaded bool
}

func (m *MeshRemesh) Load(options *pb.ModelOptions) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	// The model loader probes installed engines when backend is unspecified.
	// A weight-free processor must never claim arbitrary LLM/model files.
	identity := options.Model
	if identity == "" {
		identity = options.ModelFile
	}
	if identity != "" && identity != "meshremesh" {
		return fmt.Errorf("meshremesh accepts only its own model-free configuration")
	}
	if len(options.Options) != 0 {
		return fmt.Errorf("meshremesh does not accept model options; configure each remesh request")
	}
	m.loaded = true
	return nil
}

func (m *MeshRemesh) Free() error { m.mu.Lock(); defer m.mu.Unlock(); m.loaded = false; return nil }

func remeshParameters(request *pb.Generate3DRequest) (map[string]string, error) {
	if request == nil || request.Src == "" || request.Dst == "" || strings.ContainsRune(request.Src, 0) || strings.ContainsRune(request.Dst, 0) {
		return nil, fmt.Errorf("source and destination GLB paths are required")
	}
	if filepath.Clean(request.Src) == filepath.Clean(request.Dst) {
		return nil, fmt.Errorf("destination must not replace the source")
	}
	params := maps.Clone(request.Params)
	if params == nil {
		params = map[string]string{}
	}
	operation := params["operation"]
	if operation != "remesh" && operation != "print_remesh" {
		return nil, fmt.Errorf("meshremesh only supports remeshing, not image generation")
	}
	delete(params, "operation")
	if operation == "print_remesh" {
		if params["mode"] != "" && params["mode"] != "wrap" {
			return nil, fmt.Errorf("print_remesh requires wrap mode")
		}
		params["mode"] = "wrap"
		// Legacy /3d/remesh sends this redundant trace-only percentage.
		delete(params, "detail_percent")
	}
	if err := schema.ValidateMeshRemeshParameters(params); err != nil {
		return nil, err
	}
	return params, nil
}

func (m *MeshRemesh) Generate3D(request *pb.Generate3DRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	params, err := remeshParameters(request)
	if err != nil {
		return err
	}
	if !m.loaded {
		return fmt.Errorf("remeshing backend is not loaded")
	}
	f, err := os.Open(request.Src)
	if err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(f, (512<<20)+1))
	_ = f.Close()
	if err != nil {
		return err
	}
	if len(data) > 512<<20 {
		return fmt.Errorf("source GLB exceeds 512 MiB")
	}
	mesh, err := parseVertexGLB(data)
	if err != nil {
		return err
	}
	glb, stats, err := remeshNative(mesh.verts, mesh.tris, mesh.pbr, params)
	if err != nil {
		return err
	}
	xlog.Info("Mesh remeshing completed", "stats", stats)
	glb, err = withRemeshStats(glb, stats)
	if err != nil {
		return err
	}
	output, err := os.CreateTemp(filepath.Dir(request.Dst), ".remesh-*.glb")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(output.Name()) }()
	_, writeErr := output.Write(glb)
	closeErr := output.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(output.Name(), request.Dst)
}
