package galleryop

import (
	"fmt"
	"sync"

	"github.com/mudler/LocalAI/pkg/modelartifacts"
)

type artifactProgressBridge struct {
	mu           sync.Mutex
	last         float64
	currentBytes int64
	totalBytes   int64
	update       func(*OpStatus)
}

func newArtifactProgressBridge(update func(*OpStatus)) *artifactProgressBridge {
	return &artifactProgressBridge{update: update}
}

func (b *artifactProgressBridge) Sink(event modelartifacts.ProgressEvent) {
	b.mu.Lock()
	progress := b.last
	message := "Preparing model files"
	switch event.Phase {
	case modelartifacts.PhaseResolving:
		progress = max(progress, 0)
		message = "Resolving model files"
	case modelartifacts.PhaseDownloading:
		if event.TotalBytes > 0 {
			progress = max(progress, min(90, float64(event.CurrentBytes)*90/float64(event.TotalBytes)))
		}
		message = fmt.Sprintf("Downloading model file: %s", event.File)
	case modelartifacts.PhaseVerifying:
		progress = max(progress, 95)
		message = "Verifying model files"
	case modelartifacts.PhaseCommitting:
		progress = max(progress, 99)
		message = "Finalizing model installation"
	case modelartifacts.PhasePersisting:
		progress = max(progress, 99)
		message = "Saving model configuration"
	}
	b.last = progress
	b.currentBytes = max(b.currentBytes, event.CurrentBytes)
	b.totalBytes = max(b.totalBytes, event.TotalBytes)
	status := &OpStatus{
		Phase: string(event.Phase), Message: message, FileName: event.File,
		Progress: progress, CurrentBytes: b.currentBytes, TotalBytes: b.totalBytes,
		Cancellable: true,
	}
	update := b.update
	b.mu.Unlock()
	if update != nil {
		update(status)
	}
}

func (b *artifactProgressBridge) ClampLegacy(progress float64) float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.last = max(b.last, progress)
	return b.last
}
