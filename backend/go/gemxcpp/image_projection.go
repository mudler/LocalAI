// SPDX-License-Identifier: MIT
package main

import (
	"encoding/json"
	"fmt"
	"math"
)

type poseProjection struct {
	joints        []int
	width, height uint32
}

func projectionForProfile(definition map[string]json.RawMessage, profile string) (poseProjection, error) {
	var result poseProjection

	if profile == "soma77" {
		result.joints = make([]int, 77)
		for i := range result.joints {
			result.joints[i] = i
		}
		return result, nil
	}

	var smpl struct {
		Mapping []int `json:"mapping"`
	}

	if err := json.Unmarshal(definition["smpl24"], &smpl); err != nil {
		return result, err
	}
	if len(smpl.Mapping) != 24 {
		return result, fmt.Errorf("invalid SMPL image projection mapping")
	}
	for _, joint := range smpl.Mapping {
		if joint < 0 || joint >= 77 {
			return result, fmt.Errorf("image projection joint outside SOMA topology")
		}
	}

	result.joints = smpl.Mapping
	return result, nil
}

func (p poseProjection) project(camera, translation []float32) []float32 {
	if len(camera) != 231 || len(translation) != 3 || p.width == 0 || p.height == 0 {
		return nil
	}
	// gemx_live_submit documents and uses these exact source-frame intrinsics.
	focal := float64(max(p.width, p.height))
	result := make([]float32, 0, len(p.joints)*2)

	for _, joint := range p.joints {
		x := float64(camera[joint*3]) + float64(translation[0])
		y := float64(camera[joint*3+1]) + float64(translation[1])
		z := float64(camera[joint*3+2]) + float64(translation[2])
		if z <= 0 {
			return nil
		}
		for _, pixel := range []float64{focal*x/z + float64(p.width)/2, focal*y/z + float64(p.height)/2} {
			if math.IsNaN(pixel) || math.IsInf(pixel, 0) || math.Abs(pixel) > math.MaxFloat32 {
				return nil
			}
			result = append(result, float32(pixel))
		}
	}
	return result
}
