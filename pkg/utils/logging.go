package utils

import (
	"time"

	"github.com/rs/zerolog/log"
)

var lastProgress time.Time = time.Now()
var startTime time.Time = time.Now()

func ResetDownloadTimers() {
	lastProgress = time.Now()
	startTime = time.Now()
}

func DisplayDownloadFunction(fileName string, current string, total string, percentage float64) {
	currentTime := time.Now()

	if currentTime.Sub(lastProgress) >= 5*time.Second {

		lastProgress = currentTime

		// calculate ETA based on percentage and elapsed time
		var eta time.Duration
		if percentage > 0 {
			elapsed := currentTime.Sub(startTime)
			eta = time.Duration(float64(elapsed)*(100/percentage) - float64(elapsed))
		}

		if total != "" {
			log.Info().Msgf("Downloading %s: %s/%s (%.2f%%) ETA: %s", fileName, current, total, percentage, eta)
		} else {
			log.Info().Msgf("Downloading: %s", current)
		}
	}
}
