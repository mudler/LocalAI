// SPDX-License-Identifier: MIT
package config

import "testing"

func TestUpscaleScaleFromOptions(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options []string
		want    int
	}{
		{"absent", nil, 0},
		{"unrelated", []string{"backend:CPU", "upscale_tile_size:128"}, 0},
		{"valid", []string{"upscale_scale:4"}, 4},
		{"malformed", []string{"upscale_scale:banana"}, 0},
		{"missing value", []string{"upscale_scale"}, 0},
		{"empty", []string{"upscale_scale:"}, 0},
		{"zero", []string{"upscale_scale:0"}, 0},
		{"negative", []string{"upscale_scale:-1"}, 0},
		{"overflow", []string{"upscale_scale:2147483648"}, 0},
		{"huge", []string{"upscale_scale:999999999999999999999999999"}, 0},
		{"duplicate", []string{"upscale_scale:4", "upscale_scale:4"}, 0},
		{"invalid duplicate", []string{"upscale_scale:4", "upscale_scale:bad"}, 0},
		{"extra separator", []string{"upscale_scale:4:2"}, 0},
		{"whitespace", []string{"upscale_scale: 4"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := ModelConfig{Options: tc.options}
			if got := UpscaleScaleFromOptions(cfg); got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}
