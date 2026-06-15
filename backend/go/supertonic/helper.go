// Vendored from supertone-inc/supertonic (go/helper.go) at commit
// dff55dc00064c398736080c78195f577527832ae.
//
// Copyright (c) Supertone, Inc. Licensed under the MIT License.
// See https://github.com/supertone-inc/supertonic/blob/main/LICENSE
//
// Local modifications (if any) are marked with "LocalAI:" comments.

package main

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/go-audio/audio"
	"github.com/go-audio/wav"
	ort "github.com/yalue/onnxruntime_go"
	"golang.org/x/text/unicode/norm"
)

// Available languages for multilingual TTS
var AvailableLangs = []string{"en", "ko", "ja", "ar", "bg", "cs", "da", "de", "el", "es", "et", "fi", "fr", "hi", "hr", "hu", "id", "it", "lt", "lv", "nl", "pl", "pt", "ro", "ru", "sk", "sl", "sv", "tr", "uk", "vi", "na"}

// Config structures
type SpecProcessorConfig struct {
	NFFT      int     `json:"n_fft"`
	WinLength int     `json:"win_length"`
	HopLength int     `json:"hop_length"`
	NMels     int     `json:"n_mels"`
	Eps       float64 `json:"eps"`
	NormMean  float64 `json:"norm_mean"`
	NormStd   float64 `json:"norm_std"`
}

type EncoderConfig struct {
	SpecProcessor SpecProcessorConfig `json:"spec_processor"`
}

type AEConfig struct {
	SampleRate    int           `json:"sample_rate"`
	BaseChunkSize int           `json:"base_chunk_size"`
	Encoder       EncoderConfig `json:"encoder"`
}

type StyleTokenLayerConfig struct {
	NStyle        int `json:"n_style"`
	StyleValueDim int `json:"style_value_dim"`
}

type StyleEncoderConfig struct {
	StyleTokenLayer StyleTokenLayerConfig `json:"style_token_layer"`
}

type ProjOutConfig struct {
	Idim int `json:"idim"`
	Odim int `json:"odim"`
}

type TextEncoderConfig struct {
	ProjOut ProjOutConfig `json:"proj_out"`
}

type TTLConfig struct {
	ChunkCompressFactor int                `json:"chunk_compress_factor"`
	LatentDim           int                `json:"latent_dim"`
	StyleEncoder        StyleEncoderConfig `json:"style_encoder"`
	TextEncoder         TextEncoderConfig  `json:"text_encoder"`
}

type DPStyleEncoderConfig struct {
	StyleTokenLayer StyleTokenLayerConfig `json:"style_token_layer"`
}

type DPConfig struct {
	LatentDim           int                  `json:"latent_dim"`
	ChunkCompressFactor int                  `json:"chunk_compress_factor"`
	StyleEncoder        DPStyleEncoderConfig `json:"style_encoder"`
}

type Config struct {
	AE  AEConfig  `json:"ae"`
	TTL TTLConfig `json:"ttl"`
	DP  DPConfig  `json:"dp"`
}

// VoiceStyleData holds voice style JSON structure
type VoiceStyleData struct {
	StyleTTL struct {
		Data [][][]float64 `json:"data"`
		Dims []int64       `json:"dims"`
		Type string        `json:"type"`
	} `json:"style_ttl"`
	StyleDP struct {
		Data [][][]float64 `json:"data"`
		Dims []int64       `json:"dims"`
		Type string        `json:"type"`
	} `json:"style_dp"`
}

// UnicodeProcessor for text processing
type UnicodeProcessor struct {
	indexer []int64
}

// NewUnicodeProcessor creates a new UnicodeProcessor
func NewUnicodeProcessor(unicodeIndexerPath string) (*UnicodeProcessor, error) {
	indexer, err := loadJSONInt64(unicodeIndexerPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load unicode indexer: %w", err)
	}

	return &UnicodeProcessor{indexer: indexer}, nil
}

