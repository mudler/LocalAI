package localai

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v4"
	"github.com/mudler/LocalAI/core/schema"
)

// ttsSpeedMin and ttsSpeedMax mirror the range the OpenAI Speech API documents
// for the `speed` field.
const (
	ttsSpeedMin = 0.25
	ttsSpeedMax = 4.0
)

// applyTTSSpeed normalises the OpenAI `speed` field onto the backend-specific
// params map, which is what actually reaches the gRPC TTSRequest. Without this
// the field is parsed off the request and then dropped, so a call with
// speed=0.8 returns HTTP 200 with an unchanged playback rate instead of either
// honouring or rejecting it (#11097).
//
// An explicit params["speed"] wins so the LocalAI-native extension keeps
// precedence over the OpenAI-compatible field. Backends whose model exposes no
// rate control ignore the param, exactly like they ignore `instructions`.
func applyTTSSpeed(input *schema.TTSRequest) error {
	if input.Speed == 0 {
		return nil
	}
	if input.Speed < ttsSpeedMin || input.Speed > ttsSpeedMax {
		return echo.NewHTTPError(http.StatusBadRequest,
			fmt.Sprintf("speed must be between %g and %g", ttsSpeedMin, ttsSpeedMax))
	}
	if input.Params == nil {
		input.Params = make(map[string]string)
	}
	if _, ok := input.Params["speed"]; !ok {
		input.Params["speed"] = strconv.FormatFloat(float64(input.Speed), 'g', -1, 32)
	}
	return nil
}
