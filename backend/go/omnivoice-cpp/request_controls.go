// SPDX-License-Identifier: MIT
package main

import (
	"fmt"
	"math"
	"strconv"
)

type requestControls struct {
	durationFrames int32
	postprocess    int32
}

func parseRequestControls(params map[string]string, streaming bool) (requestControls, error) {
	controls := requestControls{postprocess: -1}
	if value, present := params["duration"]; present {
		seconds, err := strconv.ParseFloat(value, 64)
		// Forced duration bypasses chunking. This cap is wrapper resource policy,
		// not an engine positional limit or an allocation/success guarantee.
		if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 || seconds > 30 {
			return controls, fmt.Errorf("omnivoice: duration must be finite seconds greater than 0 and at most 30")
		}
		frames := math.Max(1, math.Floor(seconds*25)) // bundled codec: 24000 / 960
		if frames < 1 || frames > 750 {
			return controls, fmt.Errorf("omnivoice: duration frame count out of range")
		}
		controls.durationFrames = int32(frames)
	}
	if value, present := params["postprocess"]; present {
		switch value {
		case "true":
			controls.postprocess = 1
		case "false":
			controls.postprocess = 0
		default:
			return controls, fmt.Errorf("omnivoice: postprocess must be exactly true or false")
		}
	}
	if streaming && controls.postprocess == 0 {
		return controls, fmt.Errorf("omnivoice: streaming does not support postprocess=false")
	}
	return controls, nil
}