// Call processes text list to text IDs and mask
func (up *UnicodeProcessor) Call(textList []string, langList []string) ([][]int64, [][][]float64) {
	// Preprocess texts
	processedTexts := make([]string, len(textList))
	for i, text := range textList {
		processedTexts[i] = preprocessText(text, langList[i])
	}

	// Get text lengths
	textLengths := make([]int64, len(processedTexts))
	maxLen := 0
	for i, text := range processedTexts {
		textLengths[i] = int64(len([]rune(text)))
		if int(textLengths[i]) > maxLen {
			maxLen = int(textLengths[i])
		}
	}

	// Create text IDs
	textIDs := make([][]int64, len(processedTexts))
	for i, text := range processedTexts {
		row := make([]int64, maxLen)
		runes := []rune(text)
		for j, r := range runes {
			unicodeVal := int(r)
			if unicodeVal < len(up.indexer) {
				row[j] = up.indexer[unicodeVal]
			} else {
				row[j] = -1
			}
		}
		textIDs[i] = row
	}

	// Create text mask
	textMask := lengthToMask(textLengths, maxLen)

	return textIDs, textMask
}

// Text chunking utilities
const maxChunkLength = 300

var abbreviations = []string{
	"Dr.", "Mr.", "Mrs.", "Ms.", "Prof.", "Sr.", "Jr.",
	"St.", "Ave.", "Rd.", "Blvd.", "Dept.", "Inc.", "Ltd.",
	"Co.", "Corp.", "etc.", "vs.", "i.e.", "e.g.", "Ph.D.",
}

func chunkText(text string, maxLen int) []string {
	if maxLen == 0 {
		maxLen = maxChunkLength
	}

	text = strings.TrimSpace(text)
	if text == "" {
		return []string{""}
	}

	// Split by paragraphs
	paragraphs := regexp.MustCompile(`\n\s*\n`).Split(text, -1)
	var chunks []string

	for _, para := range paragraphs {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}

		if len(para) <= maxLen {
			chunks = append(chunks, para)
			continue
		}

		// Split by sentences
		sentences := splitSentences(para)
		var current strings.Builder
		currentLen := 0

		for _, sentence := range sentences {
			sentence = strings.TrimSpace(sentence)
			if sentence == "" {
				continue
			}

			sentenceLen := len(sentence)
			if sentenceLen > maxLen {
				// If sentence is longer than maxLen, split by comma or space
				if current.Len() > 0 {
					chunks = append(chunks, strings.TrimSpace(current.String()))
					current.Reset()
					currentLen = 0
				}

				// Try splitting by comma
				parts := strings.Split(sentence, ",")
				for _, part := range parts {
					part = strings.TrimSpace(part)
					if part == "" {
						continue
					}

					partLen := len(part)
					if partLen > maxLen {
						// Split by space as last resort
						words := strings.Fields(part)
						var wordChunk strings.Builder
						wordChunkLen := 0

						for _, word := range words {
							wordLen := len(word)
							if wordChunkLen+wordLen+1 > maxLen && wordChunk.Len() > 0 {
								chunks = append(chunks, strings.TrimSpace(wordChunk.String()))
								wordChunk.Reset()
								wordChunkLen = 0
							}

							if wordChunk.Len() > 0 {
								wordChunk.WriteString(" ")
								wordChunkLen++
							}
							wordChunk.WriteString(word)
							wordChunkLen += wordLen
						}

						if wordChunk.Len() > 0 {
							chunks = append(chunks, strings.TrimSpace(wordChunk.String()))
						}
					} else {
						if currentLen+partLen+1 > maxLen && current.Len() > 0 {
							chunks = append(chunks, strings.TrimSpace(current.String()))
							current.Reset()
							currentLen = 0
						}

						if current.Len() > 0 {
							current.WriteString(", ")
							currentLen += 2
						}
						current.WriteString(part)
						currentLen += partLen
					}
				}
				continue
			}

			if currentLen+sentenceLen+1 > maxLen && current.Len() > 0 {
				chunks = append(chunks, strings.TrimSpace(current.String()))
				current.Reset()
				currentLen = 0
			}

			if current.Len() > 0 {
				current.WriteString(" ")
				currentLen++
			}
			current.WriteString(sentence)
			currentLen += sentenceLen
		}

		if current.Len() > 0 {
			chunks = append(chunks, strings.TrimSpace(current.String()))
		}
	}

	if len(chunks) == 0 {
		return []string{""}
	}

	return chunks
}

