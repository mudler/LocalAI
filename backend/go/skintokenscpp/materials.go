// SPDX-License-Identifier: MIT
package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
)

const maxGLBBytes = 512 << 20

type glbObject map[string]json.RawMessage
type glbDocument struct {
	root glbObject
	bin  []byte
}

func glbField[T any](o glbObject, key string) (value T, err error) {
	data, ok := o[key]
	if !ok {
		return value, fmt.Errorf("GLB is missing %s", key)
	}
	err = json.Unmarshal(data, &value)
	return
}

func glbSet(o glbObject, key string, value any) { o[key], _ = json.Marshal(value) }

func readGLB(path string) (*glbDocument, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxGLBBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) < 28 || len(data) > maxGLBBytes || string(data[:4]) != "glTF" || binary.LittleEndian.Uint32(data[4:]) != 2 || uint64(binary.LittleEndian.Uint32(data[8:])) != uint64(len(data)) {
		return nil, fmt.Errorf("expected a complete GLB 2.0 file of at most 512 MiB")
	}
	d := &glbDocument{}
	for offset := 12; offset < len(data); {
		if len(data)-offset < 8 {
			return nil, fmt.Errorf("truncated GLB chunk")
		}
		size := uint64(binary.LittleEndian.Uint32(data[offset:]))
		typ := string(data[offset+4 : offset+8])
		offset += 8
		if size%4 != 0 || size > uint64(len(data)-offset) {
			return nil, fmt.Errorf("invalid GLB chunk length")
		}
		chunk := data[offset : offset+int(size)]
		switch typ {
		case "JSON":
			if offset != 20 || d.root != nil || size > 32<<20 {
				return nil, fmt.Errorf("invalid GLB JSON chunk")
			}
			if err := json.Unmarshal(chunk, &d.root); err != nil {
				return nil, err
			}
		case "BIN\x00":
			if d.root == nil || d.bin != nil {
				return nil, fmt.Errorf("invalid GLB binary chunk")
			}
			d.bin = chunk
		default:
			return nil, fmt.Errorf("unsupported GLB chunk")
		}
		offset += int(size)
	}
	buffers, err := glbField[[]glbObject](d.root, "buffers")
	if err != nil || len(buffers) != 1 || buffers[0]["uri"] != nil || d.bin == nil {
		return nil, fmt.Errorf("GLB must have one embedded buffer")
	}
	length, err := glbField[int](buffers[0], "byteLength")
	if err != nil || length < 0 || length > len(d.bin) || len(d.bin)-length > 3 {
		return nil, fmt.Errorf("invalid GLB buffer length")
	}
	return d, nil
}

