package main

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// Speaker labels on transcripts. With a diarization_model companion attached
// to an ASR model, unary transcription tags each segment (and, with word
// timestamps, each word) with its speaker, and the stream=true final result
// tags each utterance. Live transcription carries speakers through the scene
// stream instead (scene.go).

// speakerSnapSeconds mirrors parakeet.cpp's merge_asr_diarization: a word that
// overlaps no speaker segment takes the nearest segment's speaker when that
// segment is this close. ASR word boundaries and diarization boundaries can
// disagree by a frame or two; a word farther than this has no speaker.
const speakerSnapSeconds = 0.5

// transcriptSpeaker renders a 0-based speaker for a transcript segment or
// word; -1 (no speaker) is left empty so the field is omitted.
func transcriptSpeaker(spk int) string {
	if spk < 0 {
		return ""
	}
	return strconv.Itoa(spk)
}

// wantSpeakers reports whether a transcription should carry speaker labels:
// a diarization companion is attached, the library can diarize, and the
// request did not turn it off (the OpenAI endpoint sends diarize=true unless
// the client passes diarize=false).
func (p *ParakeetCpp) wantSpeakers(diarize bool) bool {
	return diarize && p.ctxPtr != 0 && p.diarCtx != 0 && CppDiarizePCM != nil
}

// diarizeSegmentsPCM runs the diarization companion over 16 kHz PCM
// (parakeet_capi_diarize_pcm, the checkpoint's own mode, as NeMo's
// diarize()) and returns its segments.
func (p *ParakeetCpp) diarizeSegmentsPCM(pcm []float32) ([]diarizeSegmentJSON, error) {
	if len(pcm) == 0 {
		return nil, nil
	}
	raw, err := p.diarizeCall(pcm, false)
	if err != nil {
		return nil, err
	}
	var doc diarizePCMDoc
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, fmt.Errorf("parakeet-cpp: decode diarization json: %w", err)
	}
	return doc.Segments, nil
}

// assignSpeakers gives each word the speaker whose segments overlap it most,
// falling back to the nearest segment within speakerSnapSeconds; -1 when none.
// Same rule as parakeet.cpp's merge_asr_diarization, so the labels match the
// library's own speaker-attributed ASR.
func assignSpeakers(words []transcriptWord, segs []diarizeSegmentJSON) []int {
	out := make([]int, len(words))
	for i, w := range words {
		best, bestOverlap := -1, 0.0
		for _, s := range segs {
			if ov := min(w.End, s.End) - max(w.Start, s.Start); ov > bestOverlap {
				best, bestOverlap = s.Speaker, ov
			}
		}
		if best < 0 {
			bestDist := speakerSnapSeconds
			for _, s := range segs {
				dist := s.Start - w.End
				if s.End <= w.Start {
					dist = w.Start - s.End
				}
				if dist >= 0 && dist <= bestDist {
					best, bestDist = s.Speaker, dist
				}
			}
		}
		out[i] = best
	}
	return out
}

// splitAtSpeakerChanges splits each word group wherever the speaker changes,
// so every segment has one speaker. speakers is indexed like the
// concatenation of groups. Returns the new groups and each group's speaker.
func splitAtSpeakerChanges(groups [][]transcriptWord, speakers []int) ([][]transcriptWord, []int) {
	var outGroups [][]transcriptWord
	var outSpk []int
	k := 0
	for _, g := range groups {
		start := 0
		for i := 1; i <= len(g); i++ {
			if i == len(g) || speakers[k+i] != speakers[k+start] {
				outGroups = append(outGroups, g[start:i])
				outSpk = append(outSpk, speakers[k+start])
				start = i
			}
		}
		k += len(g)
	}
	return outGroups, outSpk
}

// majoritySpeaker is the speaker covering most of the words' duration, or -1.
func majoritySpeaker(words []transcriptWord, speakers []int) int {
	dur := map[int]float64{}
	best, bestDur := -1, 0.0
	for i, w := range words {
		if speakers[i] < 0 {
			continue
		}
		dur[speakers[i]] += max(w.End-w.Start, 1e-3)
		if dur[speakers[i]] > bestDur {
			best, bestDur = speakers[i], dur[speakers[i]]
		}
	}
	return best
}