func splitSentences(text string) []string {
	// Go's regexp doesn't support lookbehind, so we use a simpler approach
	// Split on sentence boundaries and then check if they're abbreviations
	re := regexp.MustCompile(`([.!?])\s+`)
	
	// Find all matches
	matches := re.FindAllStringIndex(text, -1)
	if len(matches) == 0 {
		return []string{text}
	}
	
	var sentences []string
	lastEnd := 0
	
	for _, match := range matches {
		// Get the text before the punctuation
		beforePunc := text[lastEnd:match[0]]
		
		// Check if this ends with an abbreviation
		isAbbrev := false
		for _, abbrev := range abbreviations {
			if strings.HasSuffix(strings.TrimSpace(beforePunc+text[match[0]:match[0]+1]), abbrev) {
				isAbbrev = true
				break
			}
		}
		
		if !isAbbrev {
			// This is a real sentence boundary
			sentences = append(sentences, text[lastEnd:match[1]])
			lastEnd = match[1]
		}
	}
	
	// Add the remaining text
	if lastEnd < len(text) {
		sentences = append(sentences, text[lastEnd:])
	}
	
	if len(sentences) == 0 {
		return []string{text}
	}
	
	return sentences
}

// isValidLang checks if a language is in the available languages list
func isValidLang(lang string) bool {
	for _, l := range AvailableLangs {
		if l == lang {
			return true
		}
	}
	return false
}

// Utility functions
func preprocessText(text string, lang string) string {
	// TODO: Need advanced normalizer for better performance
	// Apply NFKD normalization using golang.org/x/text/unicode/norm
	text = norm.NFKD.String(text)

	// Remove emojis and various Unicode symbols
	emojiPattern := regexp.MustCompile(`[\x{1F600}-\x{1F64F}\x{1F300}-\x{1F5FF}\x{1F680}-\x{1F6FF}\x{1F700}-\x{1F77F}\x{1F780}-\x{1F7FF}\x{1F800}-\x{1F8FF}\x{1F900}-\x{1F9FF}\x{1FA00}-\x{1FA6F}\x{1FA70}-\x{1FAFF}\x{2600}-\x{26FF}\x{2700}-\x{27BF}\x{1F1E6}-\x{1F1FF}]+`)
	text = emojiPattern.ReplaceAllString(text, "")

	// Replace various dashes and symbols
	replacements := map[string]string{
		"–": "-",    // en dash
		"‑": "-",    // non-breaking hyphen
		"—": "-",    // em dash
		"_": " ",    // underscore
		"\u201C": "\"",   // left double quote
		"\u201D": "\"",   // right double quote
		"\u2018": "'",    // left single quote
		"\u2019": "'",    // right single quote
		"´": "'",    // acute accent
		"`": "'",    // grave accent
		"[": " ",    // left bracket
		"]": " ",    // right bracket
		"|": " ",    // vertical bar
		"/": " ",    // slash
		"#": " ",    // hash
		"→": " ",    // right arrow
		"←": " ",    // left arrow
	}

	for old, new := range replacements {
		text = strings.ReplaceAll(text, old, new)
	}

	// Remove special symbols
	specialSymbols := []string{"♥", "☆", "♡", "©", "\\"}
	for _, symbol := range specialSymbols {
		text = strings.ReplaceAll(text, symbol, "")
	}

	// Replace known expressions
	exprReplacements := map[string]string{
		"@":     " at ",
		"e.g.,": "for example, ",
		"i.e.,": "that is, ",
	}

	for old, new := range exprReplacements {
		text = strings.ReplaceAll(text, old, new)
	}

	// Fix spacing around punctuation
	text = regexp.MustCompile(` ,`).ReplaceAllString(text, ",")
	text = regexp.MustCompile(` \.`).ReplaceAllString(text, ".")
	text = regexp.MustCompile(` !`).ReplaceAllString(text, "!")
	text = regexp.MustCompile(` \?`).ReplaceAllString(text, "?")
	text = regexp.MustCompile(` ;`).ReplaceAllString(text, ";")
	text = regexp.MustCompile(` :`).ReplaceAllString(text, ":")
	text = regexp.MustCompile(` '`).ReplaceAllString(text, "'")

	// Remove duplicate quotes
	for strings.Contains(text, `""`) {
		text = strings.ReplaceAll(text, `""`, `"`)
	}
	for strings.Contains(text, "''") {
		text = strings.ReplaceAll(text, "''", "'")
	}
	for strings.Contains(text, "``") {
		text = strings.ReplaceAll(text, "``", "`")
	}

	// Remove extra spaces
	text = regexp.MustCompile(`\s+`).ReplaceAllString(text, " ")
	text = strings.TrimSpace(text)

	// If text doesn't end with punctuation, quotes, or closing brackets, add a period
	if text != "" {
		endsWithPunct := regexp.MustCompile(`[.!?;:,'"\x{201C}\x{201D}\x{2018}\x{2019})\]}…。」』】〉》›»]$`)
		if !endsWithPunct.MatchString(text) {
			text += "."
		}
	}

	// Validate language
	if !isValidLang(lang) {
		panic(fmt.Sprintf("Invalid language: %s. Available: %v", lang, AvailableLangs))
	}

	// Wrap text with language tags
	text = fmt.Sprintf("<%s>%s</%s>", lang, text, lang)

	return text
}

