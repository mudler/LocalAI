package services

import (
	"context"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/xsync"
)

type GalleryOp[T any, E any] struct {
	ID                 string
	GalleryElementName string
	Delete             bool

	Req T

	// If specified, we install directly the gallery element
	GalleryElement *E

	Galleries        []config.Gallery
	BackendGalleries []config.Gallery

	// Context for cancellation support
	Context    context.Context
	CancelFunc context.CancelFunc

	// External backend installation parameters (for OCI/URL/path)
	// These are used when installing backends from external sources rather than galleries
	ExternalURI   string // The OCI image, URL, or path
	ExternalName  string // Custom name for the backend
	ExternalAlias string // Custom alias for the backend
}

type GalleryOpStatus struct {
	Deletion           bool    `json:"deletion"` // Deletion is true if the operation is a deletion
	FileName           string  `json:"file_name"`
	Error              error   `json:"error"`
	Processed          bool    `json:"processed"`
	Message            string  `json:"message"`
	Progress           float64 `json:"progress"`
	TotalFileSize      string  `json:"file_size"`
	DownloadedFileSize string  `json:"downloaded_size"`
	GalleryElementName string  `json:"gallery_element_name"`
	Cancelled          bool    `json:"cancelled"`   // Cancelled is true if the operation was cancelled
	Cancellable        bool    `json:"cancellable"` // Cancellable is true if the operation can be cancelled
}

type OpCache struct {
	status         *xsync.SyncedMap[string, string]
	backendOps     *xsync.SyncedMap[string, bool] // Tracks which operations are backend operations
	galleryService *GalleryService
}

func NewOpCache(galleryService *GalleryService) *OpCache {
	return &OpCache{
		status:         xsync.NewSyncedMap[string, string](),
		backendOps:     xsync.NewSyncedMap[string, bool](),
		galleryService: galleryService,
	}
}

func (m *OpCache) Set(key string, value string) {
	m.status.Set(key, value)
}

// SetBackend sets a key-value pair and marks it as a backend operation
func (m *OpCache) SetBackend(key string, value string) {
	m.status.Set(key, value)
	m.backendOps.Set(key, true)
}

// IsBackendOp returns true if the given key is a backend operation
func (m *OpCache) IsBackendOp(key string) bool {
	return m.backendOps.Get(key)
}

func (m *OpCache) Get(key string) string {
	return m.status.Get(key)
}

func (m *OpCache) DeleteUUID(uuid string) {
	for _, k := range m.status.Keys() {
		if m.status.Get(k) == uuid {
			m.status.Delete(k)
			m.backendOps.Delete(k) // Also clean up the backend flag
		}
	}
}

func (m *OpCache) Map() map[string]string {
	return m.status.Map()
}

func (m *OpCache) Exists(key string) bool {
	return m.status.Exists(key)
}

func (m *OpCache) GetStatus() (map[string]string, map[string]string) {
	processingModelsData := m.Map()

	taskTypes := map[string]string{}

	for k, v := range processingModelsData {
		status := m.galleryService.GetStatus(v)
		taskTypes[k] = "Installation"
		if status != nil && status.Deletion {
			taskTypes[k] = "Deletion"
		} else if status == nil {
			taskTypes[k] = "Waiting"
		}
	}

	return processingModelsData, taskTypes
}
