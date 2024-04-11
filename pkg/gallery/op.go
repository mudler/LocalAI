package gallery

type GalleryOp struct {
	Req         GalleryModel
	Id          string
	Galleries   []Gallery
	GalleryName string
	ConfigURL   string
}

type GalleryOpStatus struct {
	FileName           string  `json:"file_name"`
	Error              error   `json:"error"`
	Processed          bool    `json:"processed"`
	Message            string  `json:"message"`
	Progress           float64 `json:"progress"`
	TotalFileSize      string  `json:"file_size"`
	DownloadedFileSize string  `json:"downloaded_size"`
}
