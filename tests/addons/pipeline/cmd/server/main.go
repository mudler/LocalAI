// SPDX-License-Identifier: MIT
// This loopback-only acceptance server exercises the production runner and store;
// only inference and the SDK HTTP transport are replaced.
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"net/http"
	"time"

	"github.com/google/uuid"
	ap "github.com/mudler/LocalAI/pkg/assetpipeline"
)

type executor struct{ store *ap.Store }

func (e executor) Execute(ctx context.Context, _, resource string, a ap.Arguments, progress func(string)) (ap.Result, error) {
	progress("Mock backend: processing " + string(a.Node.Kind))
	delay := 1300 * time.Millisecond
	if a.Node.Model == "mock-slow" {
		delay = 10 * time.Second
	}
	select {
	case <-time.After(delay):
	case <-ctx.Done():
		return ap.Result{}, ctx.Err()
	}
	if a.Node.Model == "mock-fail" {
		return ap.Result{}, errors.New("Mock backend failed deliberately; retry is safe")
	}
	switch a.Node.Kind {
	case ap.Source:
		return ap.Result{Outputs: []ap.Value{a.Input}}, nil
	case ap.Review:
		if a.Node.Params["purpose"] == "motion" {
			return ap.Result{Outputs: []ap.Value{{Type: "text", Text: a.Input.Text + ", steady human movement"}}, Explanation: "A clear direction and pace help describe the motion."}, nil
		}
		return ap.Result{Outputs: []ap.Value{{Type: "text", Text: a.Input.Text + ", centered game asset on a black background"}}, Explanation: "A plain background helps isolate the asset."}, nil
	case ap.Extract:
		return ap.Result{Outputs: []ap.Value{a.Input}, Explanation: "Mock object mask applied."}, nil
	}
	count := 1
	if a.Node.Kind == ap.Image {
		count = a.Node.Count
	}
	result := ap.Result{Diagnostics: []string{"mock inference completed; best-effort model lease released"}}
	for i := 0; i < count; i++ {
		var data []byte
		name, mime, typ := "asset.png", "image/png", "image"
		if a.Node.Kind == ap.Mesh || a.Node.Kind == ap.Remesh || a.Node.Kind == ap.Rig || a.Node.Kind == ap.Skin || a.Node.Kind == ap.Retarget || a.Node.Kind == ap.Motion {
			data = triangle()
			name, mime, typ = "asset.glb", "model/gltf-binary", "mesh"
			if a.Node.Kind == ap.Motion {
				data = animatedFixture(false)
				name, typ = "motion.glb", "motion"
			} else if a.Node.Kind == ap.Rig || a.Node.Kind == ap.Skin || a.Node.Kind == ap.Retarget {
				data = animatedFixture(true)
			}
		} else {
			pic := image.NewRGBA(image.Rect(0, 0, 64, 64))
			for y := 0; y < 64; y++ {
				for x := 0; x < 64; x++ {
					pic.Set(x, y, color.RGBA{uint8(50 + i*40), uint8(x * 3), uint8(y * 3), 255})
				}
			}
			var b bytes.Buffer
			_ = png.Encode(&b, pic)
			data = b.Bytes()
		}
		artifact, err := e.store.PutArtifact(ctx, resource, name, ap.ArtifactMIME(mime), bytes.NewReader(data))
		if err != nil {
			return result, err
		}
		result.Outputs = append(result.Outputs, ap.Value{Type: ap.ValueType(typ), Artifact: artifact.ID})
	}
	return result, nil
}