func lengthToMask(lengths []int64, maxLen int) [][][]float64 {
	bsz := len(lengths)
	mask := make([][][]float64, bsz)

	for i := 0; i < bsz; i++ {
		row := make([]float64, maxLen)
		for j := 0; j < maxLen; j++ {
			if int64(j) < lengths[i] {
				row[j] = 1.0
			} else {
				row[j] = 0.0
			}
		}
		mask[i] = [][]float64{row}
	}

	return mask
}

func getTextMask(textLengths []int64, maxLen int) [][][]float64 {
	return lengthToMask(textLengths, maxLen)
}

func getLatentMask(wavLengths []int64, cfg Config) [][][]float64 {
	baseChunkSize := int64(cfg.AE.BaseChunkSize)
	chunkCompressFactor := int64(cfg.TTL.ChunkCompressFactor)
	latentSize := baseChunkSize * chunkCompressFactor

	latentLengths := make([]int64, len(wavLengths))
	maxLen := int64(0)
	for i, wavLen := range wavLengths {
		latentLengths[i] = (wavLen + latentSize - 1) / latentSize
		if latentLengths[i] > maxLen {
			maxLen = latentLengths[i]
		}
	}

	return lengthToMask(latentLengths, int(maxLen))
}

func writeWavFile(filename string, audioData []float64, sampleRate int) error {
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	// Convert float64 to int
	intData := make([]int, len(audioData))
	for i, sample := range audioData {
		// Clamp to [-1, 1] and convert to 16-bit int
		clamped := math.Max(-1.0, math.Min(1.0, sample))
		intData[i] = int(clamped * 32767)
	}

	encoder := wav.NewEncoder(file, sampleRate, 16, 1, 1)
	buf := &audio.IntBuffer{
		Data:           intData,
		Format:         &audio.Format{SampleRate: sampleRate, NumChannels: 1},
		SourceBitDepth: 16,
	}

	if err := encoder.Write(buf); err != nil {
		return err
	}

	return encoder.Close()
}

// Style holds style tensors
type Style struct {
	TtlTensor *ort.Tensor[float32]
	DpTensor  *ort.Tensor[float32]
}

func (s *Style) Destroy() {
	if s.TtlTensor != nil {
		s.TtlTensor.Destroy()
	}
	if s.DpTensor != nil {
		s.DpTensor.Destroy()
	}
}