func (d *glbDocument) write(path string) error {
	glbSet(d.root, "buffers", []any{map[string]int{"byteLength": len(d.bin)}})
	text, err := json.Marshal(d.root)
	if err != nil {
		return err
	}
	for len(text)%4 != 0 {
		text = append(text, ' ')
	}
	for len(d.bin)%4 != 0 {
		d.bin = append(d.bin, 0)
	}
	size := 28 + len(text) + len(d.bin)
	if size > maxGLBBytes {
		return fmt.Errorf("preserved GLB exceeds 512 MiB")
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	var header bytes.Buffer
	for _, v := range []uint32{0x46546c67, 2, uint32(size), uint32(len(text)), 0x4e4f534a} {
		_ = binary.Write(&header, binary.LittleEndian, v)
	}
	if _, err = f.Write(header.Bytes()); err != nil {
		return err
	}
	if _, err = f.Write(text); err != nil {
		return err
	}
	header.Reset()
	for _, v := range []uint32{uint32(len(d.bin)), 0x004e4942} {
		_ = binary.Write(&header, binary.LittleEndian, v)
	}
	if _, err = f.Write(header.Bytes()); err != nil {
		return err
	}
	if _, err = f.Write(d.bin); err != nil {
		return err
	}
	return f.Close()
}

func singlePrimitive(d *glbDocument) (glbObject, []glbObject, error) {
	meshes, err := glbField[[]glbObject](d.root, "meshes")
	if err != nil || len(meshes) != 1 {
		return nil, nil, fmt.Errorf("material-preserving rigging currently requires one mesh")
	}
	primitives, err := glbField[[]glbObject](meshes[0], "primitives")
	if err != nil || len(primitives) != 1 {
		return nil, nil, fmt.Errorf("material-preserving rigging currently requires one mesh primitive")
	}
	p := primitives[0]
	if mode, ok := p["mode"]; ok && string(mode) != "4" {
		return nil, nil, fmt.Errorf("only triangle primitives can be rigged")
	}
	if p["extensions"] != nil || p["targets"] != nil {
		return nil, nil, fmt.Errorf("compressed geometry, primitive extensions and morph targets are not supported for rigging")
	}
	nodes, err := glbField[[]glbObject](d.root, "nodes")
	if err != nil {
		return nil, nil, err
	}
	instances := 0
	for _, node := range nodes {
		if node["mesh"] != nil {
			instances++
		}
	}
	if instances != 1 {
		return nil, nil, fmt.Errorf("material-preserving rigging requires one mesh instance")
	}
	return p, meshes, nil
}

func triangleIndices(d *glbDocument, primitive glbObject) ([]uint32, error) {
	accessors, err := glbField[[]glbObject](d.root, "accessors")
	if err != nil {
		return nil, err
	}
	index, err := glbField[int](primitive, "indices")
	if err != nil || index < 0 || index >= len(accessors) {
		return nil, fmt.Errorf("invalid triangle indices")
	}
	a := accessors[index]
	count, err := glbField[int](a, "count")
	if err != nil || count <= 0 || count%3 != 0 || count > maxGLBBytes/4 || a["sparse"] != nil {
		return nil, fmt.Errorf("invalid triangle count")
	}
	typ, err := glbField[string](a, "type")
	if err != nil || typ != "SCALAR" {
		return nil, fmt.Errorf("invalid index accessor type")
	}
	component, err := glbField[int](a, "componentType")
	width := map[int]int{5121: 1, 5123: 2, 5125: 4}[component]
	if err != nil || width == 0 {
		return nil, fmt.Errorf("invalid index component type")
	}
	views, err := glbField[[]glbObject](d.root, "bufferViews")
	if err != nil {
		return nil, err
	}
	viewIndex, err := glbField[int](a, "bufferView")
	if err != nil || viewIndex < 0 || viewIndex >= len(views) {
		return nil, fmt.Errorf("invalid index buffer view")
	}
	view := views[viewIndex]
	buffer, err := glbField[int](view, "buffer")
	if err != nil || buffer != 0 || view["extensions"] != nil || view["byteStride"] != nil {
		return nil, fmt.Errorf("unsupported index buffer")
	}
	length, err := glbField[int](view, "byteLength")
	if err != nil || length < 0 || length > len(d.bin) {
		return nil, fmt.Errorf("invalid index buffer length")
	}
	offset, local := 0, 0
	if view["byteOffset"] != nil {
		offset, err = glbField[int](view, "byteOffset")
		if err != nil {
			return nil, err
		}
	}
	if a["byteOffset"] != nil {
		local, err = glbField[int](a, "byteOffset")
		if err != nil {
			return nil, err
		}
	}
	if offset < 0 || offset > len(d.bin)-length || local < 0 || local > length || count > (length-local)/width {
		return nil, fmt.Errorf("index accessor exceeds buffer")
	}
	result := make([]uint32, count)
	for i := range result {
		data := d.bin[offset+local+i*width:]
		switch width {
		case 1:
			result[i] = uint32(data[0])
		case 2:
			result[i] = uint32(binary.LittleEndian.Uint16(data))
		case 4:
			result[i] = binary.LittleEndian.Uint32(data)
		}
	}
	return result, nil
}

// SkinTokens preserves vertex order while baking node transforms. Restore the
// original UVs, vertex colours and PBR resources, not the exporter's flattened
// colour/material factors. Never apply this to a topology-changing operation.
func preserveMaterials(source, generated string) error {
	src, err := readGLB(source)
	if err != nil {
		return err
	}
	dst, err := readGLB(generated)
	if err != nil {
		return err
	}
	sp, _, err := singlePrimitive(src)
	if err != nil {
		return err
	}
	dp, meshes, err := singlePrimitive(dst)
	if err != nil {
		return err
	}
	sa, err := glbField[map[string]int](sp, "attributes")
	if err != nil {
		return err
	}
	da, err := glbField[map[string]int](dp, "attributes")
	if err != nil {
		return err
	}
	saccess, err := glbField[[]glbObject](src.root, "accessors")
	if err != nil {
		return err
	}
	daccess, err := glbField[[]glbObject](dst.root, "accessors")
	if err != nil {
		return err
	}
	count := func(access []glbObject, attrs map[string]int) (int, error) {
		index, ok := attrs["POSITION"]
		if !ok || index < 0 || index >= len(access) {
			return 0, fmt.Errorf("invalid POSITION accessor")
		}
		return glbField[int](access[index], "count")
	}
	scount, err := count(saccess, sa)
	if err != nil {
		return err
	}
	dcount, err := count(daccess, da)
	if err != nil || scount <= 0 || scount != dcount {
		return fmt.Errorf("rigging changed vertex count; refusing unsafe material transfer")
	}
	si, err := triangleIndices(src, sp)
	if err != nil {
		return err
	}
	di, err := triangleIndices(dst, dp)
	if err != nil {
		return err
	}
	if len(si) != len(di) {
		return fmt.Errorf("rigging changed topology; refusing material transfer")
	}
	for i := 0; i < len(si); i += 3 {
		// A mirrored node transform reverses winding without changing vertex order.
		slices.Sort(si[i : i+3])
		slices.Sort(di[i : i+3])
		if !slices.Equal(si[i:i+3], di[i:i+3]) || uint64(si[i+2]) >= uint64(scount) {
			return fmt.Errorf("rigging changed topology; refusing material transfer")
		}
	}
	sviews, err := glbField[[]glbObject](src.root, "bufferViews")
	if err != nil {
		return err
	}
	dviews, err := glbField[[]glbObject](dst.root, "bufferViews")
	if err != nil {
		return err
	}
	copiedViews := map[int]int{}
	copyView := func(index int) (int, error) {
		if result, ok := copiedViews[index]; ok {
			return result, nil
		}
		if index < 0 || index >= len(sviews) {
			return 0, fmt.Errorf("invalid source buffer view")
		}
		view := sviews[index]
		buffer, err := glbField[int](view, "buffer")
		if err != nil || buffer != 0 || view["extensions"] != nil {
			return 0, fmt.Errorf("unsupported source buffer view")
		}
		offset := 0
		if view["byteOffset"] != nil {
			offset, err = glbField[int](view, "byteOffset")
			if err != nil {
				return 0, err
			}
		}
		length, err := glbField[int](view, "byteLength")
		if err != nil || offset < 0 || length < 0 || offset > len(src.bin) || length > len(src.bin)-offset {
			return 0, fmt.Errorf("source buffer view is out of bounds")
		}
		for len(dst.bin)%4 != 0 {
			dst.bin = append(dst.bin, 0)
		}
		if length > maxGLBBytes-len(dst.bin) {
			return 0, fmt.Errorf("combined GLB exceeds 512 MiB")
		}
		glbSet(view, "byteOffset", len(dst.bin))
		dst.bin = append(dst.bin, src.bin[offset:offset+length]...)
		result := len(dviews)
		dviews = append(dviews, view)
		copiedViews[index] = result
		return result, nil
	}
	copiedAccessors := map[int]int{}
	copyAccessor := func(index int) (int, error) {
		if result, ok := copiedAccessors[index]; ok {
			return result, nil
		}
		accessor := saccess[index]
		view, err := glbField[int](accessor, "bufferView")
		if err != nil || view < 0 || view >= len(sviews) || accessor["sparse"] != nil {
			return 0, fmt.Errorf("sparse or invalid source accessor is not supported")
		}
		mapped, err := copyView(view)
		if err != nil {
			return 0, err
		}
		glbSet(accessor, "bufferView", mapped)
		result := len(daccess)
		daccess = append(daccess, accessor)
		copiedAccessors[index] = result
		return result, nil
	}
	for _, name := range slices.Sorted(maps.Keys(sa)) {
		index := sa[name]
		if index < 0 || index >= len(saccess) {
			return fmt.Errorf("invalid source attribute")
		}
		if name == "COLOR_0" || strings.HasPrefix(name, "TEXCOORD_") || strings.HasPrefix(name, "_") {
			attributeCount, err := glbField[int](saccess[index], "count")
			if err != nil || attributeCount != scount {
				return fmt.Errorf("source attribute count differs from vertices")
			}
			mapped, err := copyAccessor(index)
			if err != nil {
				return err
			}
			da[name] = mapped
		}
	}
	if _, ok := sa["COLOR_0"]; !ok {
		delete(da, "COLOR_0")
	}
	// Original tangents are in pre-transform space; glTF renderers derive them
	// from the preserved UVs when a normal map is present.
	delete(da, "TANGENT")
	glbSet(dp, "attributes", da)
	if material, ok := sp["material"]; ok {
		dp["material"] = material
	} else {
		delete(dp, "material")
	}
	glbSet(meshes[0], "primitives", []glbObject{dp})
	glbSet(dst.root, "meshes", meshes)
	for _, key := range []string{"materials", "textures", "samplers", "extensionsUsed", "extensionsRequired"} {
		if value, ok := src.root[key]; ok {
			dst.root[key] = value
		} else {
			delete(dst.root, key)
		}
	}
	if src.root["extensions"] != nil {
		return fmt.Errorf("root glTF extensions require explicit preservation support")
	}
	if src.root["images"] != nil {
		images, err := glbField[[]glbObject](src.root, "images")
		if err != nil {
			return err
		}
		for _, image := range images {
			if image["bufferView"] != nil {
				index, err := glbField[int](image, "bufferView")
				if err != nil || index < 0 || index >= len(sviews) {
					return fmt.Errorf("invalid image buffer view")
				}
				mapped, err := copyView(index)
				if err != nil {
					return err
				}
				glbSet(image, "bufferView", mapped)
			} else {
				uri, err := glbField[string](image, "uri")
				if err != nil || !strings.HasPrefix(uri, "data:") {
					return fmt.Errorf("textures must be embedded, not external files or URLs")
				}
			}
		}
		glbSet(dst.root, "images", images)
	} else {
		delete(dst.root, "images")
	}
	glbSet(dst.root, "accessors", daccess)
	glbSet(dst.root, "bufferViews", dviews)
	return dst.write(generated)
}
