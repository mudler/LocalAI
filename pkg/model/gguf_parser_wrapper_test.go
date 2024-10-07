package model_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/golang/mock/gomock"
	ggufparser "github.com/gpustack/gguf-parser-go"
	"github.com/mudler/LocalAI/pkg/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockGGUFParser is used to mock the gguf-parser methods
type MockGGUFParser struct {
	mock.Mock
}

type MockModelMemoryEstimator struct {
	mock.Mock
}

func (m *MockModelMemoryEstimator) Estimate(ggufFile *ggufparser.GGUFFile) (*model.ModelEstimate, error) {
	args := m.Called(ggufFile)
	return args.Get(0).(*model.ModelEstimate), args.Error(1)
}

func (m *MockGGUFParser) ParseGGUFFileRemote(ctx context.Context, url string) (*ggufparser.GGUFFile, error) {
	args := m.Called(ctx, url)
	return args.Get(0).(*ggufparser.GGUFFile), args.Error(1)
}

func (m *MockGGUFParser) ParseGGUFFileFromOllama(ctx context.Context, model string) (*ggufparser.GGUFFile, error) {
	args := m.Called(ctx, model)
	return args.Get(0).(*ggufparser.GGUFFile), args.Error(1)
}

func (m *MockGGUFParser) ParseGGUFFileFromHuggingFace(ctx context.Context, repo, file string) (*ggufparser.GGUFFile, error) {
	args := m.Called(ctx, repo, file)
	return args.Get(0).(*ggufparser.GGUFFile), args.Error(1)
}

func (m *MockGGUFParser) ParseGGUFFile(filePath string) (*ggufparser.GGUFFile, error) {
	args := m.Called(filePath)
	return args.Get(0).(*ggufparser.GGUFFile), args.Error(1)
}

// MockEstimateModelMemoryUsage mocks the internal function estimateModelMemoryUsage for testing
func MockEstimateModelMemoryUsage(mockGGUFFile *ggufparser.GGUFFile) *model.ModelEstimate {
	return &model.ModelEstimate{
		Estimate: model.ModelEstimateItems{
			Items: []model.ModelMemory{
				{
					OffloadLayers: 32,
					FullOffloaded: true,
					RAM: model.EstimateRAM{
						UMA:    512,
						NonUMA: 1024,
					},
					VRAMs: []model.EstimateVRAM{
						{
							UMA:    2048,
							NonUMA: 4096,
						},
					},
				},
			},
			Type:              "model",
			Architecture:      "llama",
			ContextSize:       2048,
			FlashAttention:    true,
			NoMMap:            false,
			EmbeddingOnly:     false,
			Distributable:     true,
			LogicalBatchSize:  2048,
			PhysicalBatchSize: 512,
		},
		Architecture: model.Architecture{
			Type:                 "model",
			Architecture:         "llama",
			MaximumContextLength: 2048,
			EmbeddingLength:      512,
			VocabularyLength:     32000,
		},
		Metadata: model.Metadata{
			Type:                "model",
			Architecture:        "llama",
			QuantizationVersion: 2,
			Alignment:           32,
			Name:                "Mock Model",
			License:             "open",
			FileType:            1,
			LittleEndian:        true,
			FileSize:            1024,
			Parameters:          1000000,
		},
		Tokenizer: model.Tokenizer{
			Model:        "gpt2",
			TokensLength: 32000,
			TokensSize:   512000,
		},
	}
}

func TestGetModelGGufData_URL_WithMockedEstimateModelMemoryUsage(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	// Create a new mock parser for GGUF files
	mockParser := new(MockGGUFParser)
	mockEstimator := new(MockModelMemoryEstimator)

	// Create a mock GGUFFile object
	mockGGUFFile := new(ggufparser.GGUFFile)

	// Mock the internal estimateModelMemoryUsage call
	mockEstimate := MockEstimateModelMemoryUsage(mockGGUFFile)

	// Set up the mock to return the mock GGUFFile when ParseGGUFFileRemote is called
	mockParser.On("ParseGGUFFileRemote", mock.Anything, "https://example.com/model.gguf").Return(mockGGUFFile, nil)
	mockEstimator.On("Estimate", mockGGUFFile).Return(mockEstimate, nil)

	// Call GetModelGGufData with the mocked data
	modelEstimate, err := model.GetModelGGufData("https://example.com/model.gguf", mockParser, mockEstimator, false)

	// Test assertions
	assert.Nil(t, err, "There should be no error when parsing a valid URL")
	assert.NotNil(t, modelEstimate, "ModelEstimate should not be nil")
	assert.Equal(t, "model", modelEstimate.Metadata.Type, "Expected Metadata Type is 'model'")
	assert.Equal(t, "Mock Model", modelEstimate.Metadata.Name, "Expected model name is 'Mock Model'")
	assert.Equal(t, 512, modelEstimate.Architecture.EmbeddingLength, "Expected EmbeddingLength to be 512")

	// Assert expectations
	mockParser.AssertExpectations(t)
}