// LoadVoiceStyle loads voice style from JSON files
func LoadVoiceStyle(voiceStylePaths []string, verbose bool) (*Style, error) {
	bsz := len(voiceStylePaths)

	// Read first file to get dimensions
	firstData, err := os.ReadFile(voiceStylePaths[0])
	if err != nil {
		return nil, fmt.Errorf("failed to read voice style file: %w", err)
	}

	var firstStyle VoiceStyleData
	if err := json.Unmarshal(firstData, &firstStyle); err != nil {
		return nil, fmt.Errorf("failed to parse voice style JSON: %w", err)
	}

	ttlDims := firstStyle.StyleTTL.Dims
	dpDims := firstStyle.StyleDP.Dims

	ttlDim1 := ttlDims[1]
	ttlDim2 := ttlDims[2]
	dpDim1 := dpDims[1]
	dpDim2 := dpDims[2]

	// Pre-allocate arrays with full batch size
	ttlSize := int(int64(bsz) * ttlDim1 * ttlDim2)
	dpSize := int(int64(bsz) * dpDim1 * dpDim2)
	ttlFlat := make([]float32, ttlSize)
	dpFlat := make([]float32, dpSize)

	// Fill in the data
	for i := 0; i < bsz; i++ {
		data, err := os.ReadFile(voiceStylePaths[i])
		if err != nil {
			return nil, fmt.Errorf("failed to read voice style file: %w", err)
		}

		var voiceStyle VoiceStyleData
		if err := json.Unmarshal(data, &voiceStyle); err != nil {
			return nil, fmt.Errorf("failed to parse voice style JSON: %w", err)
		}

		// Flatten TTL data
		ttlOffset := int(int64(i) * ttlDim1 * ttlDim2)
		idx := 0
		for _, batch := range voiceStyle.StyleTTL.Data {
			for _, row := range batch {
				for _, val := range row {
					ttlFlat[ttlOffset+idx] = float32(val)
					idx++
				}
			}
		}

		// Flatten DP data
		dpOffset := int(int64(i) * dpDim1 * dpDim2)
		idx = 0
		for _, batch := range voiceStyle.StyleDP.Data {
			for _, row := range batch {
				for _, val := range row {
					dpFlat[dpOffset+idx] = float32(val)
					idx++
				}
			}
		}
	}

	ttlShape := []int64{int64(bsz), ttlDim1, ttlDim2}
	dpShape := []int64{int64(bsz), dpDim1, dpDim2}

	ttlTensor, err := ort.NewTensor(ttlShape, ttlFlat)
	if err != nil {
		return nil, fmt.Errorf("failed to create TTL tensor: %w", err)
	}

	dpTensor, err := ort.NewTensor(dpShape, dpFlat)
	if err != nil {
		ttlTensor.Destroy()
		return nil, fmt.Errorf("failed to create DP tensor: %w", err)
	}

	if verbose {
		fmt.Printf("Loaded %d voice styles\n\n", bsz)
	}

	return &Style{
		TtlTensor: ttlTensor,
		DpTensor:  dpTensor,
	}, nil
}

// TextToSpeech generates speech from text
type TextToSpeech struct {
	cfg           Config
	textProcessor *UnicodeProcessor
	dpOrt         *ort.DynamicAdvancedSession
	textEncOrt    *ort.DynamicAdvancedSession
	vectorEstOrt  *ort.DynamicAdvancedSession
	vocoderOrt    *ort.DynamicAdvancedSession
	SampleRate    int
	baseChunkSize int
	chunkCompress int
	ldim          int
}

func (tts *TextToSpeech) sampleNoisyLatent(durOnnx []float32) ([][][]float64, [][][]float64) {
	bsz := len(durOnnx)
	maxDur := float64(0)
	for _, d := range durOnnx {
		if float64(d) > maxDur {
			maxDur = float64(d)
		}
	}

	wavLenMax := maxDur * float64(tts.SampleRate)
	wavLengths := make([]int64, bsz)
	for i, d := range durOnnx {
		wavLengths[i] = int64(float64(d) * float64(tts.SampleRate))
	}

	chunkSize := tts.baseChunkSize * tts.chunkCompress
	latentLen := int((wavLenMax + float64(chunkSize) - 1) / float64(chunkSize))
	latentDim := tts.ldim * tts.chunkCompress

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	noisyLatent := make([][][]float64, bsz)
	for b := 0; b < bsz; b++ {
		batch := make([][]float64, latentDim)
		for d := 0; d < latentDim; d++ {
			row := make([]float64, latentLen)
			for t := 0; t < latentLen; t++ {
				// Box-Muller transform for normal distribution
				// Add epsilon to avoid log(0)
				const eps = 1e-10
				u1 := math.Max(eps, rng.Float64())
				u2 := rng.Float64()
				row[t] = math.Sqrt(-2.0*math.Log(u1)) * math.Cos(2.0*math.Pi*u2)
			}
			batch[d] = row
		}
		noisyLatent[b] = batch
	}

	latentMask := getLatentMask(wavLengths, tts.cfg)

	// Apply mask
	for b := 0; b < bsz; b++ {
		for d := 0; d < latentDim; d++ {
			for t := 0; t < latentLen; t++ {
				noisyLatent[b][d][t] *= latentMask[b][0][t]
			}
		}
	}

	return noisyLatent, latentMask
}

