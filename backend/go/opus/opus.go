package main

import (
	"fmt"
	"sync"
	"time"

	"github.com/mudler/LocalAI/pkg/grpc/base"
	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/LocalAI/pkg/sound"
)

const (
	opusSampleRate    = 48000
	opusChannels      = 1
	opusFrameSize     = 960 // 20ms at 48kHz
	opusMaxPacketSize = 4000
	opusMaxFrameSize  = 5760 // 120ms at 48kHz

	decoderIdleTTL   = 60 * time.Second
	decoderEvictTick = 30 * time.Second
)

type cachedDecoder struct {
	mu       sync.Mutex
	dec      *Decoder
	lastUsed time.Time
}

type Opus struct {
	base.Base

	decodersMu sync.Mutex
	decoders   map[string]*cachedDecoder
}

func (o *Opus) Load(opts *pb.ModelOptions) error {
	o.decoders = make(map[string]*cachedDecoder)
	go o.evictLoop()
	return Init()
}

func (o *Opus) evictLoop() {
	ticker := time.NewTicker(decoderEvictTick)
	defer ticker.Stop()
	for range ticker.C {
		o.decodersMu.Lock()
		now := time.Now()
		for id, cd := range o.decoders {
			if now.Sub(cd.lastUsed) > decoderIdleTTL {
				cd.dec.Close()
				delete(o.decoders, id)
			}
		}
		o.decodersMu.Unlock()
	}
}

// getOrCreateDecoder returns a cached decoder for the given session ID,
// creating one if it doesn't exist yet.
func (o *Opus) getOrCreateDecoder(sessionID string) (*cachedDecoder, error) {
	o.decodersMu.Lock()
	defer o.decodersMu.Unlock()

	if cd, ok := o.decoders[sessionID]; ok {
		cd.lastUsed = time.Now()
		return cd, nil
	}

	dec, err := NewDecoder(opusSampleRate, opusChannels)
	if err != nil {
		return nil, err
	}
	cd := &cachedDecoder{dec: dec, lastUsed: time.Now()}
	o.decoders[sessionID] = cd
	return cd, nil
}

func (o *Opus) AudioEncode(req *pb.AudioEncodeRequest) (*pb.AudioEncodeResult, error) {
	enc, err := NewEncoder(opusSampleRate, opusChannels, ApplicationAudio)
	if err != nil {
		return nil, fmt.Errorf("opus encoder create: %w", err)
	}
	defer enc.Close()

	if err := enc.SetBitrate(64000); err != nil {
		return nil, fmt.Errorf("opus set bitrate: %w", err)
	}
	if err := enc.SetComplexity(10); err != nil {
		return nil, fmt.Errorf("opus set complexity: %w", err)
	}

	samples := sound.BytesToInt16sLE(req.PcmData)
	if len(samples) == 0 {
		return &pb.AudioEncodeResult{
			SampleRate:      opusSampleRate,
			SamplesPerFrame: opusFrameSize,
		}, nil
	}

	if req.SampleRate != 0 && int(req.SampleRate) != opusSampleRate {
		samples = sound.ResampleInt16(samples, int(req.SampleRate), opusSampleRate)
	}

	var frames [][]byte
	packet := make([]byte, opusMaxPacketSize)

	for offset := 0; offset+opusFrameSize <= len(samples); offset += opusFrameSize {
		frame := samples[offset : offset+opusFrameSize]
		n, err := enc.Encode(frame, opusFrameSize, packet)
		if err != nil {
			return nil, fmt.Errorf("opus encode: %w", err)
		}
		out := make([]byte, n)
		copy(out, packet[:n])
		frames = append(frames, out)
	}

	return &pb.AudioEncodeResult{
		Frames:          frames,
		SampleRate:      opusSampleRate,
		SamplesPerFrame: opusFrameSize,
	}, nil
}

func (o *Opus) AudioDecode(req *pb.AudioDecodeRequest) (*pb.AudioDecodeResult, error) {
	if len(req.Frames) == 0 {
		return &pb.AudioDecodeResult{
			SampleRate:      opusSampleRate,
			SamplesPerFrame: opusFrameSize,
		}, nil
	}

	// Use a persistent decoder when a session ID is provided so that Opus
	// prediction state carries across batches. Fall back to a fresh decoder
	// for backward compatibility.
	sessionID := req.Options["session_id"]

	var cd *cachedDecoder
	var ownedDec *Decoder

	if sessionID != "" && o.decoders != nil {
		var err error
		cd, err = o.getOrCreateDecoder(sessionID)
		if err != nil {
			return nil, fmt.Errorf("opus decoder create: %w", err)
		}
		cd.mu.Lock()
		defer cd.mu.Unlock()
	} else {
		dec, err := NewDecoder(opusSampleRate, opusChannels)
		if err != nil {
			return nil, fmt.Errorf("opus decoder create: %w", err)
		}
		ownedDec = dec
		defer ownedDec.Close()
	}

	dec := ownedDec
	if cd != nil {
		dec = cd.dec
	}

	var allSamples []int16
	var samplesPerFrame int32

	pcm := make([]int16, opusMaxFrameSize)
	for _, frame := range req.Frames {
		n, err := dec.Decode(frame, pcm, opusMaxFrameSize, false)
		if err != nil {
			return nil, fmt.Errorf("opus decode: %w", err)
		}
		if samplesPerFrame == 0 {
			samplesPerFrame = int32(n)
		}
		allSamples = append(allSamples, pcm[:n]...)
	}

	return &pb.AudioDecodeResult{
		PcmData:         sound.Int16toBytesLE(allSamples),
		SampleRate:      opusSampleRate,
		SamplesPerFrame: samplesPerFrame,
	}, nil
}
