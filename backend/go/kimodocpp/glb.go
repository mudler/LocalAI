// SPDX-License-Identifier: MIT
package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
)

type animationJoint struct {
	Name   string
	Parent int
	Offset [3]float32
}

type animationAccessor struct {
	BufferView    int       `json:"bufferView"`
	ComponentType int       `json:"componentType"`
	Count         int       `json:"count"`
	Type          string    `json:"type"`
	Min           []float32 `json:"min,omitempty"`
	Max           []float32 `json:"max,omitempty"`
}

func writeAnimationGLB(path string, roots, rotations []float32, joints []animationJoint) error {
	frames := len(roots) / 3
	if frames < 1 || len(joints) == 0 || len(roots)%3 != 0 || len(rotations) != frames*len(joints)*4 {
		return fmt.Errorf("invalid animation dimensions")
	}
	for _, values := range [][]float32{roots, rotations} {
		for _, value := range values {
			if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
				return fmt.Errorf("animation contains non-finite values")
			}
		}
	}
	nodes := make([]map[string]any, len(joints))
	for index, joint := range joints {
		if joint.Name == "" || (index == 0 && joint.Parent != -1) || (index > 0 && (joint.Parent < 0 || joint.Parent >= index)) {
			return fmt.Errorf("invalid skeleton hierarchy at joint %d", index)
		}
		nodes[index] = map[string]any{"name": joint.Name, "translation": joint.Offset}
		children := []int{}
		for child, candidate := range joints {
			if candidate.Parent == index {
				children = append(children, child)
			}
		}
		if len(children) > 0 {
			nodes[index]["children"] = children
		}
	}
	times := make([]float32, frames)
	for frame := range times {
		times[frame] = float32(frame) / 30
	}
	bin := make([]byte, 0, 4*(len(times)+len(roots)+len(rotations)))
	views := []map[string]int{}
	accessors := []animationAccessor{}
	addTrack := func(values []float32, kind string) int {
		offset := len(bin)
		for _, value := range values {
			bin = binary.LittleEndian.AppendUint32(bin, math.Float32bits(value))
		}
		views = append(views, map[string]int{"buffer": 0, "byteOffset": offset, "byteLength": len(bin) - offset})
		accessors = append(accessors, animationAccessor{BufferView: len(views) - 1, ComponentType: 5126, Count: frames, Type: kind})
		return len(accessors) - 1
	}
	addTrack(times, "SCALAR")
	accessors[0].Min, accessors[0].Max = []float32{0}, []float32{times[frames-1]}
	samplers, channels := []map[string]any{}, []map[string]any{}
	addChannel := func(node int, target string, accessor int) {
		samplers = append(samplers, map[string]any{"input": 0, "output": accessor, "interpolation": "LINEAR"})
		channels = append(channels, map[string]any{"sampler": len(samplers) - 1, "target": map[string]any{"node": node, "path": target}})
	}
	addChannel(0, "translation", addTrack(roots, "VEC3"))
	track := make([]float32, frames*4)
	for joint := range joints {
		for frame := range frames {
			copy(track[frame*4:], rotations[(frame*len(joints)+joint)*4:][:4])
		}
		addChannel(joint, "rotation", addTrack(track, "VEC4"))
	}
	document := map[string]any{
		"asset": map[string]string{"version": "2.0", "generator": "LocalAI kimodocpp"},
		"scene": 0, "scenes": []map[string]any{{"nodes": []int{0}}}, "nodes": nodes,
		"buffers": []map[string]int{{"byteLength": len(bin)}}, "bufferViews": views, "accessors": accessors,
		"animations": []map[string]any{{"name": "Motion", "samplers": samplers, "channels": channels}},
		"extras":     map[string]any{"fps": 30, "output_type": "skeleton_animation"},
	}
	jsonChunk, err := json.Marshal(document)
	if err != nil {
		return err
	}
	for len(jsonChunk)%4 != 0 {
		jsonChunk = append(jsonChunk, ' ')
	}
	output := make([]byte, 0, 28+len(jsonChunk)+len(bin))
	for _, value := range []uint32{0x46546c67, 2, uint32(28 + len(jsonChunk) + len(bin)), uint32(len(jsonChunk)), 0x4e4f534a} {
		output = binary.LittleEndian.AppendUint32(output, value)
	}
	output = append(output, jsonChunk...)
	output = binary.LittleEndian.AppendUint32(output, uint32(len(bin)))
	output = binary.LittleEndian.AppendUint32(output, 0x004e4942)
	output = append(output, bin...)
	return os.WriteFile(path, output, 0o600)
}