func (tts *TextToSpeech) _infer(textList []string, langList []string, style *Style, totalStep int, speed float32) ([]float32, []float32, error) {
	bsz := len(textList)

	// Process text
	textIDs, textMask := tts.textProcessor.Call(textList, langList)
	textIDsShape := []int64{int64(bsz), int64(len(textIDs[0]))}
	textMaskShape := []int64{int64(bsz), 1, int64(len(textMask[0][0]))}

	textIDsTensor := IntArrayToTensor(textIDs, textIDsShape)
	defer textIDsTensor.Destroy()
	textMaskTensor := ArrayToTensor(textMask, textMaskShape)
	defer textMaskTensor.Destroy()

	// Predict duration
	dpOutputs := []ort.Value{nil}
	err := tts.dpOrt.Run(
		[]ort.Value{textIDsTensor, style.DpTensor, textMaskTensor},
		dpOutputs,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to run duration predictor: %w", err)
	}
	durTensor := dpOutputs[0].(*ort.Tensor[float32])
	defer durTensor.Destroy()
	durOnnx := durTensor.GetData()
	
	// Apply speed factor to duration
	for i := range durOnnx {
		durOnnx[i] /= speed
	}

	// Encode text
	textIDsTensor2 := IntArrayToTensor(textIDs, textIDsShape)
	defer textIDsTensor2.Destroy()
	textEncOutputs := []ort.Value{nil}
	err = tts.textEncOrt.Run(
		[]ort.Value{textIDsTensor2, style.TtlTensor, textMaskTensor},
		textEncOutputs,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to run text encoder: %w", err)
	}
	textEmbTensor := textEncOutputs[0].(*ort.Tensor[float32])
	defer textEmbTensor.Destroy()

	// Sample noisy latent
	xt, latentMask := tts.sampleNoisyLatent(durOnnx)
	latentShape := []int64{int64(bsz), int64(len(xt[0])), int64(len(xt[0][0]))}
	latentMaskShape := []int64{int64(bsz), 1, int64(len(latentMask[0][0]))}

	// Prepare constant arrays
	totalStepArray := make([]float32, bsz)
	for b := 0; b < bsz; b++ {
		totalStepArray[b] = float32(totalStep)
	}
	scalarShape := []int64{int64(bsz)}

	totalStepTensor, _ := ort.NewTensor(scalarShape, totalStepArray)
	defer totalStepTensor.Destroy()

	// Denoising loop
	for step := 0; step < totalStep; step++ {
		currentStepArray := make([]float32, bsz)
		for b := 0; b < bsz; b++ {
			currentStepArray[b] = float32(step)
		}

		currentStepTensor, _ := ort.NewTensor(scalarShape, currentStepArray)
		noisyLatentTensor := ArrayToTensor(xt, latentShape)
		latentMaskTensor := ArrayToTensor(latentMask, latentMaskShape)
		textMaskTensor2 := ArrayToTensor(textMask, textMaskShape)

		vectorEstOutputs := []ort.Value{nil}
		err = tts.vectorEstOrt.Run(
			[]ort.Value{noisyLatentTensor, textEmbTensor, style.TtlTensor, latentMaskTensor, textMaskTensor2,
				currentStepTensor, totalStepTensor},
			vectorEstOutputs,
		)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to run vector estimator: %w", err)
		}

		denoisedTensor := vectorEstOutputs[0].(*ort.Tensor[float32])
		denoisedData := denoisedTensor.GetData()

		// Update latent
		idx := 0
		for b := 0; b < bsz; b++ {
			for d := 0; d < len(xt[b]); d++ {
				for t := 0; t < len(xt[b][d]); t++ {
					xt[b][d][t] = float64(denoisedData[idx])
					idx++
				}
			}
		}

		noisyLatentTensor.Destroy()
		latentMaskTensor.Destroy()
		textMaskTensor2.Destroy()
		currentStepTensor.Destroy()
		denoisedTensor.Destroy()
	}

	// Generate waveform
	finalLatentTensor := ArrayToTensor(xt, latentShape)
	defer finalLatentTensor.Destroy()

	vocoderOutputs := []ort.Value{nil}
	err = tts.vocoderOrt.Run(
		[]ort.Value{finalLatentTensor},
		vocoderOutputs,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to run vocoder: %w", err)
	}

	wavBatchTensor := vocoderOutputs[0].(*ort.Tensor[float32])
	defer wavBatchTensor.Destroy()
	wav := wavBatchTensor.GetData()

	return wav, durOnnx, nil
}

