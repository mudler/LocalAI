// SPDX-License-Identifier: MIT
// Package media validates bounded music audio bytes. It confers no upload
// ownership, normalization contract, model capability or runtime readiness.
package media

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
)

type Encoding uint8

const (
	PCM Encoding = iota + 1
	IEEEFloat
)

type Metadata struct {
	Encoding                   Encoding
	SampleRate                 uint32
	Channels, BitsPerSample    uint16
	Frames, InterleavedSamples uint64
}

const (
	recordingBytes   int64  = 25 << 20
	outputBytes      int64  = 128 << 20
	maxChunks               = 1024
	recordingSamples uint64 = 5760000
)

// ValidationError uses the handlers' structural public-error vocabulary. It never
// retains reader diagnostics, filenames or payload data. Cancellation is returned
// unchanged rather than encoded as a synthetic HTTP response.
type ValidationError struct {
	status int
	reason string
}

func (e ValidationError) Error() string       { return "Music WAV validation failed." }
func (e ValidationError) HTTPStatus() int     { return e.status }
func (e ValidationError) MusicStage() string  { return "validation" }
func (e ValidationError) MusicReason() string { return e.reason }
func invalid() error                          { return ValidationError{422, "invalid_input"} }
func unsupported() error                      { return ValidationError{415, "unsupported_media_type"} }
func oversized() error                        { return ValidationError{413, "size_limit"} }

// ValidateRecordingWAV accepts little-endian RIFF/WAVE integer PCM 8/16/24/32,
// mono/stereo, 8–48 kHz, 0.1–60 seconds inclusive. This is WAV-only validation;
// required FLAC input support remains absent and must not be advertised as ready.
// Validation reads from byte zero and restores the original cursor on EVERY exit
// after a successful initial position query. Readers must be privately owned,
// immutable, and exclusively positioned for the duration of the call.
func ValidateRecordingWAV(ctx context.Context, r io.ReadSeeker, size int64) (Metadata, error) {
	return validate(ctx, r, size, true)
}

// ValidateOutputWAV fits outputstore.WAVValidator. It additionally accepts finite
// IEEE float32/64, requires nonempty audio <=300 s, and limits encoded size to
// 128 MiB. Channels/rate match the recording subset; no resampling is performed.
func ValidateOutputWAV(ctx context.Context, r io.ReadSeeker, size int64) error {
	_, e := validate(ctx, r, size, false)
	return e
}

