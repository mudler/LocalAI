// SPDX-License-Identifier: MIT
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/pixal3d"
)

var modelFiles = []string{"dinov3.gguf", "pixal3d_naf.gguf", "pixal3d_ss_flow_mv.gguf", "ss_dec.gguf", "pixal3d_shape_flow_512_mv.gguf", "shape_dec.gguf", "pixal3d_shape_flow_1024_mv.gguf", "pixal3d_tex_flow_1024_mv.gguf", "tex_dec.gguf"}

// Pixal3D registers a validated model directory in the normal backend lifecycle.
// This adapter invokes the native CLI, so each request reloads
// weights and releases native memory when the child exits.
type Pixal3D struct {
	base.Base
	mu     sync.Mutex
	models string
	cli    string
	cancel context.CancelFunc
}

func validateModels(dir string) error {
	f, err := os.Open(filepath.Join(dir, "pixal3d-models.json"))
	if err != nil {
		return fmt.Errorf("Pixal3D model manifest: %w", err)
	}
	var manifest struct {
		Schema int    `json:"schema_version"`
		Family string `json:"model_family"`
		Files  []struct {
			Name string `json:"name"`
			Size int64  `json:"size_bytes"`
		} `json:"files"`
	}
	err = json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(&manifest)
	f.Close()
	if err != nil || manifest.Schema != 1 || (manifest.Family != "" && manifest.Family != "mv") {
		return fmt.Errorf("invalid Pixal3D MV model manifest")
	}
	sizes := map[string]int64{}
	for _, file := range manifest.Files {
		if _, exists := sizes[file.Name]; exists {
			return fmt.Errorf("duplicate manifest component %s", file.Name)
		}
		sizes[file.Name] = file.Size
	}
	for _, name := range modelFiles {
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			return fmt.Errorf("not a Pixal3D MV model set: %s: %w", name, err)
		}
		stat, err := f.Stat()
		var magic [4]byte
		_, readErr := io.ReadFull(f, magic[:])
		f.Close()
		if err != nil || !stat.Mode().IsRegular() || readErr != nil || string(magic[:]) != "GGUF" || sizes[name] <= 0 || stat.Size() != sizes[name] {
			return fmt.Errorf("invalid Pixal3D component %s (check manifest and file size)", name)
		}
	}
	return nil
}

func (p *Pixal3D) Load(opts *pb.ModelOptions) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cancel != nil {
		return fmt.Errorf("cannot load Pixal3D while generation is running")
	}
	dir := opts.ModelFile
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(opts.ModelPath, dir)
	}
	if err := validateModels(dir); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cli := filepath.Join(filepath.Dir(exe), "trellis-cli")
	if st, err := os.Stat(cli); err != nil || st.IsDir() {
		return fmt.Errorf("packaged Pixal3D trellis-cli is missing")
	}
	p.models, p.cli = dir, cli
	return nil
}

func (p *Pixal3D) Free() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cancel != nil {
		p.cancel()
	}
	p.models = ""
	return nil
}

func commandArgs(models, views, output string, req *pb.Generate3DRequest) ([]string, error) {
	if err := pixal3d.ValidateRequest(req.Src, req.Images, req.MeshScale, req.Quality, req.Background); err != nil {
		return nil, err
	}
	if req.Step != 0 || req.CfgScale != 0 || req.TextureSteps != 0 || req.Seed != 0 || len(req.Params) != 0 {
		return nil, fmt.Errorf("Pixal3D sampling overrides and params are unsupported")
	}
	if req.Dst == "" {
		return nil, fmt.Errorf("output path is required")
	}
	return []string{"--views", views, "--models", models, "--res", "1024", "--mesh-scale", strconv.FormatFloat(req.MeshScale, 'g', -1, 64), "--output", output}, nil
}

func (p *Pixal3D) Generate3D(req *pb.Generate3DRequest) error {
	p.mu.Lock()
	if p.models == "" {
		p.mu.Unlock()
		return fmt.Errorf("Pixal3D model is not loaded")
	}
	if p.cancel != nil {
		p.mu.Unlock()
		return fmt.Errorf("Pixal3D generation already running")
	}
	models, cli := p.models, p.cli
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.mu.Unlock()
	defer func() { cancel(); p.mu.Lock(); p.cancel = nil; p.mu.Unlock() }()
	dir, err := os.MkdirTemp("", "pixal3d-views-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	// The native CLI writes texture and mesh sidecars beside its GLB.
	outputDir, err := os.MkdirTemp("", "pixal3d-output-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(outputDir)
	output := filepath.Join(outputDir, "output.glb")
	args, err := commandArgs(models, dir, output, req)
	if err != nil {
		return err
	}
	names := []string{"0_front.png", "1_right.png", "2_back.png", "3_left.png"}
	for i, path := range req.Images {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(f, pixal3d.MaxInputBytes+1))
		f.Close()
		if err != nil {
			return err
		}
		if err = pixal3d.ValidatePNG(data); err != nil {
			return fmt.Errorf("view %d: %w", i, err)
		}
		if err = os.WriteFile(filepath.Join(dir, names[i]), data, 0600); err != nil {
			return err
		}
	}
	loader := filepath.Join(filepath.Dir(cli), "lib", "ld.so")
	if _, err := os.Stat(loader); err == nil {
		args = append([]string{cli}, args...)
		cli = loader
	}
	cmd := exec.CommandContext(ctx, cli, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Pixal3D generation failed: %w", err)
	}
	f, err := os.Open(output)
	if err != nil {
		return err
	}
	defer f.Close()
	var header [4]byte
	if _, err := io.ReadFull(f, header[:]); err != nil || string(header[:]) != "glTF" {
		return fmt.Errorf("Pixal3D did not produce a GLB")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	// Copy because the destination may be on a different filesystem.
	dst, err := os.Create(req.Dst)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(dst, f)
	closeErr := dst.Close()
	if copyErr != nil || closeErr != nil {
		os.Remove(req.Dst)
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	}
	return nil
}