// Call synthesizes speech from a single text with automatic chunking
func (tts *TextToSpeech) Call(text string, lang string, style *Style, totalStep int, speed float32, silenceDuration float32) ([]float32, float32, error) {
	maxLen := 300
	if lang == "ko" || lang == "ja" {
		maxLen = 120
	}
	chunks := chunkText(text, maxLen)
	
	var wavCat []float32
	var durCat float32

	for i, chunk := range chunks {
		wav, duration, err := tts._infer([]string{chunk}, []string{lang}, style, totalStep, speed)
		if err != nil {
			return nil, 0, err
		}

		dur := duration[0]
		wavLen := int(float32(tts.SampleRate) * dur)
		wavChunk := wav[:wavLen]

		if i == 0 {
			wavCat = wavChunk
			durCat = dur
		} else {
			silenceLen := int(silenceDuration * float32(tts.SampleRate))
			silence := make([]float32, silenceLen)
			
			wavCat = append(wavCat, silence...)
			wavCat = append(wavCat, wavChunk...)
			durCat += silenceDuration + dur
		}
	}

	return wavCat, durCat, nil
}

// Batch synthesizes speech from multiple texts
func (tts *TextToSpeech) Batch(textList []string, langList []string, style *Style, totalStep int, speed float32) ([]float32, []float32, error) {
	return tts._infer(textList, langList, style, totalStep, speed)
}

func (tts *TextToSpeech) Destroy() {
	if tts.dpOrt != nil {
		tts.dpOrt.Destroy()
	}
	if tts.textEncOrt != nil {
		tts.textEncOrt.Destroy()
	}
	if tts.vectorEstOrt != nil {
		tts.vectorEstOrt.Destroy()
	}
	if tts.vocoderOrt != nil {
		tts.vocoderOrt.Destroy()
	}
}

// LoadTextToSpeech loads TTS components
func LoadTextToSpeech(onnxDir string, useGPU bool, cfg Config) (*TextToSpeech, error) {
	if useGPU {
		return nil, fmt.Errorf("GPU mode is not supported yet")
	}
	fmt.Println("Using CPU for inference") // LocalAI: drop redundant newline (vet)

	// Load models
	dpPath := filepath.Join(onnxDir, "duration_predictor.onnx")
	textEncPath := filepath.Join(onnxDir, "text_encoder.onnx")
	vectorEstPath := filepath.Join(onnxDir, "vector_estimator.onnx")
	vocoderPath := filepath.Join(onnxDir, "vocoder.onnx")

	dpOrt, err := ort.NewDynamicAdvancedSession(dpPath, []string{"text_ids", "style_dp", "text_mask"},
		[]string{"duration"}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to load duration predictor: %w", err)
	}

	textEncOrt, err := ort.NewDynamicAdvancedSession(textEncPath, []string{"text_ids", "style_ttl", "text_mask"},
		[]string{"text_emb"}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to load text encoder: %w", err)
	}

	vectorEstOrt, err := ort.NewDynamicAdvancedSession(vectorEstPath,
		[]string{"noisy_latent", "text_emb", "style_ttl", "latent_mask", "text_mask", "current_step", "total_step"},
		[]string{"denoised_latent"}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to load vector estimator: %w", err)
	}

	vocoderOrt, err := ort.NewDynamicAdvancedSession(vocoderPath, []string{"latent"},
		[]string{"wav_tts"}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to load vocoder: %w", err)
	}

	// Load text processor
	unicodeIndexerPath := filepath.Join(onnxDir, "unicode_indexer.json")
	textProcessor, err := NewUnicodeProcessor(unicodeIndexerPath)
	if err != nil {
		return nil, err
	}

	textToSpeech := &TextToSpeech{
		cfg:           cfg,
		textProcessor: textProcessor,
		dpOrt:         dpOrt,
		textEncOrt:    textEncOrt,
		vectorEstOrt:  vectorEstOrt,
		vocoderOrt:    vocoderOrt,
		SampleRate:    cfg.AE.SampleRate,
		baseChunkSize: cfg.AE.BaseChunkSize,
		chunkCompress: cfg.TTL.ChunkCompressFactor,
		ldim:          cfg.TTL.LatentDim,
	}

	return textToSpeech, nil
}

