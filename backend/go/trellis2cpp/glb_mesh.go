package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
)

const (
	glbMagic     = 0x46546c67
	glbJSONChunk = 0x4e4f534a
	glbBINChunk  = 0x004e4942
)

type glbAccessor struct {
	BufferView    int    `json:"bufferView"`
	ByteOffset    int    `json:"byteOffset"`
	ComponentType int    `json:"componentType"`
	Count         int    `json:"count"`
	Type          string `json:"type"`
	Normalized    bool   `json:"normalized"`
}

type glbBufferView struct {
	Buffer     int `json:"buffer"`
	ByteOffset int `json:"byteOffset"`
	ByteLength int `json:"byteLength"`
	ByteStride int `json:"byteStride"`
}

type glbPrimitive struct {
	Attributes map[string]int `json:"attributes"`
	Indices    *int           `json:"indices"`
}

type glbDocument struct {
	Accessors   []glbAccessor   `json:"accessors"`
	BufferViews []glbBufferView `json:"bufferViews"`
	Meshes      []struct {
		Primitives []glbPrimitive `json:"primitives"`
	} `json:"meshes"`
}

type glbVertexMesh struct {
	verts []float32
	tris  []int32
	pbr   []float32
}

func glbLayout(componentType int, accessorType string) (componentBytes, components int, err error) {
	switch componentType {
	case 5121:
		componentBytes = 1
	case 5123:
		componentBytes = 2
	case 5125, 5126:
		componentBytes = 4
	default:
		return 0, 0, fmt.Errorf("unsupported GLB component type %d", componentType)
	}
	switch accessorType {
	case "SCALAR":
		components = 1
	case "VEC2":
		components = 2
	case "VEC3":
		components = 3
	case "VEC4":
		components = 4
	default:
		return 0, 0, fmt.Errorf("unsupported GLB accessor type %q", accessorType)
	}
	return componentBytes, components, nil
}

func glbAccessorData(doc *glbDocument, binChunk []byte, index int) (glbAccessor, []byte, error) {
	if index < 0 || index >= len(doc.Accessors) {
		return glbAccessor{}, nil, fmt.Errorf("missing GLB accessor %d", index)
	}
	a := doc.Accessors[index]
	if a.BufferView < 0 || a.BufferView >= len(doc.BufferViews) {
		return glbAccessor{}, nil, fmt.Errorf("missing GLB buffer view %d", a.BufferView)
	}
	v := doc.BufferViews[a.BufferView]
	if v.Buffer != 0 || v.ByteStride != 0 {
		return glbAccessor{}, nil, fmt.Errorf("interleaved or external GLB buffers are unsupported")
	}
	componentBytes, components, err := glbLayout(a.ComponentType, a.Type)
	if err != nil {
		return glbAccessor{}, nil, err
	}
	if a.Count <= 0 || a.Count > math.MaxInt/(componentBytes*components) {
		return glbAccessor{}, nil, fmt.Errorf("invalid GLB accessor count %d", a.Count)
	}
	length := a.Count * componentBytes * components
	if v.ByteOffset < 0 || v.ByteLength < 0 || a.ByteOffset < 0 ||
		a.ByteOffset > v.ByteLength || length > v.ByteLength-a.ByteOffset ||
		length > len(binChunk) || v.ByteOffset > len(binChunk)-length-a.ByteOffset {
		return glbAccessor{}, nil, fmt.Errorf("GLB accessor %d is outside the BIN chunk", index)
	}
	start := v.ByteOffset + a.ByteOffset
	return a, binChunk[start : start+length], nil
}

