// SPDX-License-Identifier: MIT
package embeddingmedia

// Resource constants are protocol version 1 hard limits. Bytes use binary
// units; pixels use decimal megapixels; all durations use milliseconds.
const (
	MaxBodyBytes                uint64 = 48 << 20
	MaxPartDurationMilliseconds uint64 = 30_000
	MaxImagePixels              uint64 = 16_000_000
	MaxAudioChannels            uint64 = 2
	MaxAudioSampleRateHz        uint64 = 192_000
	MaxFetchMilliseconds        uint64 = 30_000
	MaxConcurrentPreparations   uint64 = 2
	MaxMediaBytes               uint64 = 32 << 20
	MaxTextBytes                uint64 = 1 << 20
	MaxItems                    uint64 = 16
	MaxSourceParts              uint64 = 128
	MaxFrames                   uint64 = 120
	MaxPixels                   uint64 = 64_000_000
	MaxAudioBytes               uint64 = 64 << 20
	MaxDurationMilliseconds     uint64 = 60_000
	MaxPreparationMilliseconds  uint64 = 60_000
	MaxTemporaryBytes           uint64 = 256 << 20
)

// Error contains classification only, never user text, media or URLs.
type Error struct {
	Status int
	Code   string
}

func (e *Error) Error() string { return e.Code }

// Budget holds remaining request-wide capacity. Its zero value is exhausted,
// not unlimited. Copy it to transmit a snapshot, not to reset a live request.
// AudioBytes and TemporaryBytes conservatively count cumulative allocations
// and writes, even after buffers or files are released. DurationMilliseconds
// counts source duration once, including video with a soundtrack. Preparation
// time counts elapsed preparation work, not inference time. Concurrent work
// must charge its summed elapsed durations. Callers serialize Consume calls.
type Budget struct {
	MediaBytes, TextBytes, Items, SourceParts, Frames, Pixels, AudioBytes uint64
	DurationMilliseconds, PreparationMilliseconds, TemporaryBytes         uint64
}

// Consumption uses the same explicit units as Budget. Callers must check any
// multiplication or addition used to construct consumption before allocation.
type Consumption Budget

func NewBudget() Budget {
	return Budget{
		MediaBytes:              MaxMediaBytes,
		TextBytes:               MaxTextBytes,
		Items:                   MaxItems,
		SourceParts:             MaxSourceParts,
		Frames:                  MaxFrames,
		Pixels:                  MaxPixels,
		AudioBytes:              MaxAudioBytes,
		DurationMilliseconds:    MaxDurationMilliseconds,
		PreparationMilliseconds: MaxPreparationMilliseconds,
		TemporaryBytes:          MaxTemporaryBytes,
	}
}

// Consume validates the whole charge before subtraction. Subtraction after
// comparison cannot wrap and a rejected charge never partially changes state.
func (b *Budget) Consume(c Consumption) error {
	if c.MediaBytes > b.MediaBytes ||
		c.TextBytes > b.TextBytes ||
		c.Items > b.Items ||
		c.SourceParts > b.SourceParts ||
		c.Frames > b.Frames ||
		c.Pixels > b.Pixels ||
		c.AudioBytes > b.AudioBytes ||
		c.DurationMilliseconds > b.DurationMilliseconds ||
		c.PreparationMilliseconds > b.PreparationMilliseconds ||
		c.TemporaryBytes > b.TemporaryBytes {
		return &Error{Status: 413, Code: "embedding_resource_limit"}
	}
	b.MediaBytes -= c.MediaBytes
	b.TextBytes -= c.TextBytes
	b.Items -= c.Items
	b.SourceParts -= c.SourceParts
	b.Frames -= c.Frames
	b.Pixels -= c.Pixels
	b.AudioBytes -= c.AudioBytes
	b.DurationMilliseconds -= c.DurationMilliseconds
	b.PreparationMilliseconds -= c.PreparationMilliseconds
	b.TemporaryBytes -= c.TemporaryBytes
	return nil
}

// CheckLimit checks a unit-matched scalar before allocation or admission.
// Deadline and capacity callers map the failure to their operation's status.
func CheckLimit(value, maximum uint64) error {
	if value > maximum {
		return &Error{Status: 413, Code: "embedding_resource_limit"}
	}
	return nil
}