// InitializeONNXRuntime initializes ONNX Runtime environment
func InitializeONNXRuntime() error {
	libPath := os.Getenv("ONNXRUNTIME_LIB_PATH")
	if libPath == "" {
		candidates := []string{
			"/opt/homebrew/opt/onnxruntime/lib/libonnxruntime.dylib",
			"/usr/local/opt/onnxruntime/lib/libonnxruntime.dylib",
			"/opt/homebrew/lib/libonnxruntime.dylib",
			"/usr/local/lib/libonnxruntime.dylib",
			"/usr/local/lib/libonnxruntime.so",
			"/usr/lib/libonnxruntime.so",
		}
		for _, candidate := range candidates {
			if _, err := os.Stat(candidate); err == nil {
				libPath = candidate
				break
			}
		}
		if libPath == "" {
			libPath = "/usr/local/lib/libonnxruntime.so"
		}
	}
	ort.SetSharedLibraryPath(libPath)

	if err := ort.InitializeEnvironment(); err != nil {
		return fmt.Errorf("failed to initialize ONNX Runtime: %w\nHint: install ONNX Runtime (macOS: brew install onnxruntime) or set ONNXRUNTIME_LIB_PATH", err)
	}
	return nil
}

// sanitizeFilename creates a safe filename from text (supports Unicode)
func sanitizeFilename(text string, maxLen int) string {
	runes := []rune(text)
	if len(runes) > maxLen {
		runes = runes[:maxLen]
	}
	
	result := make([]rune, 0, len(runes))
	for _, r := range runes {
		// unicode.IsLetter matches any Unicode letter, unicode.IsDigit matches any Unicode digit
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			result = append(result, r)
		} else {
			result = append(result, '_')
		}
	}
	return string(result)
}

// extractWavSegment extracts a single audio segment from batch output
func extractWavSegment(wav []float32, duration float32, sampleRate int, index int, batchSize int) []float64 {
	wavLen := int(float64(sampleRate) * float64(duration))
	wavPerBatch := len(wav) / batchSize
	
	wavStart := index * wavPerBatch
	wavEnd := wavStart + wavLen
	if wavEnd > len(wav) {
		wavEnd = len(wav)
	}
	
	wavOut := make([]float64, wavLen)
	for j := 0; j < wavLen && wavStart+j < len(wav); j++ {
		wavOut[j] = float64(wav[wavStart+j])
	}
	
	return wavOut
}

// Timer measures execution time
func Timer(name string, fn func() interface{}) interface{} {
	start := time.Now()
	fmt.Printf("%s...\n", name)
	result := fn()
	elapsed := time.Since(start).Seconds()
	fmt.Printf("  -> %s completed in %.2f sec\n", name, elapsed)
	return result
}

// LoadCfgs loads configuration from JSON file
func LoadCfgs(onnxDir string) (Config, error) {
	cfgPath := filepath.Join(onnxDir, "tts.json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return Config{}, err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// JSON loading helpers
func loadJSONInt64(filePath string) ([]int64, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	var result []int64
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}

	return result, nil
}

// Tensor conversion utilities
func ArrayToTensor(array [][][]float64, shape []int64) *ort.Tensor[float32] {
	// Flatten array
	totalSize := int64(1)
	for _, dim := range shape {
		totalSize *= dim
	}

	flat := make([]float32, totalSize)
	idx := 0
	for b := 0; b < len(array); b++ {
		for d := 0; d < len(array[b]); d++ {
			for t := 0; t < len(array[b][d]); t++ {
				flat[idx] = float32(array[b][d][t])
				idx++
			}
		}
	}

	tensor, err := ort.NewTensor(shape, flat)
	if err != nil {
		panic(err)
	}

	return tensor
}

func IntArrayToTensor(array [][]int64, shape []int64) *ort.Tensor[int64] {
	// Flatten array
	totalSize := int64(1)
	for _, dim := range shape {
		totalSize *= dim
	}

	flat := make([]int64, totalSize)
	idx := 0
	for b := 0; b < len(array); b++ {
		for t := 0; t < len(array[b]); t++ {
			flat[idx] = array[b][t]
			idx++
		}
	}

	tensor, err := ort.NewTensor(shape, flat)
	if err != nil {
		panic(err)
	}

	return tensor
}