// parseVertexGLB reads the dense vertex-PBR form emitted by trellis2.cpp. GLB
// coordinates and linear COLOR_0 values are converted back to the native
// trellis coordinate/material convention before CGAL remeshing and rebaking.
func parseVertexGLB(data []byte) (*glbVertexMesh, error) {
	if len(data) < 20 || binary.LittleEndian.Uint32(data[0:4]) != glbMagic {
		return nil, fmt.Errorf("input is not a GLB file")
	}
	if binary.LittleEndian.Uint32(data[4:8]) != 2 {
		return nil, fmt.Errorf("unsupported GLB version")
	}
	total := int(binary.LittleEndian.Uint32(data[8:12]))
	if total != len(data) {
		return nil, fmt.Errorf("invalid GLB length")
	}

	var jsonChunk, binChunk []byte
	for offset := 12; offset <= len(data)-8; {
		length := int(binary.LittleEndian.Uint32(data[offset : offset+4]))
		chunkType := binary.LittleEndian.Uint32(data[offset+4 : offset+8])
		start := offset + 8
		if length < 0 || start > len(data)-length {
			return nil, fmt.Errorf("invalid GLB chunk length")
		}
		switch chunkType {
		case glbJSONChunk:
			if jsonChunk == nil {
				jsonChunk = data[start : start+length]
			}
		case glbBINChunk:
			if binChunk == nil {
				binChunk = data[start : start+length]
			}
		}
		offset = start + length
	}
	if jsonChunk == nil || binChunk == nil {
		return nil, fmt.Errorf("GLB must contain JSON and BIN chunks")
	}

	var doc glbDocument
	if err := json.Unmarshal(jsonChunk, &doc); err != nil {
		return nil, fmt.Errorf("parsing GLB JSON: %w", err)
	}
	if len(doc.Meshes) != 1 || len(doc.Meshes[0].Primitives) != 1 {
		return nil, fmt.Errorf("GLB must contain one mesh primitive")
	}
	primitive := doc.Meshes[0].Primitives[0]
	positionIndex, ok := primitive.Attributes["POSITION"]
	if !ok {
		return nil, fmt.Errorf("GLB mesh has no POSITION attribute")
	}
	position, positionData, err := glbAccessorData(&doc, binChunk, positionIndex)
	if err != nil {
		return nil, err
	}
	if position.ComponentType != 5126 || position.Type != "VEC3" {
		return nil, fmt.Errorf("GLB POSITION must be float32 VEC3")
	}

	mesh := &glbVertexMesh{verts: make([]float32, position.Count*3)}
	for i := 0; i < position.Count; i++ {
		x := math.Float32frombits(binary.LittleEndian.Uint32(positionData[(i*3)*4:]))
		y := math.Float32frombits(binary.LittleEndian.Uint32(positionData[(i*3+1)*4:]))
		z := math.Float32frombits(binary.LittleEndian.Uint32(positionData[(i*3+2)*4:]))
		if math.IsNaN(float64(x)) || math.IsNaN(float64(y)) || math.IsNaN(float64(z)) ||
			math.IsInf(float64(x), 0) || math.IsInf(float64(y), 0) || math.IsInf(float64(z), 0) {
			return nil, fmt.Errorf("GLB POSITION contains a non-finite value")
		}
		mesh.verts[i*3] = x
		mesh.verts[i*3+1] = -z
		mesh.verts[i*3+2] = y
	}

	if primitive.Indices == nil {
		if position.Count%3 != 0 {
			return nil, fmt.Errorf("unindexed GLB vertex count is not divisible by three")
		}
		mesh.tris = make([]int32, position.Count)
		for i := range mesh.tris {
			mesh.tris[i] = int32(i)
		}
	} else {
		indices, indexData, err := glbAccessorData(&doc, binChunk, *primitive.Indices)
		if err != nil {
			return nil, err
		}
		if indices.Type != "SCALAR" || indices.Count%3 != 0 || (indices.ComponentType != 5123 && indices.ComponentType != 5125) {
			return nil, fmt.Errorf("GLB indices must be uint16/uint32 triangles")
		}
		mesh.tris = make([]int32, indices.Count)
		for i := range mesh.tris {
			var value uint32
			if indices.ComponentType == 5123 {
				value = uint32(binary.LittleEndian.Uint16(indexData[i*2:]))
			} else {
				value = binary.LittleEndian.Uint32(indexData[i*4:])
			}
			if value >= uint32(position.Count) || value > math.MaxInt32 {
				return nil, fmt.Errorf("GLB index %d is outside the vertex buffer", value)
			}
			mesh.tris[i] = int32(value)
		}
	}

	colorIndex, hasColor := primitive.Attributes["COLOR_0"]
	if !hasColor {
		return mesh, nil
	}
	color, colorData, err := glbAccessorData(&doc, binChunk, colorIndex)
	if err != nil {
		return nil, err
	}
	if color.ComponentType != 5123 || color.Type != "VEC4" || !color.Normalized || color.Count != position.Count {
		return nil, fmt.Errorf("GLB COLOR_0 must be normalized uint16 VEC4 aligned with POSITION")
	}
	metalRoughIndex, hasMetalRough := primitive.Attributes["_METALLIC_ROUGHNESS"]
	var metalRoughData []byte
	if hasMetalRough {
		metalRough, data, err := glbAccessorData(&doc, binChunk, metalRoughIndex)
		if err != nil {
			return nil, err
		}
		if metalRough.ComponentType != 5121 || metalRough.Type != "VEC2" || !metalRough.Normalized || metalRough.Count != position.Count {
			return nil, fmt.Errorf("GLB _METALLIC_ROUGHNESS must be normalized uint8 VEC2 aligned with POSITION")
		}
		metalRoughData = data
	}

	mesh.pbr = make([]float32, position.Count*6)
	for i := 0; i < position.Count; i++ {
		for channel := 0; channel < 3; channel++ {
			linear := float32(binary.LittleEndian.Uint16(colorData[(i*4+channel)*2:])) / 65535
			if linear <= 0.0031308 {
				mesh.pbr[i*6+channel] = linear * 12.92
			} else {
				mesh.pbr[i*6+channel] = 1.055*float32(math.Pow(float64(linear), 1.0/2.4)) - 0.055
			}
		}
		mesh.pbr[i*6+5] = float32(binary.LittleEndian.Uint16(colorData[(i*4+3)*2:])) / 65535
		mesh.pbr[i*6+4] = 0.6
		if hasMetalRough {
			mesh.pbr[i*6+3] = float32(metalRoughData[i*2]) / 255
			mesh.pbr[i*6+4] = float32(metalRoughData[i*2+1]) / 255
		}
	}
	return mesh, nil
}
