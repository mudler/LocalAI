package distributed

import (
	"fmt"

	"github.com/mudler/xlog"
	"gorm.io/gorm"
)

// Stores holds all Phase 4 distributed stores.
type Stores struct {
	Gallery  *GalleryStore
	FineTune *FineTuneStore
	Quant    *QuantStore
	Skills   *SkillStore

	// Responses is the durable backing for the responses.metadata SyncedMap. It
	// is what a replica re-hydrates from after its listener misses a delta;
	// without it a response created during the gap is invisible on this replica
	// forever and the same response_id answers 404 here and 200 on a peer.
	Responses *ResponseMetadataStore
}

// InitStores creates and migrates all Phase 4 distributed stores.
func InitStores(db *gorm.DB) (*Stores, error) {
	gallery, err := NewGalleryStore(db)
	if err != nil {
		return nil, fmt.Errorf("gallery store: %w", err)
	}

	ft, err := NewFineTuneStore(db)
	if err != nil {
		return nil, fmt.Errorf("fine-tune store: %w", err)
	}

	quant, err := NewQuantStore(db)
	if err != nil {
		return nil, fmt.Errorf("quantization store: %w", err)
	}

	skills, err := NewSkillStore(db)
	if err != nil {
		return nil, fmt.Errorf("skills store: %w", err)
	}

	responses, err := NewResponseMetadataStore(db)
	if err != nil {
		return nil, fmt.Errorf("response metadata store: %w", err)
	}

	xlog.Info("Distributed stores initialized (Gallery, FineTune, Quant, Skills, Responses)")
	return &Stores{
		Gallery:   gallery,
		FineTune:  ft,
		Quant:     quant,
		Skills:    skills,
		Responses: responses,
	}, nil
}
