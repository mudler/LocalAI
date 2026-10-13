// SPDX-License-Identifier: MIT
package config

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime/debug"

	gguf "github.com/gpustack/gguf-parser-go"
)

// ggufHeaderCacheSchema is the on-disk format version. A file written with any
// other value is ignored, so bump it when persistedGGUFHeader changes shape.
const ggufHeaderCacheSchema = 1

// ggufHeaderSpec makes the header cache a GGUF cache: the file name, the
// schema, the estimator fingerprint and the GGUF field mapping.
var ggufHeaderSpec = headerCacheSpec[ggufHeader]{
	name:        "GGUF header cache",
	filePrefix:  "gguf-header-cache-",
	schema:      ggufHeaderCacheSchema,
	fingerprint: ggufEstimatorFingerprint(),
	parse: func(path string) (ggufHeader, error) {
		return parseGGUFHeader(path, gguf.ParseGGUFFile)
	},
	encode: func(h ggufHeader) any { return toPersistedHeader(h) },
	decode: decodeGGUFHeader,
}

// persistedGGUFHeader mirrors ggufHeader. Every field is either a property of
// the file bytes or an estimator output whose inputs are fixed in code (no
// device metrics, fixed candidate contexts and offload), keyed by the estimator
// fingerprint. Nothing hardware-dependent is stored: the per-device budget is
// read at decision time from perDeviceVRAM, not from this file.
type persistedGGUFHeader struct {
	Name             string         `json:"name"`
	Architecture     string         `json:"architecture"`
	MaxContextLength int            `json:"max_context_length"`
	BOSTokenID       int64          `json:"bos_token_id"`
	EOSTokenID       int64          `json:"eos_token_id"`
	ChatTemplate     string         `json:"chat_template"`
	HasChatTemplate  bool           `json:"has_chat_template"`
	MTPLayers        uint32         `json:"mtp_layers"`
	HasMTP           bool           `json:"has_mtp"`
	TrainedContext   int            `json:"trained_context"`
	Footprints       map[int]uint64 `json:"footprints,omitempty"`
	MaxCtxFootprint  uint64         `json:"max_ctx_footprint"`
	// TrainedPanic is the estimator panic message, kept so the replay still
	// happens for this file. The replay panics with the message as a string,
	// which the defaults path recovers the same way.
	TrainedPanic *string `json:"trained_panic,omitempty"`
}

// ggufHeaderCachePath returns the GGUF cache file for an instance. An empty ID
// (standalone mode) uses a fixed name.
func ggufHeaderCachePath(dataPath, instanceID string) string {
	return headerCachePath(ggufHeaderSpec.filePrefix, dataPath, instanceID)
}

// ggufEstimatorFingerprint names the inputs that decide the estimator-derived
// fields: the gguf-parser version, the candidate contexts, the auto context
// size and the offload count. Changing any of them changes the file's key.
func ggufEstimatorFingerprint() string {
	parser := "unknown"
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, d := range bi.Deps {
			if d.Path == "github.com/gpustack/gguf-parser-go" {
				parser = d.Version
				if d.Replace != nil {
					parser = d.Replace.Path + "@" + d.Replace.Version
				}
			}
		}
	}
	return fmt.Sprintf("gguf-parser=%s;candidates=%v;auto=%d;ngl=%d",
		parser, contextFitCandidates, DefaultAutoContextSize, DefaultNGPULayers)
}

// decodeGGUFHeader rebuilds a snapshot from its file form. An absent header
// decodes to the zero snapshot, as a missing JSON value always did.
func decodeGGUFHeader(raw json.RawMessage) (ggufHeader, error) {
	var p persistedGGUFHeader
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return ggufHeader{}, err
		}
	}
	return fromPersistedHeader(p), nil
}

func toPersistedHeader(h ggufHeader) persistedGGUFHeader {
	var panicked *string
	if h.trainedPanic != nil {
		msg := fmt.Sprint(h.trainedPanic)
		panicked = &msg
	}
	return persistedGGUFHeader{
		Name:             h.Name,
		Architecture:     h.Architecture,
		MaxContextLength: h.MaxContextLength,
		BOSTokenID:       h.BOSTokenID,
		EOSTokenID:       h.EOSTokenID,
		ChatTemplate:     h.ChatTemplate,
		HasChatTemplate:  h.HasChatTemplate,
		MTPLayers:        h.MTPLayers,
		HasMTP:           h.HasMTP,
		TrainedContext:   h.TrainedContext,
		Footprints:       h.footprints,
		MaxCtxFootprint:  h.maxCtxFootprint,
		TrainedPanic:     panicked,
	}
}

func fromPersistedHeader(p persistedGGUFHeader) ggufHeader {
	var panicked any
	if p.TrainedPanic != nil {
		panicked = *p.TrainedPanic
	}
	return ggufHeader{
		Name:             p.Name,
		Architecture:     p.Architecture,
		MaxContextLength: p.MaxContextLength,
		BOSTokenID:       p.BOSTokenID,
		EOSTokenID:       p.EOSTokenID,
		ChatTemplate:     p.ChatTemplate,
		HasChatTemplate:  p.HasChatTemplate,
		MTPLayers:        p.MTPLayers,
		HasMTP:           p.HasMTP,
		TrainedContext:   p.TrainedContext,
		footprints:       p.Footprints,
		maxCtxFootprint:  p.MaxCtxFootprint,
		trainedPanic:     panicked,
	}
}

// ConfigureGGUFHeaderCache attaches the process-wide header cache to the
// instance's data directory. Call it once at startup, after the instance ID is
// known and before model configs are loaded. An empty dataPath (standalone
// without data) keeps the cache in memory only. The cache is a documented
// per-instance exception: each frontend keeps its own file.
func ConfigureGGUFHeaderCache(ctx context.Context, dataPath, instanceID string) {
	ggufHeaders.attachDisk(ctx, dataPath, instanceID)
}
