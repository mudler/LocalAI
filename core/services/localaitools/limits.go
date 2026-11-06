package localaitools

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// Caps on what a model can ask of one client tool call. The tools run real
// models on shared hardware, so a number the model made up must not become an
// hour of GPU time or a request of hundreds of megabytes. The schema does not
// repeat them (every tool of the client group is paid on every turn); the
// error says the limit.
const (
	maxSteps         = 150
	maxVideoSeconds  = 30
	maxSoundSeconds  = 600
	maxFPS           = 60
	maxTopK          = 50
	maxRerankDocs    = 1000
	maxEmbedInputs   = 256
	maxTextBatchSize = 1 << 20 // total bytes of the texts of one embed or rerank call

	// maxHeavyCalls is how many image, video, sound and speech calls one user
	// may have running at once.
	maxHeavyCalls = 2
)

func checkSteps(steps int) error {
	if steps < 0 || steps > maxSteps {
		return fmt.Errorf("steps must be from 1 to %d", maxSteps)
	}
	return nil
}

func checkTopK(k int) error {
	if k < 0 || k > maxTopK {
		return fmt.Errorf("top_k must be from 1 to %d", maxTopK)
	}
	return nil
}

func checkVideoLength(seconds string, fps int) error {
	if s := strings.TrimSpace(seconds); s != "" {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil || v <= 0 || v > maxVideoSeconds {
			return fmt.Errorf("seconds must be a number from 1 to %d", maxVideoSeconds)
		}
	}
	if fps < 0 || fps > maxFPS {
		return fmt.Errorf("fps must be from 1 to %d", maxFPS)
	}
	return nil
}

func checkSoundLength(seconds *float32) error {
	if seconds != nil && (*seconds <= 0 || *seconds > maxSoundSeconds) {
		return fmt.Errorf("duration_seconds must be from 1 to %d", maxSoundSeconds)
	}
	return nil
}

func checkTexts(what string, texts []string, maxCount int) error {
	if len(texts) > maxCount {
		return fmt.Errorf("%s: at most %d items per call, got %d", what, maxCount, len(texts))
	}
	total := 0
	for _, t := range texts {
		total += len(t)
	}
	if total > maxTextBatchSize {
		return fmt.Errorf("%s: the texts weigh %d bytes together; the limit is %d. Send fewer or shorter texts", what, total, maxTextBatchSize)
	}
	return nil
}

// errBusy is the answer to a call over the limit of heavy calls.
var errBusy = fmt.Errorf("busy: this account already has %d media calls running. Try again when one has finished", maxHeavyCalls)

// heavyCalls counts the heavy calls of every user of this process. Both
// clients build their Inference anew for each call, so it cannot hold the
// count.
var heavyCalls = &heavyGate{}

// heavyGate counts the heavy calls of each user. Its zero value is ready.
type heavyGate struct {
	mu      sync.Mutex
	running map[string]int
}

// enter takes a place for the user, or returns errBusy. The returned function
// gives the place back; call it once.
func (g *heavyGate) enter(user string) (func(), error) {
	if g == nil {
		return func() {}, nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.running == nil {
		g.running = map[string]int{}
	}
	if g.running[user] >= maxHeavyCalls {
		return nil, errBusy
	}
	g.running[user]++
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			defer g.mu.Unlock()
			if g.running[user]--; g.running[user] <= 0 {
				delete(g.running, user)
			}
		})
	}, nil
}
