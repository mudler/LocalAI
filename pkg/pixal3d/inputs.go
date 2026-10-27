// SPDX-License-Identifier: MIT
// Package pixal3d defines the canonical multiview contract shared by HTTP and
// native backend entry points.
package pixal3d

import (
	"bytes"
	"fmt"
	"image/png"
	"math"
	"strings"
)

const MaxInputBytes = 32 << 20

func ValidateRequest(single string, views []string, scale float64, quality, background string) error {
	if single != "" || len(views) != 4 {
		return fmt.Errorf("Pixal3D requires exactly four images in front/right/back/left order and no image field")
	}
	for _, v := range views {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("Pixal3D images must not be empty")
		}
	}
	if scale <= 0 || math.IsNaN(scale) || math.IsInf(scale, 0) || float32(scale) == 0 || math.IsInf(float64(float32(scale)), 0) {
		return fmt.Errorf("mesh_scale must be explicitly set to a finite positive number")
	}
	if quality != "" && quality != "1024" {
		return fmt.Errorf("Pixal3D supports only quality 1024")
	}
	if background != "" && background != "keep" {
		return fmt.Errorf("Pixal3D requires pre-matted RGBA PNG images; background must be keep")
	}
	return nil
}

func ValidatePNG(data []byte) error {
	// Checking IHDR before decoding bounds memory use even for compressed bombs.
	if len(data) > MaxInputBytes || len(data) < 33 || !bytes.Equal(data[:8], []byte("\x89PNG\r\n\x1a\n")) || string(data[12:16]) != "IHDR" || data[24] != 8 || data[25] != 6 {
		return fmt.Errorf("each Pixal3D view must be an 8-bit RGBA PNG of at most 32 MiB")
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("invalid PNG: %w", err)
	}
	if cfg.Width > 4096 || cfg.Height > 4096 {
		return fmt.Errorf("Pixal3D views must be at most 4096 by 4096 pixels")
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		return fmt.Errorf("invalid PNG: %w", err)
	}
	return nil
}
