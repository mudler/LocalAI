package localaitools

import (
	"fmt"
	"math"
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
	maxVideoFrames   = 512 // frames of one reference video, as the video route
	maxVideoRefs     = 16  // references of one kind, as the video route
	maxVideoSide     = 4096
	maxVideoInputs   = 256 << 20 // all media of one video call together
	minSampleRate    = 8000
	maxSampleRate    = 192000
	maxPromptShapes  = 64 // points or boxes of one detect call
	maxRefImages     = 16 // the video route stops at 16 references of a kind; images use the same bound
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

// videoGeometry turns "WxH" into width and height.
func videoGeometry(size string) (w, h int, err error) {
	size = strings.TrimSpace(size)
	if size == "" {
		return 0, 0, nil
	}
	ws, hs, ok := strings.Cut(strings.ToLower(size), "x")
	w, e1 := strconv.Atoi(strings.TrimSpace(ws))
	h, e2 := strconv.Atoi(strings.TrimSpace(hs))
	if !ok || e1 != nil || e2 != nil || w < 1 || h < 1 || w > maxVideoSide || h > maxVideoSide {
		return 0, 0, fmt.Errorf("size must be WIDTHxHEIGHT, each from 1 to %d", maxVideoSide)
	}
	return w, h, nil
}

// videoFrames returns the fps and num_frames to send: the length the caller
// asked for in seconds, as a number of frames. Seconds need a frame rate to
// mean anything; 24 is used when the caller gives none.
func videoFrames(seconds string, fps int) (outFPS, frames int) {
	s := strings.TrimSpace(seconds)
	if s == "" {
		return fps, 0
	}
	if fps == 0 {
		fps = 24
	}
	v, _ := strconv.ParseFloat(s, 64) // checkVideoLength has validated it
	return fps, max(1, int(math.Round(v*float64(fps))))
}

// checkPromptNumbers checks a flat list of numbers that the detection route
// reads in groups: points as x,y,label triples, boxes as x1,y1,x2,y2 quads.
func checkPromptNumbers(name string, v []float32, group int, labelLast bool) error {
	if len(v) == 0 {
		return nil
	}
	if len(v)%group != 0 || len(v)/group > maxPromptShapes {
		return fmt.Errorf("%s: a flat list of groups of %d numbers, at most %d groups", name, group, maxPromptShapes)
	}
	for k, n := range v {
		f := float64(n)
		if math.IsNaN(f) || math.IsInf(f, 0) || n < 0 {
			return fmt.Errorf("%s: numbers must be finite and not negative", name)
		}
		if labelLast && k%group == group-1 && n != 0 && n != 1 {
			return fmt.Errorf("%s: the label of each point is 1 or 0", name)
		}
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
