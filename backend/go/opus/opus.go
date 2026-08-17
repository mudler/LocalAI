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

type cachedEncoder struct {
	mu           sync.Mutex
	enc          *Encoder
	resampler    *sound.StreamingResampler
	sourceRate   int
	pcmRemainder []int16
	lastUsed     time.Time
}

type Opus struct {
	base.Base

	encodersMu sync.Mutex
	encoders   map[string]*cachedEncoder
	decodersMu sync.Mutex
	decoders   map[string]*cachedDecoder
}

// Load accepts only the codec's own name (what the realtime WebRTC path
// sends) or no name at all — there is no model artefact here, so without
// this check the model loader's greedy autoload (which probes every
// installed backend with real model names) would happily bind an LLM to
// the audio codec.
func (o *Opus) Load(opts *pb.ModelOptions) error {
	if m := opts.GetModel(); m != "" && m != "opus" {
		return fmt.Errorf("opus: refusing to load %q: opus is an audio codec, not a model backend", m)
	}
	o.encoders = make(map[string]*cachedEncoder)
	o.decoders = make(map[string]*cachedDecoder)
	go o.evictLoop()
	return Init()
}

func (o *Opus) evictLoop() {
	ticker := time.NewTicker(decoderEvictTick)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		o.encodersMu.Lock()
		for id, ce := range o.encoders {
			ce.mu.Lock()
			if now.Sub(ce.lastUsed) > decoderIdleTTL {
				ce.enc.Close()
				delete(o.encoders, id)
			}
			ce.mu.Unlock()
		}
		o.encodersMu.Unlock()

		o.decodersMu.Lock()
		for id, cd := range o.decoders {
			cd.mu.Lock()
			if now.Sub(cd.lastUsed) > decoderIdleTTL {
				cd.dec.Close()
				delete(o.decoders, id)
			}
			cd.mu.Unlock()
		}
		o.decodersMu.Unlock()
	}
}

// lockDecoder returns a cached decoder locked against both use and eviction.
func (o *Opus) lockDecoder(sessionID string) (*cachedDecoder, error) {
	o.decodersMu.Lock()
	if cd, ok := o.decoders[sessionID]; ok {
		cd.mu.Lock()
		cd.lastUsed = time.Now()
		o.decodersMu.Unlock()
		return cd, nil
	}

	dec, err := NewDecoder(opusSampleRate, opusChannels)
	if err != nil {
		o.decodersMu.Unlock()
		return nil, err
	}
	cd := &cachedDecoder{dec: dec, lastUsed: time.Now()}
	cd.mu.Lock()
	o.decoders[sessionID] = cd
	o.decodersMu.Unlock()
	return cd, nil
}

func newConfiguredEncoder() (*Encoder, error) {
	enc, err := NewEncoder(opusSampleRate, opusChannels, ApplicationAudio)
	if err != nil {
		return nil, fmt.Errorf("opus encoder create: %w", err)
	}
	if err := enc.SetBitrate(64000); err != nil {
		enc.Close()
		return nil, fmt.Errorf("opus set bitrate: %w", err)
	}
	if err := enc.SetComplexity(10); err != nil {
		enc.Close()
		return nil, fmt.Errorf("opus set complexity: %w", err)
	}
	return enc, nil
}

// lockEncoder returns one transport-scoped encoder locked against use and
// eviction. The RTP SSRC, not an individual TTS callback, defines the codec
// stream lifetime.
func (o *Opus) lockEncoder(streamID string) (*cachedEncoder, error) {
	o.encodersMu.Lock()
	if ce, ok := o.encoders[streamID]; ok {
		ce.mu.Lock()
		ce.lastUsed = time.Now()
		o.encodersMu.Unlock()
		return ce, nil
	}
	enc, err := newConfiguredEncoder()
	if err != nil {
		o.encodersMu.Unlock()
		return nil, err
	}
	ce := &cachedEncoder{enc: enc, lastUsed: time.Now()}
	ce.mu.Lock()
	o.encoders[streamID] = ce
	o.encodersMu.Unlock()
	return ce, nil
}

func (o *Opus) closeEncoder(streamID string) {
	o.encodersMu.Lock()
	ce, ok := o.encoders[streamID]
	if !ok {
		o.encodersMu.Unlock()
		return
	}
	ce.mu.Lock()
	delete(o.encoders, streamID)
	o.encodersMu.Unlock()
	ce.enc.Close()
	ce.mu.Unlock()
}

