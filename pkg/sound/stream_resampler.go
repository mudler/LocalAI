// SPDX-License-Identifier: MIT

package sound

import "fmt"

// StreamingResampler preserves interpolation phase across independently
// delivered PCM buffers. Push withholds samples that need future input; Flush
// emits the terminal samples using the same end-clamping rule as ResampleInt16.
type StreamingResampler struct {
	inputRate   int64
	outputRate  int64
	buffer      []int16
	baseIndex   int64
	totalInput  int64
	outputCount int64
	finished    bool
}

func NewStreamingResampler(inputRate, outputRate int) (*StreamingResampler, error) {
	if inputRate <= 0 || outputRate <= 0 {
		return nil, fmt.Errorf("streaming resampler rates must be positive")
	}
	return &StreamingResampler{
		inputRate:  int64(inputRate),
		outputRate: int64(outputRate),
	}, nil
}

func (r *StreamingResampler) Push(input []int16) ([]int16, error) {
	if r.finished {
		return nil, fmt.Errorf("streaming resampler is already flushed")
	}
	if len(input) == 0 {
		return nil, nil
	}
	if r.inputRate == r.outputRate {
		r.totalInput += int64(len(input))
		r.outputCount += int64(len(input))
		out := make([]int16, len(input))
		copy(out, input)
		return out, nil
	}
	r.buffer = append(r.buffer, input...)
	r.totalInput += int64(len(input))
	return r.produce(false), nil
}

func (r *StreamingResampler) Flush() []int16 {
	if r.finished {
		return nil
	}
	r.finished = true
	if r.inputRate == r.outputRate {
		return nil
	}
	return r.produce(true)
}

func (r *StreamingResampler) Reset() {
	r.buffer = nil
	r.baseIndex = 0
	r.totalInput = 0
	r.outputCount = 0
	r.finished = false
}

func (r *StreamingResampler) produce(final bool) []int16 {
	if len(r.buffer) == 0 {
		return nil
	}
	target := int64(-1)
	if final {
		target = r.totalInput * r.outputRate / r.inputRate
	}
	var output []int16
	for target < 0 || r.outputCount < target {
		positionNumerator := r.outputCount * r.inputRate
		beforeIndex := positionNumerator / r.outputRate
		afterIndex := beforeIndex + 1
		if !final && afterIndex >= r.totalInput {
			break
		}
		if beforeIndex >= r.totalInput {
			break
		}
		if afterIndex >= r.totalInput {
			afterIndex = r.totalInput - 1
		}
		before := r.buffer[beforeIndex-r.baseIndex]
		after := r.buffer[afterIndex-r.baseIndex]
		fraction := float64(positionNumerator%r.outputRate) / float64(r.outputRate)
		output = append(output, int16((1-fraction)*float64(before)+fraction*float64(after)))
		r.outputCount++
	}

	nextIndex := r.outputCount * r.inputRate / r.outputRate
	if nextIndex > r.totalInput {
		nextIndex = r.totalInput
	}
	if nextIndex > r.baseIndex {
		drop := nextIndex - r.baseIndex
		if drop > int64(len(r.buffer)) {
			drop = int64(len(r.buffer))
		}
		r.buffer = r.buffer[drop:]
		r.baseIndex += drop
	}
	return output
}
