// SPDX-License-Identifier: MIT
package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
)

// Keep quality statistics with the downloadable asset, not just transient logs.
func withRemeshStats(glb []byte, stats string) ([]byte, error) {
	if len(glb) < 28 || !json.Valid([]byte(stats)) {
		return nil, fmt.Errorf("invalid remeshing output")
	}
	size := int(binary.LittleEndian.Uint32(glb[12:]))
	if size < 2 || size > len(glb)-28 {
		return nil, fmt.Errorf("invalid remeshing JSON chunk")
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(glb[20:20+size], &root); err != nil {
		return nil, err
	}
	var extras map[string]json.RawMessage
	if raw := root["extras"]; raw != nil {
		if err := json.Unmarshal(raw, &extras); err != nil {
			return nil, err
		}
	}
	if extras == nil {
		extras = map[string]json.RawMessage{}
	}
	extras["localai_remesh"] = json.RawMessage(stats)
	root["extras"], _ = json.Marshal(extras)
	text, err := json.Marshal(root)
	if err != nil {
		return nil, err
	}
	for len(text)%4 != 0 {
		text = append(text, ' ')
	}
	length := 20 + len(text) + len(glb) - (20 + size)
	if length > 512<<20 {
		return nil, fmt.Errorf("remeshed GLB exceeds 512 MiB")
	}
	output := make([]byte, 20, length)
	copy(output, glb[:20])
	binary.LittleEndian.PutUint32(output[8:], uint32(length))
	binary.LittleEndian.PutUint32(output[12:], uint32(len(text)))
	output = append(output, text...)
	output = append(output, glb[20+size:]...)
	return output, nil
}