func TestGetModelGGufData_Ollama_WithMockedEstimateModelMemoryUsage(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	// Create a new mock parser for GGUF files
	mockParser := new(MockGGUFParser)
	mockEstimator := new(MockModelMemoryEstimator)

	// Create a mock GGUFFile object
	mockGGUFFile := new(ggufparser.GGUFFile)

	// Mock the internal estimateModelMemoryUsage call
	mockEstimate := MockEstimateModelMemoryUsage(mockGGUFFile)

	// Set up the mock to return the mock GGUFFile when ParseGGUFFileFromOllama is called
	mockParser.On("ParseGGUFFileFromOllama", mock.Anything, "ollama").Return(mockGGUFFile, nil)
	mockEstimator.On("Estimate", mockGGUFFile).Return(mockEstimate, nil)

	// Call GetModelGGufData with the mocked data
	modelEstimate, err := model.GetModelGGufData("ollama", mockParser, mockEstimator, true)

	// Test assertions
	assert.Nil(t, err, "There should be no error when parsing a valid URL")
	assert.NotNil(t, modelEstimate, "ModelEstimate should not be nil")
	assert.Equal(t, "model", modelEstimate.Metadata.Type, "Expected Metadata Type is 'model'")
	assert.Equal(t, "Mock Model", modelEstimate.Metadata.Name, "Expected model name is 'Mock Model'")
	assert.Equal(t, 512, modelEstimate.Architecture.EmbeddingLength, "Expected EmbeddingLength to be 512")

	// Assert expectations
	mockParser.AssertExpectations(t)
}

func TestGetModelGGufData_FileOnDisk_WithMockedEstimateModelMemoryUsage(t *testing.T) {
	// Create a temporary file on disk to simulate the fileExists check
	tmpFile, err := os.CreateTemp("", "test-model.gguf")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name()) // Clean up the temp file afterward
	fmt.Println(tmpFile.Name())

	// Write some dummy data to the file
	dummyData := []byte{0x47, 0x47, 0x55, 0x46} // "GGUF" magic bytes or similar expected data
	if _, err := tmpFile.Write(dummyData); err != nil {
		t.Fatalf("Failed to write to temp file: %v", err)
	}
	if err := tmpFile.Close(); err != nil {
		t.Fatalf("Failed to close temp file: %v", err)
	}

	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	// Create a new mock parser for GGUF files
	mockParser := new(MockGGUFParser)
	mockEstimator := new(MockModelMemoryEstimator)

	// Create a mock GGUFFile object
	mockGGUFFile := new(ggufparser.GGUFFile)

	// Mock the internal estimateModelMemoryUsage call
	mockEstimate := MockEstimateModelMemoryUsage(mockGGUFFile)

	// Set up the mock to return the mock GGUFFile when ParseGGUFFile is called
	mockParser.On("ParseGGUFFile", mock.Anything).Return(mockGGUFFile, nil)
	mockEstimator.On("Estimate", mockGGUFFile).Return(mockEstimate, nil)

	// Call GetModelGGufData with the mocked data
	modelEstimate, err := model.GetModelGGufData(tmpFile.Name(), mockParser, mockEstimator, false)

	// Test assertions
	assert.Nil(t, err, "There should be no error when parsing a valid URL")
	assert.NotNil(t, modelEstimate, "ModelEstimate should not be nil")
	assert.Equal(t, "model", modelEstimate.Metadata.Type, "Expected Metadata Type is 'model'")
	assert.Equal(t, "Mock Model", modelEstimate.Metadata.Name, "Expected model name is 'Mock Model'")
	assert.Equal(t, 512, modelEstimate.Architecture.EmbeddingLength, "Expected EmbeddingLength to be 512")

	// Assert expectations
	mockParser.AssertExpectations(t)
}
