// SPDX-License-Identifier: MIT
package config

import (
	"strconv"
	"strings"
)

// UpscaleScaleFromOptions reports the declared native scale for client metadata.
// Match the backend's int32 range and reject duplicates rather than advertising
// a value that the backend will reject. This does not replace backend validation.
func UpscaleScaleFromOptions(c ModelConfig) int {
	var scale int
	seen := false
	for _, option := range c.Options {
		key, value, _ := strings.Cut(option, ":")
		if key != "upscale_scale" {
			continue
		}
		if seen {
			return 0
		}
		seen = true
		n, err := strconv.ParseInt(value, 10, 32)
		if err != nil || n <= 0 {
			return 0
		}
		scale = int(n)
	}
	return scale
}