// Two moving joints and a skinned triangle exercise the production viewer without
// downloading a model or shipping third-party animation assets.
func animatedFixture(mesh bool) []byte {
	var doc map[string]any
	_ = json.Unmarshal([]byte(`{"asset":{"version":"2.0"},"scene":0,"scenes":[{"nodes":[0,2]}],"nodes":[{"name":"Root","children":[1]},{"name":"Tip","translation":[0,1,0]},{"mesh":0,"skin":0}],"meshes":[{"primitives":[{"attributes":{"POSITION":0,"JOINTS_0":2,"WEIGHTS_0":3},"indices":1}]}],"skins":[{"joints":[0,1],"inverseBindMatrices":4,"skeleton":0}],"animations":[{"name":"Walk","samplers":[{"input":5,"output":6,"interpolation":"LINEAR"}],"channels":[{"sampler":0,"target":{"node":0,"path":"translation"}}]}],"buffers":[{"byteLength":264}],"bufferViews":[{"buffer":0,"byteOffset":0,"byteLength":36},{"buffer":0,"byteOffset":36,"byteLength":6},{"buffer":0,"byteOffset":44,"byteLength":12},{"buffer":0,"byteOffset":56,"byteLength":48},{"buffer":0,"byteOffset":104,"byteLength":128},{"buffer":0,"byteOffset":232,"byteLength":8},{"buffer":0,"byteOffset":240,"byteLength":24}],"accessors":[{"bufferView":0,"componentType":5126,"count":3,"type":"VEC3","min":[-0.3,0,0],"max":[0.3,1,0]},{"bufferView":1,"componentType":5123,"count":3,"type":"SCALAR"},{"bufferView":2,"componentType":5121,"count":3,"type":"VEC4"},{"bufferView":3,"componentType":5126,"count":3,"type":"VEC4"},{"bufferView":4,"componentType":5126,"count":2,"type":"MAT4"},{"bufferView":5,"componentType":5126,"count":2,"type":"SCALAR","min":[0],"max":[2]},{"bufferView":6,"componentType":5126,"count":2,"type":"VEC3"}]}`), &doc)
	if !mesh {
		delete(doc, "meshes")
		delete(doc, "skins")
		doc["nodes"] = doc["nodes"].([]any)[:2]
		doc["scenes"] = []any{map[string]any{"nodes": []int{0}}}
	}
	// Relative joint motion remains visible when the camera follows root motion.
	doc["buffers"].([]any)[0].(map[string]any)["byteLength"] = 288
	doc["bufferViews"] = append(doc["bufferViews"].([]any), map[string]any{"buffer": 0, "byteOffset": 264, "byteLength": 24})
	doc["accessors"] = append(doc["accessors"].([]any), map[string]any{"bufferView": 7, "componentType": 5126, "count": 2, "type": "VEC3"})
	animation := doc["animations"].([]any)[0].(map[string]any)
	animation["samplers"] = append(animation["samplers"].([]any), map[string]any{"input": 5, "output": 7, "interpolation": "LINEAR"})
	animation["channels"] = append(animation["channels"].([]any), map[string]any{"sampler": 1, "target": map[string]any{"node": 1, "path": "translation"}})
	bin := make([]byte, 288)
	put := func(offset int, values ...float32) {
		for i, v := range values {
			binary.LittleEndian.PutUint32(bin[offset+i*4:], math.Float32bits(v))
		}
	}
	put(0, -.3, 0, 0, .3, 0, 0, 0, 1, 0)
	binary.LittleEndian.PutUint16(bin[38:], 1)
	binary.LittleEndian.PutUint16(bin[40:], 2)
	bin[52] = 1
	put(56, 1, 0, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0)
	put(104, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1)
	put(168, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, -1, 0, 1)
	put(232, 0, 2)
	put(240, 0, 0, 0, 1, 0, 0)
	put(264, 0, 1, 0, .4, .6, 0)
	metadata, _ := json.Marshal(doc)
	for len(metadata)%4 != 0 {
		metadata = append(metadata, ' ')
	}
	var result bytes.Buffer
	for _, v := range []uint32{0x46546c67, 2, uint32(28 + len(metadata) + len(bin)), uint32(len(metadata)), 0x4e4f534a} {
		_ = binary.Write(&result, binary.LittleEndian, v)
	}
	result.Write(metadata)
	_ = binary.Write(&result, binary.LittleEndian, uint32(len(bin)))
	_ = binary.Write(&result, binary.LittleEndian, uint32(0x004e4942))
	result.Write(bin)
	return result.Bytes()
}

