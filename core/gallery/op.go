package gallery

import "github.com/mudler/LocalAI/core/config"

type GalleryOp struct {
	Id               string
	GalleryModelName string
	ConfigURL        string
	Delete           bool

	Req       GalleryModel
	Galleries []config.Gallery
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
	GalleryModelName   string  `json:"gallery_model_name"`
}