func validate(ctx context.Context, r io.ReadSeeker, size int64, recording bool) (meta Metadata, err error) {
	if e := ctx.Err(); e != nil {
		return meta, e
	}
	if r == nil {
		return meta, invalid()
	}
	limit := outputBytes
	if recording {
		limit = recordingBytes
	}
	if size > limit {
		return meta, oversized()
	}
	if size < 12 {
		return meta, invalid()
	}
	original, e := r.Seek(0, io.SeekCurrent)
	if e != nil {
		return meta, invalid()
	}
	defer func() {
		if _, e := r.Seek(original, io.SeekStart); e != nil {
			meta = Metadata{}
			err = errors.Join(err, invalid())
		}
		if err != nil {
			meta = Metadata{}
		}
	}()
	end, e := r.Seek(0, io.SeekEnd)
	if e != nil || end != size {
		return meta, invalid()
	}
	if _, e = r.Seek(0, io.SeekStart); e != nil {
		return meta, invalid()
	}
	read := func(b []byte) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		if _, e := io.ReadFull(r, b); e != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return invalid()
		}
		return ctx.Err()
	}
	var header [12]byte
	if e = read(header[:]); e != nil {
		return meta, e
	}
	if string(header[:4]) != "RIFF" || string(header[8:]) != "WAVE" {
		return meta, unsupported()
	}
	if uint64(binary.LittleEndian.Uint32(header[4:]))+8 != uint64(size) {
		return meta, invalid()
	}
	offset := int64(12)
	seenFormat, seenData := false, false
	var frameBytes uint16
	var scratch [32 << 10]byte
	for chunks := 0; offset < size; chunks++ {
		if chunks >= maxChunks {
			return meta, oversized()
		}
		if size-offset < 8 {
			return meta, invalid()
		}
		var chunk [8]byte
		if e = read(chunk[:]); e != nil {
			return meta, e
		}
		offset += 8
		length := int64(binary.LittleEndian.Uint32(chunk[4:]))
		padded := length + (length & 1)
		// All arithmetic is int64 over bounded uint32 lengths, checked against the
		// actual end BEFORE reading. No chunk-sized or sample-count-sized allocation.
		if padded > size-offset {
			return meta, invalid()
		}
		switch string(chunk[:4]) {
		case "fmt ":
			if seenFormat || seenData {
				return meta, invalid()
			}
			seenFormat = true
			if length != 16 && length != 18 {
				return meta, unsupported()
			}
			var format [18]byte
			if e = read(format[:length]); e != nil {
				return meta, e
			}
			tag := binary.LittleEndian.Uint16(format[:])
			meta.Channels = binary.LittleEndian.Uint16(format[2:])
			meta.SampleRate = binary.LittleEndian.Uint32(format[4:])
			rateBytes := binary.LittleEndian.Uint32(format[8:])
			frameBytes = binary.LittleEndian.Uint16(format[12:])
			meta.BitsPerSample = binary.LittleEndian.Uint16(format[14:])
			if length == 18 && binary.LittleEndian.Uint16(format[16:]) != 0 {
				return meta, unsupported()
			}
			switch tag {
			case 1:
				meta.Encoding = PCM
				switch meta.BitsPerSample {
				case 8, 16, 24, 32:
				default:
					return meta, unsupported()
				}
			case 3:
				if recording {
					return meta, unsupported()
				}
				meta.Encoding = IEEEFloat
				if meta.BitsPerSample != 32 && meta.BitsPerSample != 64 {
					return meta, unsupported()
				}
			default:
				return meta, unsupported()
			}
			if meta.Channels < 1 || meta.Channels > 2 || meta.SampleRate < 8000 || meta.SampleRate > 48000 {
				return meta, invalid()
			}
			expected := uint32(meta.Channels) * uint32(meta.BitsPerSample) / 8
			if uint32(frameBytes) != expected || rateBytes != meta.SampleRate*expected {
				return meta, invalid()
			}
		case "data":
			if !seenFormat || seenData {
				return meta, invalid()
			}
			seenData = true
			if length == 0 || length%int64(frameBytes) != 0 {
				return meta, invalid()
			}
			meta.Frames = uint64(length / int64(frameBytes))
			meta.InterleavedSamples = meta.Frames * uint64(meta.Channels)
			maxSeconds := uint64(300)
			if recording {
				maxSeconds = 60
				if meta.Frames*10 < uint64(meta.SampleRate) {
					return meta, invalid()
				}
				if meta.InterleavedSamples > recordingSamples {
					return meta, oversized()
				}
			}
			if meta.Frames > maxSeconds*uint64(meta.SampleRate) {
				return meta, invalid()
			}
			// Consume every encoded PCM sample; integer PCM has only finite values.
			// Float samples are inspected individually. Buffer size is fixed and aligned
			// to every accepted sample width, independent of attacker-controlled sizes.
			remaining := length
			for remaining > 0 {
				n := int64(len(scratch))
				if remaining < n {
					n = remaining
				}
				if e = read(scratch[:n]); e != nil {
					return meta, e
				}
				if meta.Encoding == IEEEFloat {
					width := int(meta.BitsPerSample / 8)
					for i := 0; i < int(n); i += width {
						var value float64
						if width == 4 {
							value = float64(math.Float32frombits(binary.LittleEndian.Uint32(scratch[i:])))
						} else {
							value = math.Float64frombits(binary.LittleEndian.Uint64(scratch[i:]))
						}
						if math.IsNaN(value) || math.IsInf(value, 0) {
							return meta, invalid()
						}
					}
				}
				remaining -= n
			}
		default:
			// Ancillary chunks are opaque bounded bytes, never metadata allocations.
			remaining := length
			for remaining > 0 {
				n := int64(len(scratch))
				if remaining < n {
					n = remaining
				}
				if e = read(scratch[:n]); e != nil {
					return meta, e
				}
				remaining -= n
			}
		}
		offset += length
		if length&1 != 0 {
			var pad [1]byte
			if e = read(pad[:]); e != nil {
				return meta, e
			}
			if pad[0] != 0 {
				return meta, invalid()
			}
			offset++
		}
	}
	if !seenFormat || !seenData {
		return meta, invalid()
	}
	if e = ctx.Err(); e != nil {
		return meta, e
	}
	return meta, nil
}