func encodeOpusFrames(enc *Encoder, samples []int16) ([][]byte, error) {
	packet := make([]byte, opusMaxPacketSize)
	frames := make([][]byte, 0, len(samples)/opusFrameSize)
	for offset := 0; offset+opusFrameSize <= len(samples); offset += opusFrameSize {
		n, err := enc.Encode(samples[offset:offset+opusFrameSize], opusFrameSize, packet)
		if err != nil {
			return nil, fmt.Errorf("opus encode: %w", err)
		}
		out := make([]byte, n)
		copy(out, packet[:n])
		frames = append(frames, out)
	}
	return frames, nil
}

func (o *Opus) AudioEncode(req *pb.AudioEncodeRequest) (*pb.AudioEncodeResult, error) {
	streamID := req.Options["stream_id"]
	if streamID != "" && req.Options["close"] == "true" {
		o.closeEncoder(streamID)
		return &pb.AudioEncodeResult{SampleRate: opusSampleRate, SamplesPerFrame: opusFrameSize}, nil
	}
	samples := sound.BytesToInt16sLE(req.PcmData)
	if streamID == "" {
		if len(samples) == 0 {
			return &pb.AudioEncodeResult{SampleRate: opusSampleRate, SamplesPerFrame: opusFrameSize}, nil
		}
		enc, err := newConfiguredEncoder()
		if err != nil {
			return nil, err
		}
		defer enc.Close()
		if req.SampleRate != 0 && int(req.SampleRate) != opusSampleRate {
			samples = sound.ResampleInt16(samples, int(req.SampleRate), opusSampleRate)
		}
		frames, err := encodeOpusFrames(enc, samples)
		if err != nil {
			return nil, err
		}
		return &pb.AudioEncodeResult{Frames: frames, SampleRate: opusSampleRate, SamplesPerFrame: opusFrameSize}, nil
	}

	ce, err := o.lockEncoder(streamID)
	if err != nil {
		return nil, err
	}
	defer ce.mu.Unlock()

	if req.Options["abort"] == "true" {
		ce.resampler = nil
		ce.sourceRate = 0
		ce.pcmRemainder = nil
		return &pb.AudioEncodeResult{SampleRate: opusSampleRate, SamplesPerFrame: opusFrameSize}, nil
	}
	if len(samples) > 0 {
		sourceRate := int(req.SampleRate)
		if sourceRate == 0 {
			sourceRate = opusSampleRate
		}
		if ce.resampler == nil {
			ce.resampler, err = sound.NewStreamingResampler(sourceRate, opusSampleRate)
			if err != nil {
				return nil, err
			}
			ce.sourceRate = sourceRate
		} else if ce.sourceRate != sourceRate {
			return nil, fmt.Errorf("opus stream %q sample rate changed from %d to %d", streamID, ce.sourceRate, sourceRate)
		}
		resampled, err := ce.resampler.Push(samples)
		if err != nil {
			return nil, err
		}
		ce.pcmRemainder = append(ce.pcmRemainder, resampled...)
	}
	if req.Options["drain"] == "true" {
		if ce.resampler != nil {
			ce.pcmRemainder = append(ce.pcmRemainder, ce.resampler.Flush()...)
		}
		if remainder := len(ce.pcmRemainder) % opusFrameSize; remainder != 0 {
			ce.pcmRemainder = append(ce.pcmRemainder, make([]int16, opusFrameSize-remainder)...)
		}
	}
	completeSamples := len(ce.pcmRemainder) / opusFrameSize * opusFrameSize
	frames, err := encodeOpusFrames(ce.enc, ce.pcmRemainder[:completeSamples])
	if err != nil {
		return nil, err
	}
	ce.pcmRemainder = append([]int16(nil), ce.pcmRemainder[completeSamples:]...)
	if req.Options["drain"] == "true" {
		ce.resampler = nil
		ce.sourceRate = 0
		ce.pcmRemainder = nil
	}
	return &pb.AudioEncodeResult{Frames: frames, SampleRate: opusSampleRate, SamplesPerFrame: opusFrameSize}, nil
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
		cd, err = o.lockDecoder(sessionID)
		if err != nil {
			return nil, fmt.Errorf("opus decoder create: %w", err)
		}
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