func triangle() []byte {
	doc := []byte(`{"asset":{"version":"2.0"},"scene":0,"scenes":[{"nodes":[0]}],"nodes":[{"mesh":0}],"meshes":[{"primitives":[{"attributes":{"POSITION":0},"indices":1}]}],"buffers":[{"byteLength":44}],"bufferViews":[{"buffer":0,"byteOffset":0,"byteLength":36},{"buffer":0,"byteOffset":36,"byteLength":6}],"accessors":[{"bufferView":0,"componentType":5126,"count":3,"type":"VEC3","min":[-1,-1,0],"max":[1,1,0]},{"bufferView":1,"componentType":5123,"count":3,"type":"SCALAR"}]}`)
	for len(doc)%4 != 0 {
		doc = append(doc, ' ')
	}
	bin := make([]byte, 44)
	for i, v := range []float32{-1, -1, 0, 1, -1, 0, 0, 1, 0} {
		binary.LittleEndian.PutUint32(bin[i*4:], math.Float32bits(v))
	}
	binary.LittleEndian.PutUint16(bin[38:], 1)
	binary.LittleEndian.PutUint16(bin[40:], 2)
	var b bytes.Buffer
	for _, v := range []uint32{0x46546c67, 2, uint32(12 + 8 + len(doc) + 8 + len(bin)), uint32(len(doc)), 0x4e4f534a} {
		_ = binary.Write(&b, binary.LittleEndian, v)
	}
	b.Write(doc)
	_ = binary.Write(&b, binary.LittleEndian, uint32(len(bin)))
	_ = binary.Write(&b, binary.LittleEndian, uint32(0x004e4942))
	b.Write(bin)
	return b.Bytes()
}
func main() {
	dir := flag.String("data", "/tmp/localai-pipeline-acceptance", "store")
	ui := flag.String("ui", "tests/addons/pipeline/dist", "test UI")
	addr := flag.String("addr", "127.0.0.1:8097", "loopback address")
	flag.Parse()
	store, err := ap.Open(*dir)
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	runner, err := ap.NewRunner(context.Background(), store, executor{store})
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = runner.Close(context.Background()) }()
	http.HandleFunc("/rpc", func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			Method    string     `json:"method"`
			Resource  string     `json:"resource"`
			Branch    string     `json:"branch"`
			Before    int        `json:"before"`
			Operation string     `json:"operation"`
			Revision  uint64     `json:"revision"`
			Command   ap.Command `json:"command"`
			Name      string     `json:"name"`
			MIME      string     `json:"mime"`
			Data      string     `json:"data"`
			Upload    string     `json:"upload"`
			Offset    int        `json:"offset"`
			Final     bool       `json:"final"`
		}
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		var out any
		var err error
		switch q.Method {
		case "list":
			var ids []string
			ids, err = store.IDs(r.Context())
			resources := []map[string]string{}
			for _, id := range ids {
				v, e := runner.Read(r.Context(), id)
				if e == nil {
					resources = append(resources, map[string]string{"id": id, "name": v.Name})
				}
			}
			out = map[string]any{"resources": resources}
		case "create":
			id := uuid.NewString()
			_, err = runner.Apply(r.Context(), "acceptance", id, uuid.NewString(), 0, ap.Command{Action: "create", Name: q.Name})
			out = map[string]string{"id": id}
		case "inspect":
			var state ap.Workspace
			state, err = runner.Read(r.Context(), q.Resource)
			out = map[string]any{"view": ap.MakeView(state, q.Branch, q.Before)}
		case "apply":
			var state ap.Workspace
			state, err = runner.Apply(r.Context(), "acceptance", q.Resource, q.Operation, q.Revision, q.Command)
			branch := q.Command.Branch
			if q.Command.Action == "fork" || q.Command.Action == "retry" {
				branch = ""
			}
			if q.Command.Action == "run-node" || q.Command.Action == "run-all" || q.Command.Action == "continue" {
				branch = state.ResultBranch
			}
			out = map[string]any{"view": ap.MakeView(state, branch, 0)}
		case "upload":
			var data []byte
			data, err = base64.StdEncoding.DecodeString(q.Data)
			if err == nil {
				var a ap.Artifact
				if q.Upload != "" {
					a, err = store.Upload(r.Context(), q.Resource, q.Upload, q.Name, ap.ArtifactMIME(q.MIME), q.Offset, data, q.Final)
				} else {
					a, err = store.PutArtifact(r.Context(), q.Resource, q.Name, ap.ArtifactMIME(q.MIME), bytes.NewReader(data))
				}
				out = map[string]string{"id": a.ID}
			}
		default:
			err = errors.New("unknown method")
		}
		if err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})
	http.HandleFunc("/artifact", func(w http.ResponseWriter, r *http.Request) {
		f, a, err := store.OpenArtifact(r.Context(), r.URL.Query().Get("resource"), r.URL.Query().Get("id"))
		if err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		defer func() { _ = f.Close() }()
		w.Header().Set("Content-Type", string(a.MIME))
		http.ServeContent(w, r, a.Name, time.Time{}, f)
	})
	http.HandleFunc("/fixture.png", func(w http.ResponseWriter, r *http.Request) {
		pic := image.NewNRGBA(image.Rect(0, 0, 512, 512))
		for y := 0; y < 512; y++ {
			for x := 0; x < 512; x++ {
				pic.SetNRGBA(x, y, color.NRGBA{uint8(x), uint8(y), uint8(x + y), 255})
			}
		}
		w.Header().Set("Content-Type", "image/png")
		encoder := png.Encoder{CompressionLevel: png.NoCompression}
		_ = encoder.Encode(w, pic)
	})
	http.Handle("/", http.FileServer(http.Dir(*ui)))
	log.Printf("acceptance server http://%s", *addr)
	log.Fatal(http.ListenAndServe(*addr, nil))
}
